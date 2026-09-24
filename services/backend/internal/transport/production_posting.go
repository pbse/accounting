package transport

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/quickbooks"
	"close.local/backend/internal/secrets"
)

type productionGrant struct {
	ID           string    `json:"id"`
	Realm        string    `json:"realm"`
	Company      string    `json:"company"`
	PrepaidID    string    `json:"prepaid_account_id"`
	ExpenseID    string    `json:"expense_account_id"`
	MaxAmount    string    `json:"max_amount"`
	Expires      time.Time `json:"expires_at"`
	Reason       string    `json:"reason"`
	GrantedBy    string    `json:"granted_by"`
	ConnectionID string    `json:"connection_id"`
}
type productionPosting struct {
	posting
	GrantID      string                       `json:"grant_id"`
	ConnectionID string                       `json:"connection_id"`
	LedgerBefore quickbooks.LedgerObservation `json:"ledger_before"`
	Projected    map[string]string            `json:"projected_balances,omitempty"`
	Conflicts    []string                     `json:"other_open_intents"`
	LedgerAfter  json.RawMessage              `json:"ledger_after,omitempty"`
	VerifiedAt   *time.Time                   `json:"verified_at,omitempty"`
}

type rehearsalInput struct {
	PaperID     string
	ReviewState string
	Posting     productionPosting
}
type rehearsalAccount struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Balance   string `json:"balance"`
	SyncToken string `json:"sync_token"`
	Delta     string `json:"delta"`
	Projected string `json:"projected"`
	baseline  *big.Rat
	change    *big.Rat
}
type closeRehearsal struct {
	Company        string             `json:"company"`
	Accounts       []rehearsalAccount `json:"accounts"`
	Blockers       []string           `json:"blockers"`
	ReviewRequired []string           `json:"review_required"`
	IntentCount    int                `json:"intent_count"`
}

func productionActor(w http.ResponseWriter, r *http.Request, permission string) (*auth.Claims, bool) {
	a := auth.Actor(r.Context())
	if a == nil || a.Organization == "" || (permission != "" && !slices.Contains(a.Permissions, permission)) {
		message := "A hosted client-authorized session is required."
		if permission != "" {
			message = "A hosted client-authorized session with " + permission + " is required."
		}
		problem(w, 403, message)
		return nil, false
	}
	return a, true
}
func productionScope(ctx context.Context, db *sql.DB, closeID, org string) (string, error) {
	var entity string
	err := db.QueryRowContext(ctx, `SELECT entity FROM client_closes WHERE id::text=$1 AND organization_id=$2`, closeID, org).Scan(&entity)
	return entity, err
}
func readProductionGrant(ctx context.Context, db *sql.DB, org, entity string) (productionGrant, error) {
	var g productionGrant
	err := db.QueryRowContext(ctx, `SELECT g.id::text,g.realm,x.company_name,g.prepaid_account_id,g.expense_account_id,g.max_amount::text,g.expires_at,g.reason,g.granted_by,g.connection_id::text FROM production_posting_grants g JOIN quickbooks_connections x ON x.id=g.connection_id WHERE g.organization_id=$1 AND lower(g.client_entity)=lower($2) AND g.revoked_at IS NULL LIMIT 1`, org, entity).Scan(&g.ID, &g.Realm, &g.Company, &g.PrepaidID, &g.ExpenseID, &g.MaxAmount, &g.Expires, &g.Reason, &g.GrantedBy, &g.ConnectionID)
	return g, err
}

type productionReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readProductionPosting(ctx context.Context, db productionReader, closeID, paperID string) (productionPosting, error) {
	var p productionPosting
	var before []byte
	err := db.QueryRowContext(ctx, `SELECT id::text,request_id::text,digest,realm,state,journal,COALESCE(approved_by,''),approval_expires_at,COALESCE(external_id,''),last_error,grant_id::text,connection_id::text,ledger_before,COALESCE(ledger_after,'null'::jsonb),verified_at FROM production_postings WHERE close_id::text=$1 AND workpaper_id::text=$2`, closeID, paperID).Scan(&p.ID, &p.RequestID, &p.Digest, &p.Realm, &p.State, &p.Journal, &p.ApprovedBy, &p.Expires, &p.ExternalID, &p.Error, &p.GrantID, &p.ConnectionID, &before, &p.LedgerAfter, &p.VerifiedAt)
	if err == nil {
		err = json.Unmarshal(before, &p.LedgerBefore)
	}
	if err == nil {
		var journal quickbooks.Journal
		if json.Unmarshal(p.Journal, &journal) != nil || len(journal.Line) != 2 {
			return p, fmt.Errorf("saved journal is invalid")
		}
		p.Projected, err = projectedBalances(p.LedgerBefore, journal.Line[0].Amount)
	}
	if err == nil {
		var raw []byte
		err = db.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(id::text ORDER BY id),'[]'::jsonb) FROM production_postings WHERE connection_id::text=$1 AND id::text<>$2 AND (state IN ('executing','unknown') OR (state='approved' AND approval_expires_at>now())) AND (ledger_before->'prepaid'->>'id' IN ($3,$4) OR ledger_before->'expense'->>'id' IN ($3,$4))`, p.ConnectionID, p.ID, p.LedgerBefore.Prepaid.ID, p.LedgerBefore.Expense.ID).Scan(&raw)
		if err == nil {
			err = json.Unmarshal(raw, &p.Conflicts)
		}
	}
	return p, err
}

func projectedBalances(before quickbooks.LedgerObservation, amount json.Number) (map[string]string, error) {
	delta, ok := new(big.Rat).SetString(string(amount))
	if !ok || delta.Sign() <= 0 {
		return nil, fmt.Errorf("journal amount is invalid")
	}
	out := map[string]string{}
	for _, item := range []struct {
		name, balance string
		sign          int64
	}{{"prepaid", string(before.Prepaid.Balance), 1}, {"expense", string(before.Expense.Balance), -1}} {
		current, valid := new(big.Rat).SetString(item.balance)
		if !valid {
			return nil, fmt.Errorf("versioned balance is invalid")
		}
		projected := new(big.Rat).Add(current, new(big.Rat).Mul(delta, big.NewRat(item.sign, 1)))
		cents := new(big.Rat).Mul(projected, big.NewRat(100, 1))
		if !cents.IsInt() {
			return nil, fmt.Errorf("projected balance is not exact cents")
		}
		out[item.name] = projected.FloatString(2)
	}
	return out, nil
}

func rehearseProduction(items []rehearsalInput, hasMore bool) closeRehearsal {
	out := closeRehearsal{Accounts: []rehearsalAccount{}, Blockers: []string{}, ReviewRequired: []string{}, IntentCount: len(items)}
	if hasMore {
		out.Blockers = append(out.Blockers, "More than 100 saved proposals exist. Narrow or resolve them before relying on a combined state.")
	}
	included := map[string]bool{}
	for _, item := range items {
		included[item.Posting.ID] = true
	}
	competing := map[string]bool{}
	accounts := map[string]*rehearsalAccount{}
	var connection, company, realm string
	for _, item := range items {
		p := item.Posting
		for _, id := range p.Conflicts {
			if !included[id] && !competing[id] {
				out.Blockers = append(out.Blockers, "Competing posting #"+id+" affects this company's saved account snapshot. Resolve it before relying on a combined state.")
				competing[id] = true
			}
		}
		if item.ReviewState != "approved" {
			out.ReviewRequired = append(out.ReviewRequired, item.PaperID)
		}
		if p.ConnectionID == "" || p.Realm == "" || p.LedgerBefore.Company == "" {
			out.Blockers = append(out.Blockers, "Draft #"+item.PaperID+" lacks a confirmed company or connection.")
			continue
		}
		if connection == "" {
			connection, company, realm = p.ConnectionID, p.LedgerBefore.Company, p.Realm
			out.Company = company
		} else if p.ConnectionID != connection || p.LedgerBefore.Company != company || p.Realm != realm {
			out.Blockers = append(out.Blockers, "Draft #"+item.PaperID+" uses a different QuickBooks company or connection.")
			continue
		}
		var journal quickbooks.Journal
		if json.Unmarshal(p.Journal, &journal) != nil || journal.CurrencyRef.Value != "USD" || len(journal.Line) != 2 || journal.Line[0].DetailType != "JournalEntryLineDetail" || journal.Line[1].DetailType != "JournalEntryLineDetail" || journal.Line[0].JournalEntryLineDetail.PostingType != "Debit" || journal.Line[1].JournalEntryLineDetail.PostingType != "Credit" || journal.Line[0].JournalEntryLineDetail.AccountRef.Value != p.LedgerBefore.Prepaid.ID || journal.Line[1].JournalEntryLineDetail.AccountRef.Value != p.LedgerBefore.Expense.ID || p.LedgerBefore.Prepaid.ID == p.LedgerBefore.Expense.ID {
			out.Blockers = append(out.Blockers, "Draft #"+item.PaperID+" has an incompatible saved journal.")
			continue
		}
		amount, err := prepaids.Amount(string(journal.Line[0].Amount))
		other, otherErr := prepaids.Amount(string(journal.Line[1].Amount))
		if err != nil || otherErr != nil || amount <= 0 || amount != other {
			out.Blockers = append(out.Blockers, "Draft #"+item.PaperID+" has an invalid or unbalanced amount.")
			continue
		}
		for _, effect := range []struct {
			account quickbooks.LedgerAccount
			delta   int64
		}{{p.LedgerBefore.Prepaid, amount}, {p.LedgerBefore.Expense, -amount}} {
			balance, valid := new(big.Rat).SetString(string(effect.account.Balance))
			if !valid || !new(big.Rat).Mul(balance, big.NewRat(100, 1)).IsInt() || effect.account.ID == "" || effect.account.SyncToken == "" {
				out.Blockers = append(out.Blockers, "Draft #"+item.PaperID+" lacks a valid versioned account balance.")
				continue
			}
			current := accounts[effect.account.ID]
			if current == nil {
				current = &rehearsalAccount{ID: effect.account.ID, Name: effect.account.Name, SyncToken: effect.account.SyncToken, baseline: balance, change: new(big.Rat)}
				accounts[effect.account.ID] = current
			} else if current.baseline.Cmp(balance) != 0 || current.SyncToken != effect.account.SyncToken || current.Name != effect.account.Name {
				out.Blockers = append(out.Blockers, "Draft #"+item.PaperID+" uses a different balance or version for account #"+effect.account.ID+".")
				continue
			}
			current.change.Add(current.change, new(big.Rat).SetFrac(big.NewInt(effect.delta), big.NewInt(100)))
		}
	}
	if len(out.Blockers) > 0 {
		return out
	}
	for _, account := range accounts {
		account.Balance = account.baseline.FloatString(2)
		account.Delta = account.change.FloatString(2)
		account.Projected = new(big.Rat).Add(account.baseline, account.change).FloatString(2)
		out.Accounts = append(out.Accounts, *account)
	}
	slices.SortFunc(out.Accounts, func(a, b rehearsalAccount) int { return strings.Compare(a.ID, b.ID) })
	return out
}
func productionDigest(grantID, paperDigest string, j quickbooks.Journal, o quickbooks.LedgerObservation) string {
	raw, _ := json.Marshal([]any{grantID, paperDigest, j, o})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
func ledgerAfterMatches(before, after quickbooks.LedgerObservation, amount json.Number) bool {
	delta, ok := new(big.Rat).SetString(string(amount))
	if !ok || delta.Sign() <= 0 || after.Company != before.Company || after.Prepaid.ID != before.Prepaid.ID || after.Expense.ID != before.Expense.ID {
		return false
	}
	for _, item := range []struct {
		old, next json.Number
		sign      int64
	}{{before.Prepaid.Balance, after.Prepaid.Balance, 1}, {before.Expense.Balance, after.Expense.Balance, -1}} {
		old, a := new(big.Rat).SetString(string(item.old))
		next, b := new(big.Rat).SetString(string(item.next))
		if !a || !b || next.Cmp(new(big.Rat).Add(old, new(big.Rat).Mul(delta, big.NewRat(item.sign, 1)))) != 0 {
			return false
		}
	}
	return true
}
func registerProductionPosting(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/{id}/proposed-state", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := productionActor(w, r, ""); !ok {
			return
		}
		tx, err := db.BeginTx(r.Context(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			problem(w, 503, "Proposed state unavailable.")
			return
		}
		defer tx.Rollback()
		rows, err := tx.QueryContext(r.Context(), `SELECT workpaper_id::text FROM production_postings WHERE close_id::text=$1 AND state IN ('preview','approved') ORDER BY id DESC LIMIT 101`, r.PathValue("id"))
		if err != nil {
			problem(w, 503, "Proposed state unavailable.")
			return
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if rows.Scan(&id) != nil {
				rows.Close()
				problem(w, 503, "Proposed state unavailable.")
				return
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			problem(w, 503, "Proposed state unavailable.")
			return
		}
		more := len(ids) > 100
		if more {
			ids = ids[:100]
		}
		items := []map[string]any{}
		rehearsalItems := []rehearsalInput{}
		for _, id := range ids {
			intent, err := readProductionPosting(r.Context(), tx, r.PathValue("id"), id)
			if err != nil {
				problem(w, 503, "A saved proposal could not be read. Resolve it before relying on this preview.")
				return
			}
			var state string
			if err = tx.QueryRowContext(r.Context(), `SELECT review_state FROM workpaper_review_state WHERE id::text=$1 AND close_id::text=$2`, id, r.PathValue("id")).Scan(&state); err != nil {
				problem(w, 503, "Current workpaper review unavailable.")
				return
			}
			items = append(items, map[string]any{"workpaper_id": id, "state": intent.State, "review_state": state, "digest": intent.Digest, "ledger_before": intent.LedgerBefore, "projected_balances": intent.Projected, "other_open_intents": intent.Conflicts})
			rehearsalItems = append(rehearsalItems, rehearsalInput{PaperID: id, ReviewState: state, Posting: intent})
		}
		rehearsal := rehearseProduction(rehearsalItems, more)
		var uncertain int
		if err := tx.QueryRowContext(r.Context(), `SELECT count(*) FROM production_postings WHERE close_id::text=$1 AND state IN ('executing','unknown')`, r.PathValue("id")).Scan(&uncertain); err != nil {
			problem(w, 503, "Could not check unsettled production attempts.")
			return
		}
		if uncertain > 0 {
			rehearsal.Blockers = append(rehearsal.Blockers, fmt.Sprintf("%d production posting attempt(s) have an uncertain outcome. Investigate before relying on a combined state.", uncertain))
			rehearsal.Accounts = []rehearsalAccount{}
		}
		if tx.Commit() != nil {
			problem(w, 503, "Proposed state could not be confirmed.")
			return
		}
		jsonReply(w, 200, map[string]any{"items": items, "has_more": more, "rehearsal": rehearsal, "notice": "Combined production journal proposal from saved, versioned account observations. Sandbox corrections and verified postings are outside this calculation. This is not a current ledger balance or official statement; every intent is rechecked before approval and posting."})
	})
	grantPath := "/v1/closes/{id}/production-grant"
	mux.HandleFunc("GET "+grantPath, func(w http.ResponseWriter, r *http.Request) {
		a, ok := productionActor(w, r, "")
		if !ok {
			return
		}
		entity, err := productionScope(r.Context(), db, r.PathValue("id"), a.Organization)
		if err != nil {
			problem(w, 404, "Client close not found.")
			return
		}
		access, err := clientAccess(db, r, entity)
		if err != nil || !access {
			problem(w, 403, "Client access is required.")
			return
		}
		g, err := readProductionGrant(r.Context(), db, a.Organization, entity)
		if err == sql.ErrNoRows {
			jsonReply(w, 200, map[string]string{"state": "not_granted"})
			return
		}
		if err != nil {
			problem(w, 503, "Grant unavailable.")
			return
		}
		jsonReply(w, 200, g)
	})
	mux.HandleFunc("POST "+grantPath, func(w http.ResponseWriter, r *http.Request) {
		a, ok := productionActor(w, r, "posting:approve")
		if !ok {
			return
		}
		entity, err := productionScope(r.Context(), db, r.PathValue("id"), a.Organization)
		if err != nil {
			problem(w, 404, "Client close not found.")
			return
		}
		access, err := clientAccess(db, r, entity)
		if err != nil || !access {
			problem(w, 403, "Client access is required.")
			return
		}
		var in struct {
			Action       string    `json:"action"`
			ConnectionID string    `json:"connection_id"`
			GrantID      string    `json:"grant_id"`
			PrepaidID    string    `json:"prepaid_account_id"`
			ExpenseID    string    `json:"expense_account_id"`
			MaxAmount    string    `json:"max_amount"`
			Reason       string    `json:"reason"`
			Confirmation string    `json:"confirmation"`
			ExpiresAt    time.Time `json:"expires_at"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Action == "revoke" {
			if in.Confirmation != "REVOKE PRODUCTION GRANT" {
				problem(w, 422, "Type REVOKE PRODUCTION GRANT.")
				return
			}
			result, e := db.ExecContext(r.Context(), `UPDATE production_posting_grants SET revoked_at=now(),revoked_by=$4 WHERE id::text=$1 AND organization_id=$2 AND lower(client_entity)=lower($3) AND revoked_at IS NULL`, in.GrantID, a.Organization, entity, a.Subject)
			if e != nil {
				problem(w, 503, "Grant revocation unavailable.")
				return
			}
			n, _ := result.RowsAffected()
			if n != 1 {
				problem(w, 409, "Grant changed. Refresh before deciding.")
				return
			}
			jsonReply(w, 200, map[string]string{"state": "revoked"})
			return
		}
		amount, amountErr := prepaids.Amount(in.MaxAmount)
		if in.Action != "grant" || in.Confirmation != "GRANT PRODUCTION POSTING" || !quickbooks.ValidRealm(in.PrepaidID) || !quickbooks.ValidRealm(in.ExpenseID) || in.PrepaidID == in.ExpenseID || amountErr != nil || amount <= 0 || in.ExpiresAt.Before(time.Now()) || in.ExpiresAt.After(time.Now().Add(30*24*time.Hour)) || len(strings.TrimSpace(in.Reason)) < 10 || len(in.Reason) > 2000 {
			problem(w, 422, "Confirm distinct account IDs, a positive limit, expiry within 30 days and a reason of at least 10 characters.")
			return
		}
		var realm, company string
		err = db.QueryRowContext(r.Context(), `SELECT realm,company_name FROM quickbooks_connections WHERE id::text=$1 AND organization_id=$2 AND lower(client_entity)=lower($3) AND environment='production' AND state='active'`, in.ConnectionID, a.Organization, entity).Scan(&realm, &company)
		if err != nil || !quickbooks.ValidRealm(realm) || company != entity {
			problem(w, 409, "Confirm the production QuickBooks company for this exact client first.")
			return
		}
		var id string
		err = db.QueryRowContext(r.Context(), `INSERT INTO production_posting_grants(organization_id,client_entity,connection_id,realm,prepaid_account_id,expense_account_id,max_amount,expires_at,reason,granted_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT DO NOTHING RETURNING id::text`, a.Organization, entity, in.ConnectionID, realm, in.PrepaidID, in.ExpenseID, in.MaxAmount, in.ExpiresAt, strings.TrimSpace(in.Reason), a.Subject).Scan(&id)
		if err != nil {
			problem(w, 409, "A current grant exists or the grant could not be saved. Refresh before deciding.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": id, "state": "granted"})
	})
	path := "/v1/closes/{id}/workpapers/{paper}/production-posting"
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		a, ok := productionActor(w, r, "")
		if !ok {
			return
		}
		entity, err := productionScope(r.Context(), db, r.PathValue("id"), a.Organization)
		if err != nil {
			problem(w, 404, "Client close not found.")
			return
		}
		access, err := clientAccess(db, r, entity)
		if err != nil || !access {
			problem(w, 403, "Client access is required.")
			return
		}
		p, err := readProductionPosting(r.Context(), db, r.PathValue("id"), r.PathValue("paper"))
		if err == sql.ErrNoRows {
			jsonReply(w, 200, map[string]string{"state": "not_prepared"})
			return
		}
		if err != nil {
			problem(w, 503, "Posting status unavailable.")
			return
		}
		jsonReply(w, 200, p)
	})
	mux.HandleFunc("POST "+path, func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Action, Digest, Confirmation string
			ExternalID                   string `json:"external_id"`
		}
		if !decode(w, r, &in) {
			return
		}
		permission := "posting:approve"
		if in.Action == "execute" || in.Action == "verify" {
			permission = "posting:execute"
		}
		a, ok := productionActor(w, r, permission)
		if !ok {
			return
		}
		entity, err := productionScope(r.Context(), db, r.PathValue("id"), a.Organization)
		if err != nil {
			problem(w, 404, "Client close not found.")
			return
		}
		access, err := clientAccess(db, r, entity)
		if err != nil || !access {
			problem(w, 403, "Client access is required.")
			return
		}
		var paperRaw []byte
		var paperDigest, period string
		err = db.QueryRowContext(r.Context(), `SELECT w.paper,w.digest,c.period FROM workpapers w JOIN client_closes c ON c.id=w.close_id WHERE w.id::text=$1 AND w.close_id::text=$2 AND w.kind='prepaid' AND c.organization_id=$3`, r.PathValue("paper"), r.PathValue("id"), a.Organization).Scan(&paperRaw, &paperDigest, &period)
		if err != nil {
			problem(w, 404, "Saved prepaid workpaper not found.")
			return
		}
		var paper prepaids.Workpaper
		if json.Unmarshal(paperRaw, &paper) != nil || paper.Input.Entity != entity || paper.Input.ClosePeriod != period {
			problem(w, 409, "Workpaper client or period does not match this close.")
			return
		}
		record, recordErr := readProductionPosting(r.Context(), db, r.PathValue("id"), r.PathValue("paper"))
		var g productionGrant
		if in.Action == "verify" && recordErr == nil {
			err = db.QueryRowContext(r.Context(), `SELECT g.id::text,g.realm,x.company_name,g.prepaid_account_id,g.expense_account_id,g.max_amount::text,g.expires_at,g.reason,g.granted_by,g.connection_id::text FROM production_posting_grants g JOIN quickbooks_connections x ON x.id=g.connection_id WHERE g.id=$1 AND g.organization_id=$2 AND lower(g.client_entity)=lower($3)`, record.GrantID, a.Organization, entity).Scan(&g.ID, &g.Realm, &g.Company, &g.PrepaidID, &g.ExpenseID, &g.MaxAmount, &g.Expires, &g.Reason, &g.GrantedBy, &g.ConnectionID)
		} else {
			g, err = readProductionGrant(r.Context(), db, a.Organization, entity)
		}
		if err != nil || (in.Action != "verify" && time.Now().After(g.Expires)) {
			problem(w, 409, "An unexpired client-specific production grant is required for new decisions.")
			return
		}
		var encrypted []byte
		var expires time.Time
		connectionID := g.ConnectionID
		if in.Action == "verify" {
			err = db.QueryRowContext(r.Context(), `SELECT id::text,credential,access_expires_at FROM quickbooks_connections WHERE organization_id=$1 AND lower(client_entity)=lower($2) AND environment='production' AND state='active' AND realm=$3 AND company_name=$2`, a.Organization, entity, g.Realm).Scan(&connectionID, &encrypted, &expires)
		} else {
			err = db.QueryRowContext(r.Context(), `SELECT credential,access_expires_at FROM quickbooks_connections WHERE id=$1 AND organization_id=$2 AND lower(client_entity)=lower($3) AND environment='production' AND state='active' AND realm=$4 AND company_name=$3`, connectionID, a.Organization, entity, g.Realm).Scan(&encrypted, &expires)
		}
		if err != nil || time.Until(expires) < 2*time.Minute {
			problem(w, 409, "Refresh the active production connection before posting.")
			return
		}
		raw, err := secrets.Open(a.Organization+"/qbo/"+connectionID, encrypted)
		var credentials quickbooks.Credentials
		if err != nil || json.Unmarshal(raw, &credentials) != nil || credentials.Access == "" {
			problem(w, 503, "Production credential unavailable.")
			return
		}
		client := quickbooks.Sandbox{Realm: g.Realm, Token: credentials.Access, Entity: entity, DebitAccount: g.PrepaidID, CreditAccount: g.ExpenseID, Production: true}
		if in.Action == "verify" {
			if recordErr != nil || record.State == "preview" || record.State == "approved" {
				problem(w, 409, "No saved external journal is available. Never retry an unknown write.")
				return
			}
			if record.ExternalID == "" && record.State == "unknown" && quickbooks.ValidRealm(in.ExternalID) {
				record.ExternalID = in.ExternalID
			}
			if record.ExternalID == "" || (in.ExternalID != "" && in.ExternalID != record.ExternalID) {
				problem(w, 422, "Enter the exact numeric QuickBooks journal ID found for this saved attempt.")
				return
			}
			var expected quickbooks.Journal
			if json.Unmarshal(record.Journal, &expected) != nil {
				problem(w, 503, "Saved posting intent is invalid.")
				return
			}
			verifyProduction(w, r, db, client, paper, record, expected)
			return
		}
		if in.Action != "preview" && in.Action != "approve" && in.Action != "execute" {
			problem(w, 422, "Choose preview, approve, execute or verify.")
			return
		}
		var newer bool
		err = db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM workpapers WHERE close_id::text=$1 AND id>$2 AND kind='prepaid' AND lower(trim(paper->'input'->>'source_reference'))=$3)`, r.PathValue("id"), r.PathValue("paper"), strings.ToLower(strings.TrimSpace(paper.Input.SourceReference))).Scan(&newer)
		if err != nil || newer {
			problem(w, 409, "A newer source draft exists or source freshness could not be checked.")
			return
		}
		journal, observed, err := client.PrepareObserved(r.Context(), paper)
		if err != nil {
			problem(w, 422, err.Error())
			return
		}
		amount, err := prepaids.Amount(string(journal.Line[0].Amount))
		if err != nil {
			problem(w, 422, "Unsupported journal amount.")
			return
		}
		limit, err := prepaids.Amount(g.MaxAmount)
		if err != nil || amount > limit {
			problem(w, 409, "Journal exceeds this client's grant limit.")
			return
		}
		digest := productionDigest(g.ID, paperDigest, journal, observed)
		if in.Action == "preview" {
			j, _ := json.Marshal(journal)
			b, _ := json.Marshal(observed)
			sourceKey := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(paper.Input.SourceReference)))))
			var otherIntent string
			err = db.QueryRowContext(r.Context(), `SELECT id::text FROM production_postings WHERE close_id::text=$1 AND source_key=$2 AND workpaper_id::text<>$3`, r.PathValue("id"), sourceKey, r.PathValue("paper")).Scan(&otherIntent)
			if err == nil {
				problem(w, 409, "Another draft of this source already has a production intent. Review that intent before posting.")
				return
			}
			if err != sql.ErrNoRows {
				problem(w, 503, "Source intent status unavailable.")
				return
			}
			saved, saveErr := db.ExecContext(r.Context(), `INSERT INTO production_postings(close_id,workpaper_id,source_key,grant_id,connection_id,realm,digest,journal,ledger_before,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'preview') ON CONFLICT(workpaper_id) DO UPDATE SET grant_id=EXCLUDED.grant_id,connection_id=EXCLUDED.connection_id,realm=EXCLUDED.realm,digest=EXCLUDED.digest,journal=EXCLUDED.journal,ledger_before=EXCLUDED.ledger_before,state='preview',approved_by=NULL,approval_expires_at=NULL WHERE production_postings.attempted_at IS NULL`, r.PathValue("id"), r.PathValue("paper"), sourceKey, g.ID, g.ConnectionID, g.Realm, digest, string(j), string(b))
			if saveErr != nil {
				problem(w, 503, "Production preview could not be saved.")
				return
			}
			n, _ := saved.RowsAffected()
			if n != 1 {
				problem(w, 409, "An execution attempt already exists. Investigate the saved result; never create a second intent.")
				return
			}
			p, e := readProductionPosting(r.Context(), db, r.PathValue("id"), r.PathValue("paper"))
			if e != nil || p.Digest != digest {
				problem(w, 409, "A different intent exists for this source or the ledger changed. Investigate before posting.")
				return
			}
			jsonReply(w, 200, p)
			return
		}
		if recordErr != nil || record.Digest != in.Digest || record.Digest != digest || record.GrantID != g.ID || record.Realm != g.Realm {
			problem(w, 409, "Intent, grant or live ledger preconditions changed. No write was made.")
			return
		}
		if len(record.Conflicts) > 0 {
			problem(w, 409, "Another approved or unsettled intent uses these accounts. Resolve it and refresh the preview before approval or execution.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Review state unavailable.")
			return
		}
		defer tx.Rollback()
		var closeID, reviewState, currentGrant string
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM client_closes WHERE id::text=$1 AND organization_id=$2 FOR UPDATE`, r.PathValue("id"), a.Organization).Scan(&closeID); err != nil {
			problem(w, 409, "Close changed.")
			return
		}
		var activeMember bool
		if err = tx.QueryRowContext(r.Context(), `SELECT enabled AND EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=$1 AND subject=$2 AND granted) FROM organizations WHERE id=$1 FOR UPDATE`, a.Organization, a.Subject).Scan(&activeMember); err != nil || !activeMember {
			problem(w, 403, "Firm or member access was revoked before this posting decision.")
			return
		}
		err = tx.QueryRowContext(r.Context(), `SELECT review_state FROM workpaper_review_state WHERE id::text=$1 AND close_id=$2`, r.PathValue("paper"), closeID).Scan(&reviewState)
		if err != nil || reviewState != "approved" {
			problem(w, 409, "A current accountant-approved workpaper is required.")
			return
		}
		var reviewer string
		if err = tx.QueryRowContext(r.Context(), `SELECT actor FROM workpaper_reviews WHERE workpaper_id::text=$1 ORDER BY id DESC LIMIT 1`, r.PathValue("paper")).Scan(&reviewer); err != nil {
			problem(w, 409, "A current accountant-approved workpaper is required.")
			return
		}
		issue, err := independenceIssue(r.Context(), tx, closeID, r.PathValue("paper"), reviewer)
		if err != nil {
			problem(w, 503, "Firm review policy could not be checked. No write was made.")
			return
		}
		if issue != "" {
			problem(w, 409, "Posting needs an approval that meets the firm's independent-review policy. "+issue)
			return
		}
		err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM production_posting_grants WHERE id=$1 AND revoked_at IS NULL AND expires_at>now() AND EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=production_posting_grants.organization_id AND subject=production_posting_grants.granted_by AND granted) FOR UPDATE`, g.ID).Scan(&currentGrant)
		if err != nil {
			problem(w, 409, "Production grant expired or was revoked.")
			return
		}
		var lockedConnection string
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM quickbooks_connections WHERE id::text=$1 FOR UPDATE`, record.ConnectionID).Scan(&lockedConnection); err != nil {
			problem(w, 409, "QuickBooks connection changed before this decision.")
			return
		}
		var competing bool
		if err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM production_postings WHERE connection_id::text=$1 AND id::text<>$2 AND (state IN ('executing','unknown') OR (state='approved' AND approval_expires_at>now())) AND (ledger_before->'prepaid'->>'id' IN ($3,$4) OR ledger_before->'expense'->>'id' IN ($3,$4)))`, record.ConnectionID, record.ID, record.LedgerBefore.Prepaid.ID, record.LedgerBefore.Expense.ID).Scan(&competing); err != nil || competing {
			problem(w, 409, "Another approved or unsettled posting now uses these accounts. Refresh before deciding.")
			return
		}
		if in.Action == "approve" {
			if in.Confirmation != "APPROVE PRODUCTION DRAFT" {
				problem(w, 422, "Type APPROVE PRODUCTION DRAFT.")
				return
			}
			result, e := tx.ExecContext(r.Context(), `UPDATE production_postings SET state='approved',approved_by=$2,approval_expires_at=now()+interval '10 minutes' WHERE id=$1 AND state IN ('preview','approved')`, record.ID, a.Subject)
			if e != nil {
				problem(w, 503, "Approval could not be saved.")
				return
			}
			n, _ := result.RowsAffected()
			if n != 1 {
				problem(w, 409, "An attempt already exists.")
				return
			}
		} else {
			if in.Confirmation != "POST TO PRODUCTION" {
				problem(w, 422, "Type POST TO PRODUCTION.")
				return
			}
			result, e := tx.ExecContext(r.Context(), `UPDATE production_postings SET state='executing',attempted_by=$2,attempted_at=now() WHERE id=$1 AND state='approved' AND approval_expires_at>now() AND approved_by IS NOT NULL AND EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=$3 AND subject=production_postings.approved_by AND granted)`, record.ID, a.Subject, a.Organization)
			if e != nil {
				problem(w, 503, "Attempt reservation failed; no provider call was made.")
				return
			}
			n, _ := result.RowsAffected()
			if n != 1 {
				problem(w, 409, "Approval expired or an attempt already exists.")
				return
			}
		}
		if err = tx.Commit(); err != nil {
			problem(w, 503, "Decision save could not be confirmed; no provider call was made.")
			return
		}
		if in.Action == "execute" {
			id, callErr := client.Create(r.Context(), journal, record.RequestID)
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
			defer cancel()
			message := ""
			if callErr != nil {
				message = callErr.Error()
			}
			if _, err = db.ExecContext(ctx, `UPDATE production_postings SET state='unknown',external_id=NULLIF($2,''),last_error=$3 WHERE id=$1 AND state='executing'`, record.ID, id, message); err != nil {
				problem(w, 503, "Attempt outcome could not be recorded. Never retry this write.")
				return
			}
			if callErr != nil {
				problemCode(w, 502, "EXECUTION_UNKNOWN", "Production outcome unknown. Investigate the saved request ID in QuickBooks; do not repost.")
				return
			}
			verifyProduction(w, r, db, client, paper, productionPosting{posting: posting{ID: record.ID, ExternalID: id, State: "unknown"}, LedgerBefore: record.LedgerBefore}, journal)
			return
		}
		p, e := readProductionPosting(r.Context(), db, r.PathValue("id"), r.PathValue("paper"))
		if e != nil {
			problem(w, 503, "Refresh to confirm saved approval.")
			return
		}
		jsonReply(w, 200, p)
	})
}
func verifyProduction(w http.ResponseWriter, r *http.Request, db *sql.DB, client quickbooks.Sandbox, paper prepaids.Workpaper, record productionPosting, journal quickbooks.Journal) {
	readback, err := client.VerifyReadback(r.Context(), record.ExternalID, journal)
	if err == nil {
		persist, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		saved, saveErr := db.ExecContext(persist, `UPDATE production_postings SET external_id=COALESCE(external_id,$2) WHERE id=$1 AND (external_id IS NULL OR external_id=$2)`, record.ID, record.ExternalID)
		cancel()
		if saveErr != nil {
			problem(w, 503, "Matching journal was found but its ID could not be saved. Recheck this saved attempt.")
			return
		}
		n, _ := saved.RowsAffected()
		if n != 1 {
			problem(w, 409, "A different journal ID is already attached to this attempt.")
			return
		}
		if record.VerifiedAt != nil {
			restore, done := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
			saved, saveErr := db.ExecContext(restore, `UPDATE production_postings SET state='verified',last_error='' WHERE id=$1 AND verified_at IS NOT NULL AND external_id=$2`, record.ID, record.ExternalID)
			done()
			if saveErr != nil {
				problem(w, 503, "Journal identity matches, but the saved receipt could not be restored.")
				return
			}
			n, _ := saved.RowsAffected()
			if n != 1 {
				problem(w, 409, "The saved journal receipt changed during readback.")
				return
			}
			p, readErr := readProductionPosting(r.Context(), db, r.PathValue("id"), r.PathValue("paper"))
			if readErr != nil {
				problem(w, 503, "Refresh the saved receipt.")
				return
			}
			jsonReply(w, 200, p)
			return
		}
	}
	var after quickbooks.LedgerObservation
	if err == nil {
		after, err = client.ObserveAfterWrite(r.Context(), paper)
		if err == nil && !ledgerAfterMatches(record.LedgerBefore, after, journal.Line[0].Amount) {
			err = errors.New("Journal exists, but account balances do not match the expected effect; investigate other activity or report lag")
		}
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	if err != nil {
		if _, saveErr := db.ExecContext(ctx, `UPDATE production_postings SET state='unknown',last_error=$2 WHERE id=$1`, record.ID, err.Error()); saveErr != nil {
			problem(w, 503, "Readback failed and status could not be saved; investigate the original attempt.")
			return
		}
		problem(w, 422, err.Error())
		return
	}
	receipt, _ := json.Marshal(map[string]any{"journal": readback, "accounts": after})
	if _, err = db.ExecContext(ctx, `UPDATE production_postings SET state='verified',ledger_after=$2,last_error='',verified_at=now() WHERE id=$1`, record.ID, string(receipt)); err != nil {
		problem(w, 503, "Canonical readback passed but receipt could not be saved; verify again.")
		return
	}
	p, e := readProductionPosting(r.Context(), db, r.PathValue("id"), r.PathValue("paper"))
	if e != nil {
		problem(w, 503, "Refresh to confirm saved receipt.")
		return
	}
	jsonReply(w, 200, p)
}
