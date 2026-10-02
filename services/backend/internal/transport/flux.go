package transport

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"math/big"
	"net/http"
	"slices"
	"time"

	"close.local/backend/internal/ledger"
	"close.local/backend/internal/quickbooks"
	"close.local/backend/internal/xero"
)

type fluxMonth struct {
	Period     string    `json:"period"`
	Source     string    `json:"source"`
	SnapshotID string    `json:"snapshot_id"`
	CapturedAt time.Time `json:"captured_at"`
	Stale      bool      `json:"stale"`
	realm      string
	accounts   json.RawMessage
	pl, gl, tb json.RawMessage
	xero       json.RawMessage
}

type payeeAmount struct {
	Name      string `json:"name"`
	ThisMonth string `json:"this_month"`
	LastMonth string `json:"last_month"`
}

// periodBooks is a close's newest month-end capture, from QuickBooks or Xero, whichever is newer; nil when neither.
// rowReader is a *sql.DB or a *sql.Tx.
type rowReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func periodBooks(ctx context.Context, db rowReader, closeID, period string) (*fluxMonth, error) {
	m := fluxMonth{Period: period, Source: "quickbooks"}
	err := db.QueryRowContext(ctx, `SELECT s.id::text,s.realm,s.captured_at,e.id IS NULL OR e.superseded_by IS NOT NULL OR x.state<>'active',s.payload->'accounts',s.payload->'reports'->'ProfitAndLoss',s.payload->'reports'->'GeneralLedger',COALESCE(s.payload->'reports'->'TrialBalance','null') FROM quickbooks_snapshots s JOIN quickbooks_connections x ON x.id=s.connection_id LEFT JOIN evidence e ON e.id::text=s.payload->>'source_id' AND e.close_id=s.close_id
 WHERE s.close_id::text=$1 AND s.payload ? 'period' AND s.connection_id=`+quickbooks.ReadingConnection("s.close_id")+` ORDER BY s.captured_at DESC,s.id DESC LIMIT 1`, closeID).Scan(&m.SnapshotID, &m.realm, &m.CapturedAt, &m.Stale, &m.accounts, &m.pl, &m.gl, &m.tb)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	x := fluxMonth{Period: period, Source: "xero"}
	xeroErr := db.QueryRowContext(ctx, `SELECT s.id::text,s.tenant_id,s.captured_at,x.state<>'active',s.payload FROM xero_snapshots s JOIN xero_connections x ON x.id=s.connection_id WHERE s.close_id::text=$1 ORDER BY s.captured_at DESC,s.id DESC LIMIT 1`, closeID).Scan(&x.SnapshotID, &x.realm, &x.CapturedAt, &x.Stale, &x.xero)
	if xeroErr != nil && xeroErr != sql.ErrNoRows {
		return nil, xeroErr
	}
	if xeroErr == nil && (err == sql.ErrNoRows || x.CapturedAt.After(m.CapturedAt)) {
		return &x, nil
	}
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &m, nil
}

// flux explains the largest movements in the Profit and Loss from last month's close to this one, from the payees behind them.
// Each month uses its close's newest snapshot, from QuickBooks or Xero; Xero payees come from the captured transactions.
func flux(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		wait := func(reason string) { jsonReply(w, 200, map[string]any{"state": "waiting", "reason": reason}) }
		var org, entity, period string
		if err := db.QueryRowContext(ctx, `SELECT organization_id,entity,period FROM client_closes WHERE id::text=$1`, r.PathValue("id")).Scan(&org, &entity, &period); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		month := func(closeID, period string) (*fluxMonth, error) { return periodBooks(ctx, db, closeID, period) }
		this, err := month(r.PathValue("id"), period)
		if err != nil {
			problem(w, 503, "Period snapshot unavailable.")
			return
		}
		if this == nil {
			wait("Capture this month's QuickBooks period snapshot or Xero Trial Balance first.")
			return
		}
		previous := shiftMonth(period, -1)
		var priorClose string
		err = db.QueryRowContext(ctx, `SELECT id::text FROM client_closes WHERE organization_id=$1 AND lower(entity)=lower($2) AND period=$3`, org, entity, previous).Scan(&priorClose)
		if err != nil && err != sql.ErrNoRows {
			problem(w, 503, "Last month's close is unavailable.")
			return
		}
		var last *fluxMonth
		if err == nil {
			if last, err = month(priorClose, previous); err != nil {
				problem(w, 503, "Last month's snapshot is unavailable.")
				return
			}
		}
		if last == nil {
			wait("Last month (" + previous + ") has no close with a month-end snapshot to compare against.")
			return
		}
		if last.Source != this.Source {
			wait("The two months' newest books come from different ledgers (QuickBooks and Xero), so they are not compared.")
			return
		}
		if last.realm != this.realm {
			wait("The two months were captured from different companies.")
			return
		}
		classes := map[string]string{}
		monthly := func(m *fluxMonth) (string, map[string]quickbooks.Activity, error) {
			if m.Source == "quickbooks" {
				var directory []struct {
					ID             string `json:"Id"`
					Classification string
				}
				json.Unmarshal(m.accounts, &directory)
				for _, a := range directory {
					classes[a.ID] = cmp.Or(classes[a.ID], a.Classification)
				}
				return quickbooks.MonthlyActivity(m.pl)
			}
			currency, rows, err := xero.MonthlyActivity(m.xero)
			out := map[string]quickbooks.Activity{}
			for _, row := range rows {
				out[row.ID] = quickbooks.Activity{Name: row.Name, Amount: row.Net}
				classes[row.ID] = cmp.Or(classes[row.ID], row.Class)
			}
			return currency, out, err
		}
		currency, now, err := monthly(this)
		lastCurrency, before, lastErr := monthly(last)
		if err == nil {
			err = lastErr
		}
		if err != nil {
			wait("A Profit and Loss report could not be read: " + err.Error() + ".")
			return
		}
		if currency != lastCurrency {
			wait("The two months' reports are in different currencies.")
			return
		}

		type change struct {
			id, name, class string
			now, before     *big.Rat
			delta           *big.Rat
		}
		changes := []change{}
		accounts := map[string]bool{}
		for id := range now {
			accounts[id] = true
		}
		for id := range before {
			accounts[id] = true
		}
		for _, id := range slices.Sorted(maps.Keys(accounts)) {
			c := change{id: id, class: classes[id], now: new(big.Rat), before: new(big.Rat)}
			if a, ok := now[id]; ok {
				c.name, c.now = a.Name, a.Amount
			}
			if a, ok := before[id]; ok {
				c.before = a.Amount
				c.name = cmp.Or(c.name, a.Name)
			}
			if c.delta = new(big.Rat).Sub(c.now, c.before); c.delta.Sign() != 0 {
				changes = append(changes, c)
			}
		}
		slices.SortStableFunc(changes, func(a, b change) int {
			return new(big.Rat).Abs(b.delta).Cmp(new(big.Rat).Abs(a.delta))
		})
		changes = changes[:min(8, len(changes))]
		ids := []string{}
		for _, c := range changes {
			ids = append(ids, c.id)
		}
		unavailable, foreign := "", map[string]bool{}
		var payeesNow, payeesBefore map[string]quickbooks.Payees
		if this.Source == "xero" {
			var nowForeign, beforeForeign map[string]bool
			var nowErr, beforeErr error
			payeesNow, nowForeign, nowErr = xero.PayeeActivity(this.xero, ids)
			payeesBefore, beforeForeign, beforeErr = xero.PayeeActivity(last.xero, ids)
			switch {
			case nowErr != nil:
				unavailable = "This month's Xero capture: " + nowErr.Error()
			case beforeErr != nil:
				unavailable = "Last month's Xero capture: " + beforeErr.Error()
			}
			maps.Copy(foreign, nowForeign)
			maps.Copy(foreign, beforeForeign)
		} else {
			var nowErr, beforeErr error
			payeesNow, nowErr = quickbooks.PayeeActivity(this.gl, ids)
			payeesBefore, beforeErr = quickbooks.PayeeActivity(last.gl, ids)
			if nowErr != nil || beforeErr != nil {
				unavailable = "The General Ledger could not be read by payee."
			}
		}
		out := []map[string]any{}
		when, _ := time.Parse("2006-01", period)
		for _, c := range changes {
			item := map[string]any{"account_id": c.id, "account": c.name, "class": c.class, "this_month": c.now.FloatString(2), "last_month": c.before.FloatString(2), "change": c.delta.FloatString(2)}
			if foreign[c.id] && unavailable == "" {
				item["causes_unavailable"] = "Some of this account's transactions are in another currency, and the capture does not hold their base-currency amounts."
				out = append(out, item)
				continue
			}
			if unavailable != "" {
				item["causes_unavailable"] = unavailable
				out = append(out, item)
				continue
			}
			a, b := payeesNow[c.id], payeesBefore[c.id]
			// The ledger may sign an account opposite the Profit and Loss; its month total says which way.
			for _, m := range []struct {
				p      quickbooks.Payees
				amount *big.Rat
			}{{a, c.now}, {b, c.before}} {
				total := new(big.Rat).Set(m.p.Unattributed)
				for _, v := range m.p.ByName {
					total.Add(total, v)
				}
				if total.Sign() != 0 && total.Cmp(new(big.Rat).Neg(m.amount)) == 0 {
					m.p.Unattributed.Neg(m.p.Unattributed)
					for _, v := range m.p.ByName {
						v.Neg(v)
					}
				}
			}
			added, missing, changed := []payeeAmount{}, []payeeAmount{}, []payeeAmount{}
			explained := new(big.Rat).Sub(a.Unattributed, b.Unattributed)
			for _, name := range slices.Sorted(maps.Keys(a.ByName)) {
				if before, ok := b.ByName[name]; !ok {
					added = append(added, payeeAmount{name, a.ByName[name].FloatString(2), "0.00"})
				} else if a.ByName[name].Cmp(before) != 0 {
					changed = append(changed, payeeAmount{name, a.ByName[name].FloatString(2), before.FloatString(2)})
				}
				explained.Add(explained, a.ByName[name])
			}
			for _, name := range slices.Sorted(maps.Keys(b.ByName)) {
				if _, ok := a.ByName[name]; !ok && a.Unnamed == 0 {
					missing = append(missing, payeeAmount{name, "0.00", b.ByName[name].FloatString(2)})
				}
				explained.Sub(explained, b.ByName[name])
			}
			largest := func(list []payeeAmount) []payeeAmount {
				gap := func(p payeeAmount) *big.Rat {
					x, _ := new(big.Rat).SetString(p.ThisMonth)
					y, _ := new(big.Rat).SetString(p.LastMonth)
					return x.Abs(x.Sub(x, y))
				}
				slices.SortStableFunc(list, func(p, q payeeAmount) int { return gap(q).Cmp(gap(p)) })
				return list[:min(3, len(list))]
			}
			item["omitted"] = max(0, len(added)-3) + max(0, len(missing)-3) + max(0, len(changed)-3)
			item["new"], item["missing"], item["changed"] = largest(added), largest(missing), largest(changed)
			item["unattributed"] = map[string]string{"this_month": a.Unattributed.FloatString(2), "last_month": b.Unattributed.FloatString(2)}
			item["reconciled"] = explained.Cmp(c.delta) == 0
			// Questions are written for the client, who sees them once a person approves a request.
			questions := []string{}
			for _, p := range item["missing"].([]payeeAmount) {
				questions = append(questions, fmt.Sprintf("%s: %s was %s in %s, and nothing is recorded for them in %s yet. Is there a bill or invoice for %s? If so, please send a copy.", c.name, p.Name, ledger.Show(p.LastMonth, currency), when.AddDate(0, -1, 0).Format("January 2006"), when.Format("January 2006"), when.Format("January")))
			}
			item["questions"] = questions
			out = append(out, item)
		}
		jsonReply(w, 200, map[string]any{"state": "ready", "currency": currency, "this": this, "last": last, "changes": out, "note": "Figures are as captured in each month's snapshot; later postings are not included."})
	}
}
