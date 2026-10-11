package transport

import (
	"context"
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
	Value       string    `json:"value"`
	Count       int       `json:"count"`
	LastPeriod  string    `json:"last_period"`
	Usable      bool      `json:"usable"`
	CloseID     string    `json:"close_id"`
	WorkpaperID string    `json:"workpaper_id"`
	ApprovedBy  string    `json:"approved_by"`
	ApprovedAt  time.Time `json:"approved_at"`
}

// approvedAccounts is what the client's approved, unrevised papers of one kind named in each account field over the 12 close
// months ending with period: the top three values, each with the latest paper that used it and who approved that paper.
func approvedAccounts(ctx context.Context, tx *sql.Tx, org, entity, period, kind string) (map[string][]usualValue, error) {
	end, _ := time.Parse("2006-01", period)
	// A draft revised since, or whose sources changed after its approval, no longer counts as approved.
	rows, err := tx.QueryContext(ctx, `SELECT c.id::text,c.period,w.id::text,w.paper,v.actor,v.created_at FROM workpaper_review_state w JOIN client_closes c ON c.id=w.close_id
 JOIN workpaper_reviews v ON v.id::text=w.review_id
 WHERE c.organization_id=$1 AND lower(c.entity)=lower($2) AND c.period BETWEEN $3 AND $4 AND w.kind=$5 AND w.review_state='approved' ORDER BY c.period,w.id`, org, entity, end.AddDate(0, -11, 0).Format("2006-01"), period, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type paper struct {
		usualValue
		input map[string]json.RawMessage
	}
	latest, order := map[string]paper{}, []string{}
	for rows.Next() {
		var p paper
		var body json.RawMessage
		if err = rows.Scan(&p.CloseID, &p.LastPeriod, &p.WorkpaperID, &body, &p.ApprovedBy, &p.ApprovedAt); err != nil {
			return nil, err
		}
		var parsed struct {
			Input map[string]json.RawMessage `json:"input"`
		}
		json.Unmarshal(body, &parsed)
		p.input = parsed.Input
		key := p.CloseID + "\x00" + paperKey(kind, body)
		if _, seen := latest[key]; !seen {
			order = append(order, key)
		}
		latest[key] = p
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := map[string][]usualValue{}
	for _, field := range hintFields[kind] {
		usual := []usualValue{}
		for _, key := range order {
			p := latest[key]
			value := strings.TrimSpace(factText(p.input[field]))
			if value == "" {
				continue
			}
			i := slices.IndexFunc(usual, func(u usualValue) bool { return strings.EqualFold(u.Value, value) })
			if i < 0 {
				usual = append(usual, usualValue{Value: value})
				i = len(usual) - 1
			}
			count := usual[i].Count + 1
			usual[i] = p.usualValue
			usual[i].Value, usual[i].Count = value, count
		}
		slices.SortStableFunc(usual, func(a, b usualValue) int {
			if a.Count != b.Count {
				return b.Count - a.Count
			}
			return strings.Compare(b.LastPeriod, a.LastPeriod)
		})
		out[field] = usual[:min(3, len(usual))]
	}
	return out, nil
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

		approved, err := approvedAccounts(ctx, tx, org, entity, period, proposal.Kind)
		if err != nil {
			problem(w, 503, "Client memory is unavailable.")
			return
		}

		// The chart of accounts in the close's newest snapshot of its month's source, a QuickBooks period snapshot on its
		// reading connection or a Xero capture; a stale one says nothing.
		var accounts []byte
		var stale bool
		err = tx.QueryRowContext(ctx, `SELECT accounts,stale FROM (
 (SELECT s.payload->'accounts' AS accounts,e.id IS NULL OR e.superseded_by IS NOT NULL OR x.state<>'active' AS stale,s.captured_at FROM quickbooks_snapshots s JOIN quickbooks_connections x ON x.id=s.connection_id LEFT JOIN evidence e ON e.id::text=s.payload->>'source_id' AND e.close_id=s.close_id
  WHERE s.close_id::text=$1 AND s.payload ? 'period' AND s.connection_id=`+quickbooks.ReadingConnection("s.close_id")+` AND `+sourceAllows("s.close_id", "quickbooks")+` ORDER BY s.captured_at DESC,s.id DESC LIMIT 1)
 UNION ALL
 (SELECT COALESCE((SELECT jsonb_agg(jsonb_build_object('Name',a->>'Name','Active',a->>'Status'='ACTIVE')) FROM jsonb_array_elements(s.payload->'accounts'->'Accounts') a),'[]'::jsonb),x.state<>'active',s.captured_at FROM xero_snapshots s JOIN xero_connections x ON x.id=s.connection_id
  WHERE s.close_id::text=$1 AND `+sourceAllows("s.close_id", "xero")+` ORDER BY s.captured_at DESC,s.id DESC LIMIT 1)) latest ORDER BY captured_at DESC LIMIT 1`, closeID).Scan(&accounts, &stale)
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
			usual := approved[field]
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

// clientMemory shows what the system remembers about the close's client, with who decided it and when, and retires an alias.
// Rules for future preparation are the client's procedures, shown and changed with their impact on the same page.
func clientMemory(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, closeID := r.Context(), r.PathValue("id")
		if r.Method == "POST" {
			actor, ok := reviewActor(w, r)
			if !ok {
				return
			}
			var in struct {
				Action    string `json:"action"`
				Alias     string `json:"alias"`
				Reason    string `json:"reason"`
				Confirmed bool   `json:"confirmed"`
			}
			if !decode(w, r, &in) {
				return
			}
			in.Reason = strings.TrimSpace(in.Reason)
			if in.Action != "retire_alias" || !in.Confirmed || in.Reason == "" || len(in.Reason) > 2000 {
				problem(w, 422, "Confirm retiring the name and give a reason (1–2,000 characters).")
				return
			}
			var retired string
			err := db.QueryRowContext(ctx, `UPDATE client_aliases a SET retired_by=$3,retired_at=now(),retire_reason=$4 FROM client_closes c
 WHERE c.id::text=$1 AND a.organization_id=c.organization_id AND lower(a.client_entity)=lower(c.entity) AND a.alias=$2 AND a.retired_at IS NULL RETURNING a.alias`, closeID, in.Alias, actor, in.Reason).Scan(&retired)
			if err == sql.ErrNoRows {
				problem(w, 404, "This client has no such name in use. Refresh the client's memory.")
				return
			}
			if err != nil {
				problem(w, 503, "The name could not be retired.")
				return
			}
			jsonReply(w, 200, map[string]string{"retired": retired})
			return
		}
		tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			problem(w, 503, "Client memory is unavailable.")
			return
		}
		defer tx.Rollback()
		var org, entity, period string
		if err = tx.QueryRowContext(ctx, `SELECT organization_id,entity,period FROM client_closes WHERE id::text=$1`, closeID).Scan(&org, &entity, &period); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		type alias struct {
			Alias        string     `json:"alias"`
			ConfirmedBy  string     `json:"confirmed_by"`
			ConfirmedAt  time.Time  `json:"confirmed_at"`
			RetiredBy    string     `json:"retired_by,omitempty"`
			RetiredAt    *time.Time `json:"retired_at,omitempty"`
			RetireReason string     `json:"retire_reason,omitempty"`
		}
		type grant struct {
			ID          string    `json:"id"`
			CloseID     string    `json:"close_id"`
			Period      string    `json:"period"`
			WorkpaperID string    `json:"workpaper_id"`
			Kind        string    `json:"kind"`
			GrantedBy   string    `json:"granted_by"`
			Reason      string    `json:"reason"`
			CreatedAt   time.Time `json:"created_at"`
			InForce     bool      `json:"in_force"`
			reviewID    string
		}
		type account struct {
			Kind   string       `json:"kind"`
			Field  string       `json:"field"`
			Values []usualValue `json:"values"`
		}
		aliases, grants, accounts := []alias{}, []grant{}, []account{}
		rows, err := tx.QueryContext(ctx, `SELECT alias,confirmed_by,confirmed_at,COALESCE(retired_by,''),retired_at,COALESCE(retire_reason,'') FROM client_aliases WHERE organization_id=$1 AND lower(client_entity)=lower($2) ORDER BY retired_at IS NOT NULL,confirmed_at DESC`, org, entity)
		for err == nil && rows.Next() {
			var a alias
			if err = rows.Scan(&a.Alias, &a.ConfirmedBy, &a.ConfirmedAt, &a.RetiredBy, &a.RetiredAt, &a.RetireReason); err == nil {
				aliases = append(aliases, a)
			}
		}
		if err == nil {
			err = rows.Err()
			rows.Close()
		}
		if err == nil {
			rows, err = tx.QueryContext(ctx, `SELECT a.id::text,g.close_id::text,gc.period,a.workpaper_id::text,g.kind,a.review_id::text,a.granted_by,a.reason,a.created_at FROM standing_approvals a JOIN workpapers g ON g.id=a.workpaper_id JOIN client_closes gc ON gc.id=g.close_id
 WHERE a.organization_id=$1 AND lower(a.client_entity)=lower($2) AND a.revoked_at IS NULL ORDER BY a.id DESC`, org, entity)
			for err == nil && rows.Next() {
				var g grant
				if err = rows.Scan(&g.ID, &g.CloseID, &g.Period, &g.WorkpaperID, &g.Kind, &g.reviewID, &g.GrantedBy, &g.Reason, &g.CreatedAt); err == nil {
					grants = append(grants, g)
				}
			}
			if err == nil {
				err = rows.Err()
				rows.Close()
			}
		}
		// An unrevoked grant stops applying once its approval changes or its granter leaves; the same check as approval uses.
		for i := range grants {
			if err == nil {
				err = tx.QueryRowContext(ctx, grantInForce, grants[i].WorkpaperID, grants[i].reviewID, org, grants[i].GrantedBy).Scan(&grants[i].InForce)
			}
		}
		for _, kind := range []string{"prepaid", "accrual", "reconciliation"} {
			if err != nil {
				break
			}
			var approved map[string][]usualValue
			if approved, err = approvedAccounts(ctx, tx, org, entity, period, kind); err == nil {
				for _, field := range hintFields[kind] {
					if len(approved[field]) > 0 {
						accounts = append(accounts, account{kind, field, approved[field]})
					}
				}
			}
		}
		if err != nil {
			problem(w, 503, "Client memory is unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"entity": entity, "aliases": aliases, "standing_approvals": grants, "accounts": accounts})
	}
}
