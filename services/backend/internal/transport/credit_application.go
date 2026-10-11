package transport

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/quickbooks"
)

// applicationKinds name each application's QuickBooks documents, the grant kind that allows it in production and the
// exception it answers.
var applicationKinds = map[string]struct{ source, target, grant, label string }{
	"payment":       {"Payment", "Invoice", "payment_application", "customer payment"},
	"vendor_credit": {"VendorCredit", "Bill", "credit_application", "vendor credit"},
}

type creditApplication struct {
	ID            string          `json:"id"`
	Kind          string          `json:"kind"`
	CaptureID     string          `json:"capture_id"`
	Realm         string          `json:"realm"`
	SourceID      string          `json:"source_id"`
	TargetID      string          `json:"target_id"`
	Amount        string          `json:"amount"`
	TxnDate       string          `json:"txn_date,omitempty"`
	BankAccountID string          `json:"bank_account_id,omitempty"`
	Reason        string          `json:"reason"`
	RuleReference string          `json:"rule_reference"`
	BeforeSource  json.RawMessage `json:"before_source"`
	BeforeTarget  json.RawMessage `json:"before_target"`
	RequestID     string          `json:"request_id"`
	Digest        string          `json:"digest"`
	State         string          `json:"state"`
	ProposedBy    string          `json:"proposed_by"`
	ApprovedBy    string          `json:"approved_by"`
	Expires       *time.Time      `json:"approval_expires_at"`
	ExternalID    string          `json:"external_id"`
	Error         string          `json:"error"`
	Readback      json.RawMessage `json:"readback"`
	GrantID       string          `json:"grant_id,omitempty"`
}

const applicationColumns = `id::text,kind,capture_id::text,realm,source_id,target_id,amount::text,COALESCE(txn_date::text,''),COALESCE(bank_account_id,''),reason,rule_reference,before_source,before_target,request_id::text,digest,state,proposed_by,COALESCE(approved_by,''),approval_expires_at,COALESCE(external_id,''),last_error,COALESCE(readback,'null'::jsonb),COALESCE(grant_id::text,'')`

func scanApplication(row interface{ Scan(...any) error }) (creditApplication, error) {
	var a creditApplication
	err := row.Scan(&a.ID, &a.Kind, &a.CaptureID, &a.Realm, &a.SourceID, &a.TargetID, &a.Amount, &a.TxnDate, &a.BankAccountID, &a.Reason, &a.RuleReference, &a.BeforeSource, &a.BeforeTarget, &a.RequestID, &a.Digest, &a.State, &a.ProposedBy, &a.ApprovedBy, &a.Expires, &a.ExternalID, &a.Error, &a.Readback, &a.GrantID)
	return a, err
}

// capturedOpen reads the open items and company of the close's latest transaction capture; an unchanged capture reuses
// its predecessor's transactions but reads the open items afresh, and records no realm, so the company comes from its
// connection.
func capturedOpen(ctx context.Context, db *sql.DB, closeID string) (string, string, string, map[string][]json.RawMessage, error) {
	var captureID, realm, currency string
	var raw []byte
	err := db.QueryRowContext(ctx, `SELECT s.id::text,x.realm,COALESCE(NULLIF(x.home_currency,''),'USD'),COALESCE(s.payload->'open_items','{}'::jsonb) FROM quickbooks_entity_syncs s JOIN quickbooks_connections x ON x.id=s.connection_id JOIN evidence e ON e.id=s.evidence_id AND e.close_id=s.close_id WHERE s.close_id::text=$1 AND `+sourceAllows("s.close_id", "quickbooks")+` AND s.state IN ('completed','unchanged') AND s.id=(SELECT max(z.id) FROM quickbooks_entity_syncs z WHERE z.close_id=s.close_id AND z.state IN ('completed','unchanged') AND z.connection_id=COALESCE(`+quickbooks.ReadingConnection("s.close_id")+`,z.connection_id)) AND e.superseded_by IS NULL`, closeID).Scan(&captureID, &realm, &currency, &raw)
	if err != nil {
		return "", "", "", nil, errors.New("A current completed QuickBooks transaction capture is required")
	}
	open := map[string][]json.RawMessage{}
	json.Unmarshal(raw, &open)
	return captureID, realm, currency, open, nil
}

// findOpen returns the captured open document of a type with an ID.
func findOpen(open map[string][]json.RawMessage, entity, id, currency string) (quickbooks.OpenDocument, bool) {
	for _, raw := range open[entity] {
		if doc, err := quickbooks.ParseOpenDocument(entity, raw, currency); err == nil && doc.ID == id {
			return doc, true
		}
	}
	return quickbooks.OpenDocument{}, false
}

// applicationCurrent checks both documents are unchanged since the capture and still open for the amount, for the same
// party, and that the period is open.
func applicationCurrent(ctx context.Context, sandbox quickbooks.Sandbox, kind string, captured [2]quickbooks.OpenDocument, amount *big.Rat, date string) ([2]quickbooks.OpenDocument, error) {
	spec := applicationKinds[kind]
	var live [2]quickbooks.OpenDocument
	for i, entity := range []string{spec.source, spec.target} {
		doc, err := sandbox.ReadOpenDocument(ctx, entity, captured[i].ID)
		if err != nil || doc.SyncToken != captured[i].SyncToken || doc.Open.Cmp(captured[i].Open) != 0 {
			return live, errors.New("The " + entity + " changed since the capture; refresh transactions and review it again")
		}
		if doc.Open.Cmp(amount) < 0 {
			return live, errors.New("The amount exceeds what is open on the " + entity)
		}
		live[i] = doc
	}
	if live[0].Party != live[1].Party {
		return live, errors.New("Both documents must belong to the same " + map[string]string{"payment": "customer", "vendor_credit": "vendor"}[kind])
	}
	if _, err := sandbox.OpenPeriod(ctx, date); err != nil {
		return live, err
	}
	return live, nil
}

func registerCreditApplications(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/{id}/quickbooks/applications", func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.QueryContext(r.Context(), `SELECT `+applicationColumns+` FROM credit_applications WHERE close_id::text=$1 ORDER BY credit_applications.id DESC`, r.PathValue("id"))
		if err != nil {
			problem(w, 503, "Applications unavailable.")
			return
		}
		defer rows.Close()
		items := []creditApplication{}
		for rows.Next() {
			a, err := scanApplication(rows)
			if err != nil {
				problem(w, 503, "Applications unavailable.")
				return
			}
			items = append(items, a)
		}
		// The capture's open documents a person can pair, by party.
		candidates, suggestions := map[string][]map[string]string{}, []quickbooks.Suggestion{}
		captureID, realm, currency, open, openErr := capturedOpen(r.Context(), db, r.PathValue("id"))
		if openErr == nil {
			// A document already in any saved application of this company, in any close, is left to that application.
			used := map[string]bool{}
			saved, err := db.QueryContext(r.Context(), `SELECT kind,source_id,target_id FROM credit_applications WHERE realm=$1`, realm)
			if err != nil {
				problem(w, 503, "Applications unavailable.")
				return
			}
			for saved.Next() {
				var kind, source, target string
				if err = saved.Scan(&kind, &source, &target); err != nil {
					saved.Close()
					problem(w, 503, "Applications unavailable.")
					return
				}
				used[kind+"/source/"+source], used[kind+"/target/"+target] = true, true
			}
			err = saved.Err()
			saved.Close()
			if err != nil {
				problem(w, 503, "Applications unavailable.")
				return
			}
			for _, s := range quickbooks.SuggestApplications(open, currency) {
				if !used[s.Kind+"/source/"+s.SourceID] && !used[s.Kind+"/target/"+s.TargetID] {
					suggestions = append(suggestions, s)
				}
			}
			for _, entity := range []string{"Payment", "Invoice", "VendorCredit", "Bill"} {
				for _, record := range open[entity] {
					var f struct {
						ID                     string `json:"Id"`
						TxnDate                string
						CustomerRef, VendorRef struct{ Value, Name string }
						Balance, UnappliedAmt  json.Number
					}
					if json.Unmarshal(record, &f) != nil || f.ID == "" {
						continue
					}
					party, amount := f.CustomerRef, f.Balance
					if entity == "VendorCredit" || entity == "Bill" {
						party = f.VendorRef
					}
					if entity == "Payment" {
						amount = f.UnappliedAmt
					}
					candidates[entity] = append(candidates[entity], map[string]string{"id": f.ID, "date": f.TxnDate, "open": amount.String(), "party_id": party.Value, "party": party.Name})
				}
			}
		}
		jsonReply(w, 200, map[string]any{"applications": items, "candidates": candidates, "suggestions": suggestions, "capture_id": captureID})
	})
	mux.HandleFunc("POST /v1/closes/{id}/quickbooks/applications", func(w http.ResponseWriter, r *http.Request) {
		a := auth.Actor(r.Context())
		if a == nil || a.Organization == "" {
			problem(w, 403, "A hosted firm session is required.")
			return
		}
		var in struct {
			Action        string `json:"action"`
			ID            string `json:"id"`
			Digest        string `json:"digest"`
			Kind          string `json:"kind"`
			SourceID      string `json:"source_id"`
			TargetID      string `json:"target_id"`
			Amount        string `json:"amount"`
			TxnDate       string `json:"txn_date"`
			BankAccountID string `json:"bank_account_id"`
			Reason        string `json:"reason"`
			RuleReference string `json:"rule_reference"`
			Confirmed     bool   `json:"confirmed"`
			BillPaymentID string `json:"bill_payment_id"`
			CaptureID     string `json:"capture_id"`
		}
		if !decode(w, r, &in) {
			return
		}
		permission := "posting:approve"
		if in.Action == "execute" || in.Action == "verify" {
			permission = "posting:execute"
		}
		if !slices.Contains(a.Permissions, permission) {
			problem(w, 403, "Your role lacks "+permission+".")
			return
		}
		closeID := r.PathValue("id")
		var item creditApplication
		if in.Action != "preview" {
			var err error
			item, err = scanApplication(db.QueryRowContext(r.Context(), `SELECT `+applicationColumns+` FROM credit_applications WHERE id::text=$1 AND close_id::text=$2`, in.ID, closeID))
			if err != nil || item.Digest != in.Digest {
				problem(w, 409, "Refresh the exact saved application.")
				return
			}
			in.Kind = item.Kind
		}
		spec, known := applicationKinds[in.Kind]
		if !known {
			problem(w, 422, "Choose a customer payment or a vendor credit to apply.")
			return
		}
		var entityName, period, environment string
		var signed bool
		if err := db.QueryRowContext(r.Context(), `SELECT entity,period,COALESCE((SELECT decision='signed_off' FROM close_signoffs WHERE close_id=client_closes.id ORDER BY close_signoffs.id DESC LIMIT 1),false),COALESCE((SELECT x.environment FROM quickbooks_connections x WHERE x.id=`+quickbooks.ReadingConnection("client_closes.id")+`),'') FROM client_closes WHERE id::text=$1 AND organization_id=$2`, closeID, a.Organization).Scan(&entityName, &period, &signed, &environment); err != nil || (signed && in.Action != "verify") {
			problem(w, 409, "Use an open close for this client.")
			return
		}
		production := environment == "production"
		var sandbox quickbooks.Sandbox
		var grant productionGrant
		if production {
			if access, err := clientAccess(db, r, entityName); err != nil || !access {
				problem(w, 403, "Client access is required.")
				return
			}
			if !slices.Contains(postingKinds(), spec.grant) {
				problem(w, 409, "Applying a "+spec.label+" in production is not enabled yet. It turns on after the QuickBooks behavior is checked in the sandbox.")
				return
			}
			if in.Action != "verify" && refuseLedgerWrites(w, r.Context(), db, organization(r)) {
				return
			}
			saved, kind := "", spec.grant
			if in.Action == "verify" {
				if item.GrantID == "" {
					problem(w, 409, "Only an attempted production application can be verified.")
					return
				}
				saved, kind = item.GrantID, ""
			}
			var err error
			if sandbox, grant, _, err = productionClient(r.Context(), db, a.Organization, entityName, closeID, kind, saved); err != nil {
				refuse(w, err)
				return
			}
		} else {
			sandbox = sandboxConfiguration()
			if a.Organization != os.Getenv("WORKOS_ORGANIZATION_ID") || !sandbox.Ready() || sandbox.Production || entityName != sandbox.Entity {
				problem(w, 409, "Use an open close for the configured sandbox company.")
				return
			}
		}
		currency := cmp.Or(sandbox.Currency, "USD")
		if in.Action == "preview" {
			in.Reason, in.RuleReference = strings.TrimSpace(in.Reason), strings.TrimSpace(in.RuleReference)
			cents, amountErr := prepaids.Amount(in.Amount)
			if !in.Confirmed || amountErr != nil || cents == 0 || len(in.Reason) < 5 || len(in.Reason) > 2000 || len(in.RuleReference) < 5 || len(in.RuleReference) > 200 {
				problem(w, 422, "Confirm one accountant-reviewed application: a positive amount, its reason and the accountant's rule it follows.")
				return
			}
			amount := big.NewRat(cents, 100)
			captureID, realm, _, open, err := capturedOpen(r.Context(), db, closeID)
			if err == nil && realm != sandbox.Realm {
				err = errors.New("The latest transaction capture is of another QuickBooks company; capture this one")
			}
			if err == nil && in.CaptureID != "" && in.CaptureID != captureID {
				err = errors.New("Transactions were captured again since this suggestion; review the suggestions again")
			}
			if err != nil {
				problem(w, 409, err.Error())
				return
			}
			source, okSource := findOpen(open, spec.source, in.SourceID, currency)
			target, okTarget := findOpen(open, spec.target, in.TargetID, currency)
			if !okSource || !okTarget {
				problem(w, 409, "Choose an open "+spec.source+" and "+spec.target+" from the latest complete capture.")
				return
			}
			// A payment is updated on its own date; a vendor credit is applied by a bill payment dated in the close month,
			// on or after both documents.
			date := source.TxnDate
			if in.Kind == "vendor_credit" {
				day, err := time.Parse("2006-01-02", in.TxnDate)
				if err != nil || in.TxnDate[:7] != period || in.TxnDate < source.TxnDate || in.TxnDate < target.TxnDate {
					problem(w, 422, "Date the application in the close month, on or after both the bill and the credit.")
					return
				}
				date = day.Format("2006-01-02")
				if _, err := sandbox.ValidateBankAccount(r.Context(), in.BankAccountID); err != nil {
					problem(w, 422, err.Error())
					return
				}
			} else {
				in.TxnDate, in.BankAccountID = "", ""
			}
			live, err := applicationCurrent(r.Context(), sandbox, in.Kind, [2]quickbooks.OpenDocument{source, target}, amount, date)
			if err != nil {
				problem(w, 409, err.Error())
				return
			}
			if production {
				limit, err := prepaids.Amount(grant.MaxAmount)
				if err != nil || cents > limit {
					problem(w, 409, "The amount exceeds this client's grant limit.")
					return
				}
			}
			amountText := prepaids.Money(cents)
			digest := preparation.Digest([]byte(in.Kind + "/" + captureID + "/" + string(live[0].Raw) + "/" + string(live[1].Raw) + "/" + amountText + "/" + in.TxnDate + "/" + in.BankAccountID + "/" + in.Reason + "/" + in.RuleReference + "/" + grant.ID))
			result, err := db.ExecContext(r.Context(), `INSERT INTO credit_applications(close_id,kind,capture_id,realm,source_id,target_id,amount,txn_date,bank_account_id,reason,rule_reference,before_source,before_target,digest,state,proposed_by,grant_id)
 VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,'')::date,NULLIF($9,''),$10,$11,$12,$13,$14,'preview',$15,NULLIF($16,'')::bigint)
 ON CONFLICT(realm,kind,source_id,target_id) DO UPDATE SET capture_id=EXCLUDED.capture_id,amount=EXCLUDED.amount,txn_date=EXCLUDED.txn_date,bank_account_id=EXCLUDED.bank_account_id,reason=EXCLUDED.reason,rule_reference=EXCLUDED.rule_reference,before_source=EXCLUDED.before_source,before_target=EXCLUDED.before_target,digest=EXCLUDED.digest,state='preview',proposed_by=EXCLUDED.proposed_by,approved_by=NULL,approval_expires_at=NULL,grant_id=EXCLUDED.grant_id
 WHERE credit_applications.close_id=EXCLUDED.close_id AND credit_applications.attempted_at IS NULL`, closeID, in.Kind, captureID, sandbox.Realm, in.SourceID, in.TargetID, amountText, in.TxnDate, in.BankAccountID, in.Reason, in.RuleReference, string(live[0].Raw), string(live[1].Raw), digest, a.Subject, grant.ID)
			if err != nil {
				problem(w, 503, "Could not save the application preview.")
				return
			}
			if n, _ := result.RowsAffected(); n != 1 {
				problem(w, 409, "These documents already have an attempted application. Verify its outcome.")
				return
			}
			saved, err := scanApplication(db.QueryRowContext(r.Context(), `SELECT `+applicationColumns+` FROM credit_applications WHERE realm=$1 AND kind=$2 AND source_id=$3 AND target_id=$4`, sandbox.Realm, in.Kind, in.SourceID, in.TargetID))
			if err != nil {
				problem(w, 503, "Refresh the application preview.")
				return
			}
			jsonReply(w, 200, saved)
			return
		}
		source, sourceErr := quickbooks.ParseOpenDocument(spec.source, item.BeforeSource, currency)
		target, targetErr := quickbooks.ParseOpenDocument(spec.target, item.BeforeTarget, currency)
		amount, amountOK := new(big.Rat).SetString(item.Amount)
		if sourceErr != nil || targetErr != nil || !amountOK {
			problem(w, 503, "The saved application is unreadable.")
			return
		}
		date := cmp.Or(item.TxnDate, source.TxnDate)
		if in.Action == "approve" || in.Action == "execute" {
			if item.GrantID != grant.ID || item.Realm != sandbox.Realm {
				problem(w, 409, "This client's company or production grant changed; prepare the application again.")
				return
			}
			if _, err := applicationCurrent(r.Context(), sandbox, item.Kind, [2]quickbooks.OpenDocument{source, target}, amount, date); err != nil {
				problem(w, 409, err.Error())
				return
			}
		}
		switch in.Action {
		case "approve":
			if !in.Confirmed || (item.State != "preview" && item.State != "approved") {
				problem(w, 409, "Review and confirm the exact application first.")
				return
			}
			tx, err := db.BeginTx(r.Context(), nil)
			if err != nil {
				problem(w, 503, "Approval unavailable.")
				return
			}
			defer tx.Rollback()
			// An approval of a live write is given only while the firm has writing on, decided under the firm-row lock that
			// turning it off takes, so turning it off withdraws every approval it does not refuse.
			if production && refuseLedgerWrites(w, r.Context(), tx, organization(r)) {
				return
			}
			if issue, err := correctionIndependenceIssue(r.Context(), tx, closeID, item.ProposedBy, a.Subject, item.Amount); err != nil {
				problem(w, 503, "Firm review policy could not be checked. No write was made.")
				return
			} else if issue != "" {
				problem(w, 409, issue)
				return
			}
			result, err := tx.ExecContext(r.Context(), `UPDATE credit_applications SET state='approved',approved_by=$2,approval_expires_at=now()+interval '10 minutes' WHERE id::text=$1 AND digest=$3 AND state IN ('preview','approved')`, item.ID, a.Subject, item.Digest)
			if err != nil {
				problem(w, 503, "Approval unavailable.")
				return
			}
			if n, _ := result.RowsAffected(); n != 1 {
				problem(w, 409, "Application changed.")
				return
			}
			if tx.Commit() != nil {
				problem(w, 503, "Approval unavailable.")
				return
			}
		case "execute":
			if in.Confirmed && approvalLapsed(item.State, item.Expires) {
				problemCode(w, 409, "APPROVAL_EXPIRED", lapsedMessage)
				return
			}
			if !in.Confirmed || item.State != "approved" || item.ApprovedBy == "" || item.Expires == nil || time.Now().After(*item.Expires) {
				problem(w, 409, "A current accountant approval is required.")
				return
			}
			tx, err := db.BeginTx(r.Context(), nil)
			if err != nil {
				problem(w, 503, "Could not reserve one attempt.")
				return
			}
			defer tx.Rollback()
			var locked string
			var signedNow bool
			if tx.QueryRowContext(r.Context(), `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, closeID).Scan(&locked) != nil || tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT decision='signed_off' FROM close_signoffs WHERE close_id=$1 ORDER BY id DESC LIMIT 1),false)`, closeID).Scan(&signedNow) != nil || signedNow {
				problem(w, 409, "The close changed or was signed off. Reopen and review before applying.")
				return
			}
			if issue, err := correctionIndependenceIssue(r.Context(), tx, closeID, item.ProposedBy, item.ApprovedBy, item.Amount); err != nil {
				problem(w, 503, "Firm review policy could not be checked. No write was made.")
				return
			} else if issue != "" {
				problem(w, 409, issue)
				return
			}
			var currentCapture string
			if e := tx.QueryRowContext(r.Context(), `SELECT id::text FROM quickbooks_entity_syncs WHERE close_id::text=$1 AND state IN ('completed','unchanged') AND connection_id=COALESCE(`+quickbooks.ReadingConnection("$1::bigint")+`,connection_id) ORDER BY quickbooks_entity_syncs.id DESC LIMIT 1`, closeID).Scan(&currentCapture); e != nil || currentCapture != item.CaptureID {
				problemCode(w, 409, "SOURCE_STALE", "Transaction capture changed. Review a new application.")
				return
			}
			if production {
				if refuseLedgerWrites(w, r.Context(), tx, organization(r)) {
					return
				}
				var current string
				if tx.QueryRowContext(r.Context(), `SELECT id::text FROM production_posting_grants WHERE id=$1 AND revoked_at IS NULL AND expires_at>now() AND EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=production_posting_grants.organization_id AND subject=production_posting_grants.granted_by AND granted) FOR UPDATE`, item.GrantID).Scan(&current) != nil {
					problem(w, 409, "Production grant expired or was revoked.")
					return
				}
			}
			result, err := tx.ExecContext(r.Context(), `UPDATE credit_applications SET state='executing',attempted_by=$2,attempted_at=now() WHERE id::text=$1 AND digest=$3 AND state='approved' AND approval_expires_at>now() AND approved_by=$5 AND EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=$4 AND subject=credit_applications.approved_by AND granted)`, item.ID, a.Subject, item.Digest, a.Organization, item.ApprovedBy)
			if err != nil {
				problem(w, 503, "Could not reserve one attempt.")
				return
			}
			if n, _ := result.RowsAffected(); n != 1 {
				problem(w, 409, "Approval changed; do not retry the write.")
				return
			}
			if tx.Commit() != nil {
				problem(w, 503, "The attempt reservation was not confirmed; inspect status before retrying.")
				return
			}
			var writeErr error
			external := ""
			if item.Kind == "payment" {
				writeErr = sandbox.ApplyPayment(r.Context(), source, target.ID, item.Amount)
			} else {
				external, writeErr = sandbox.ApplyVendorCredit(r.Context(), source, target, item.Amount, item.BankAccountID, item.TxnDate, item.RequestID)
			}
			persist, stop := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
			defer stop()
			if writeErr != nil {
				_, _ = db.ExecContext(persist, `UPDATE credit_applications SET state='unknown',last_error=$2 WHERE id::text=$1 AND state='executing'`, item.ID, writeErr.Error())
				problemCode(w, 502, "EXECUTION_UNKNOWN", "The application's outcome is uncertain. Inspect QuickBooks before any further action.")
				return
			}
			if external != "" {
				_, _ = db.ExecContext(persist, `UPDATE credit_applications SET external_id=$2 WHERE id::text=$1`, item.ID, external)
			}
			readback, readErr := sandbox.VerifyApplication(r.Context(), item.Kind, source, target, item.Amount, external, item.TxnDate, item.BankAccountID)
			if readErr != nil {
				_, _ = db.ExecContext(persist, `UPDATE credit_applications SET state='unknown',last_error=$2 WHERE id::text=$1 AND state='executing'`, item.ID, readErr.Error())
				problemCode(w, 502, "EXECUTION_UNKNOWN", "The application returned, but canonical readback is unverified.")
				return
			}
			if _, err = db.ExecContext(persist, `UPDATE credit_applications SET state='verified',readback=$2,verified_at=now(),last_error='' WHERE id::text=$1`, item.ID, string(readback)); err != nil {
				problem(w, 503, "Verified receipt could not be saved.")
				return
			}
		case "verify":
			if item.State != "executing" && item.State != "unknown" && item.State != "verified" {
				problem(w, 409, "Only an attempted application can be verified.")
				return
			}
			// A bill payment whose create response was lost is found in QuickBooks by a person and bound only if it verifies.
			external := item.ExternalID
			if external == "" && item.Kind == "vendor_credit" {
				external = strings.TrimSpace(in.BillPaymentID)
			}
			readback, err := sandbox.VerifyApplication(r.Context(), item.Kind, source, target, item.Amount, external, item.TxnDate, item.BankAccountID)
			if err != nil {
				if item.State == "verified" {
					_, _ = db.ExecContext(r.Context(), `UPDATE credit_applications SET state='unknown',last_error=$2 WHERE id::text=$1 AND state='verified'`, item.ID, err.Error())
				}
				problem(w, 409, "Canonical readback differs. Investigate manually; do not retry.")
				return
			}
			if _, err = db.ExecContext(r.Context(), `UPDATE credit_applications SET state='verified',readback=$2,verified_at=now(),last_error='',external_id=NULLIF($3,'') WHERE id::text=$1`, item.ID, string(readback), external); err != nil {
				problem(w, 503, "Readback receipt could not be saved.")
				return
			}
		default:
			problem(w, 422, "Choose preview, approve, execute or verify.")
			return
		}
		saved, err := scanApplication(db.QueryRowContext(r.Context(), `SELECT `+applicationColumns+` FROM credit_applications WHERE id::text=$1`, item.ID))
		if err != nil {
			problem(w, 503, "Refresh the application status.")
			return
		}
		jsonReply(w, 200, saved)
	})
}
