package transport

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/preparation"
	"close.local/backend/internal/quickbooks"
)

var hintFields = map[string][]string{"prepaid": {"expense_account", "prepaid_account"}, "accrual": {"expense_account", "liability_account"}, "reconciliation": {"account"}}

type usualValue struct {
	Value      string `json:"value"`
	Count      int    `json:"count"`
	LastPeriod string `json:"last_period"`
	Usable     bool   `json:"usable"`
}

// workHints tells the person confirming a reading which accounts this client's accountant approved before and whether the proposed ones exist in QuickBooks.
func workHints(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tx, err := db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			problem(w, 503, "Client memory is unavailable.")
			return
		}
		defer tx.Rollback()
		ctx, closeID := r.Context(), r.PathValue("id")
		var raw []byte
		var org, entity, period string
		err = tx.QueryRowContext(ctx, `SELECT i.proposal,c.organization_id,c.entity,c.period FROM preparation_intakes i JOIN preparation_jobs j ON j.id=i.job_id JOIN client_closes c ON c.id=j.close_id WHERE i.job_id::text=$1 AND j.close_id::text=$2`, r.PathValue("job"), closeID).Scan(&raw, &org, &entity, &period)
		if err == sql.ErrNoRows {
			problem(w, 404, "Work not found in this close.")
			return
		}
		if err != nil {
			problem(w, 503, "Client memory is unavailable.")
			return
		}
		var proposal readProposal
		json.Unmarshal(raw, &proposal)
		fields := hintFields[proposal.Kind]
		out := []map[string]any{}
		if len(fields) == 0 {
			jsonReply(w, 200, map[string]any{"fields": out})
			return
		}

		// Approved, unrevised papers of this client and kind over the 12 close months ending with this one.
		end, _ := time.Parse("2006-01", period)
		rows, err := tx.QueryContext(ctx, `SELECT c.id::text,c.period,w.paper FROM workpapers w JOIN client_closes c ON c.id=w.close_id
 WHERE c.organization_id=$1 AND lower(c.entity)=lower($2) AND c.period BETWEEN $3 AND $4 AND w.kind=$5
 AND NOT EXISTS (SELECT 1 FROM workpapers n WHERE n.close_id=w.close_id AND n.paper->>'revises'=w.id::text)
 AND (SELECT v.decision FROM workpaper_reviews v WHERE v.workpaper_id=w.id ORDER BY v.id DESC LIMIT 1)='approved' ORDER BY c.period,w.id`, org, entity, end.AddDate(0, -11, 0).Format("2006-01"), period, proposal.Kind)
		if err != nil {
			problem(w, 503, "Client memory is unavailable.")
			return
		}
		type paper struct {
			period string
			input  map[string]json.RawMessage
		}
		latest, order := map[string]paper{}, []string{}
		for rows.Next() {
			var close, when string
			var body json.RawMessage
			if err = rows.Scan(&close, &when, &body); err != nil {
				break
			}
			var p struct {
				Input map[string]json.RawMessage `json:"input"`
			}
			json.Unmarshal(body, &p)
			key := close + "\x00" + paperKey(proposal.Kind, body)
			if _, seen := latest[key]; !seen {
				order = append(order, key)
			}
			latest[key] = paper{when, p.Input}
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			problem(w, 503, "Client memory is unavailable.")
			return
		}

		// The close's newest period snapshot on its reading connection; a stale one says nothing.
		var accounts []byte
		var stale bool
		err = tx.QueryRowContext(ctx, `SELECT s.payload->'accounts',e.id IS NULL OR e.superseded_by IS NOT NULL OR x.state<>'active' FROM quickbooks_snapshots s JOIN quickbooks_connections x ON x.id=s.connection_id LEFT JOIN evidence e ON e.id::text=s.payload->>'source_id' AND e.close_id=s.close_id
 WHERE s.close_id::text=$1 AND s.payload ? 'period' AND s.connection_id=`+quickbooks.ReadingConnection("s.close_id")+` ORDER BY s.captured_at DESC,s.id DESC LIMIT 1`, closeID).Scan(&accounts, &stale)
		if err != nil && err != sql.ErrNoRows {
			problem(w, 503, "Client memory is unavailable.")
			return
		}
		var directory []struct {
			Name, FullyQualifiedName string
			Active                   *bool
		}
		ledger := err == nil && !stale && json.Unmarshal(accounts, &directory) == nil
		// find reports whether exactly one account carries this name, and whether it is active.
		find := func(value string) (found, active bool) {
			matches := 0
			for _, a := range directory {
				if value = strings.TrimSpace(value); value != "" && (strings.EqualFold(a.Name, value) || strings.EqualFold(a.FullyQualifiedName, value)) {
					matches++
					active = a.Active == nil || *a.Active
				}
			}
			return matches == 1, matches == 1 && active
		}

		procedure := map[string]string{}
		if p, err := preparation.CurrentProcedure(ctx, tx, closeID, proposal.Kind); err == nil && p.Enabled {
			procedure["expense_account"], procedure["prepaid_account"] = p.ExpenseAccount, p.PrepaidAccount
		} else if err != nil && err != sql.ErrNoRows {
			problem(w, 503, "Client memory is unavailable.")
			return
		}

		for _, field := range fields {
			proposed := ""
			for _, f := range proposal.Facts {
				if f.Field == field {
					proposed = strings.TrimSpace(f.Value)
				}
			}
			usable := func(value string) bool {
				_, active := find(value)
				return ledger && active && (procedure[field] == "" || strings.EqualFold(procedure[field], value))
			}
			usual := []usualValue{}
			for _, key := range order {
				value := strings.TrimSpace(factText(latest[key].input[field]))
				if value == "" {
					continue
				}
				i := slices.IndexFunc(usual, func(u usualValue) bool { return strings.EqualFold(u.Value, value) })
				if i < 0 {
					usual = append(usual, usualValue{Value: value})
					i = len(usual) - 1
				}
				usual[i].Count++
				usual[i].LastPeriod = latest[key].period
			}
			slices.SortStableFunc(usual, func(a, b usualValue) int {
				if a.Count != b.Count {
					return b.Count - a.Count
				}
				return strings.Compare(b.LastPeriod, a.LastPeriod)
			})
			usual = usual[:min(3, len(usual))]
			for i := range usual {
				usual[i].Usable = usable(usual[i].Value)
			}
			hint := map[string]any{"field": field, "proposed": proposed, "in_ledger": nil, "usual": usual, "procedure": procedure[field], "procedure_usable": procedure[field] != "" && usable(procedure[field])}
			if ledger && proposed != "" {
				found, _ := find(proposed)
				hint["in_ledger"] = found
			}
			out = append(out, hint)
		}
		jsonReply(w, 200, map[string]any{"fields": out})
	}
}
