package transport

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

// recodeKinds are the transactions a line correction applies to: their path segment, the grant kind that allows the
// write in production, the treatments a person may choose, and how the kind is named.
var recodeKinds = []struct {
	path, kind, grant, label string
	treatments               []string
}{
	{"bills", "Bill", "bill_correction", "Bill", []string{"uncategorized_expense", "inconsistent_coding"}},
	{"purchases", "Purchase", "purchase_correction", "expense", []string{"uncategorized_expense", "inconsistent_coding"}},
	{"deposits", "Deposit", "deposit_correction", "deposit", []string{"uncategorized_income", "inconsistent_coding"}},
}

type billCorrection struct {
	ID                string          `json:"id"`
	Entity            string          `json:"entity"`
	CaptureID         string          `json:"capture_id"`
	EvidenceID        string          `json:"evidence_id"`
	Realm             string          `json:"realm"`
	BillID            string          `json:"bill_id"`
	Treatment         string          `json:"treatment"`
	Reason            string          `json:"reason"`
	RuleReference     string          `json:"rule_reference"`
	TargetAccountID   string          `json:"target_account_id"`
	TargetLineID      string          `json:"target_line_id"`
	TargetAccountName string          `json:"target_account_name"`
	BeforeBill        json.RawMessage `json:"before_bill"`
	Digest            string          `json:"digest"`
	State             string          `json:"state"`
	ProposedBy        string          `json:"proposed_by"`
	ApprovedBy        string          `json:"approved_by"`
	Expires           *time.Time      `json:"approval_expires_at"`
	AttemptedBy       string          `json:"attempted_by"`
	Error             string          `json:"error"`
	Readback          json.RawMessage `json:"readback"`
	GrantID           string          `json:"grant_id,omitempty"`
}

func readBillCorrection(ctx context.Context, db *sql.DB, closeID, entity, id string) (billCorrection, error) {
	var b billCorrection
	err := db.QueryRowContext(ctx, `SELECT id::text,entity,capture_id::text,evidence_id::text,realm,bill_id,treatment,reason,rule_reference,target_account_id,target_line_id,target_account_name,before_bill,digest,state,proposed_by,COALESCE(approved_by,''),approval_expires_at,COALESCE(attempted_by,''),last_error,COALESCE(readback,'null'::jsonb),COALESCE(grant_id::text,'') FROM bill_corrections WHERE close_id::text=$1 AND entity=$2 AND bill_id=$3`, closeID, entity, id).Scan(&b.ID, &b.Entity, &b.CaptureID, &b.EvidenceID, &b.Realm, &b.BillID, &b.Treatment, &b.Reason, &b.RuleReference, &b.TargetAccountID, &b.TargetLineID, &b.TargetAccountName, &b.BeforeBill, &b.Digest, &b.State, &b.ProposedBy, &b.ApprovedBy, &b.Expires, &b.AttemptedBy, &b.Error, &b.Readback, &b.GrantID)
	return b, err
}

// capturedTxn finds a transaction of a kind in the close's latest complete capture of the realm.
func capturedTxn(ctx context.Context, db *sql.DB, closeID, entity, id, realm string) (string, string, json.RawMessage, error) {
	var captureID, evidenceID string
	var raw []byte
	err := db.QueryRowContext(ctx, `SELECT s.id::text,s.evidence_id::text,s.payload FROM quickbooks_entity_syncs s JOIN evidence e ON e.id=s.evidence_id AND e.close_id=s.close_id WHERE s.close_id::text=$1 AND s.state='completed' AND s.id=(SELECT max(z.id) FROM quickbooks_entity_syncs z WHERE z.close_id=s.close_id AND z.state='completed' AND z.connection_id=COALESCE(`+quickbooks.ReadingConnection("s.close_id")+`,z.connection_id)) AND s.payload->>'realm'=$2 AND e.superseded_by IS NULL AND e.extraction_state='ready'`, closeID, realm).Scan(&captureID, &evidenceID, &raw)
	if err != nil {
		return "", "", nil, errors.New("A current completed QuickBooks transaction capture is required")
	}
	var capture struct {
		Records map[string][]json.RawMessage `json:"records"`
	}
	if json.Unmarshal(raw, &capture) != nil {
		return "", "", nil, errors.New("Transaction capture is invalid")
	}
	for _, row := range capture.Records[entity] {
		var t struct {
			ID string `json:"Id"`
		}
		if json.Unmarshal(row, &t) == nil && t.ID == id {
			return captureID, evidenceID, row, nil
		}
	}
	return "", "", nil, errors.New("Select a supported " + entity + " from the latest complete capture")
}

// recodedAmount is the amount of the line a correction moves; empty when it cannot be read, which fails the policy check.
func recodedAmount(item billCorrection, sandbox quickbooks.Sandbox) string {
	_, _, _, lines, _ := quickbooks.ParseRecodable(item.Entity, item.BeforeBill, cmp.Or(sandbox.Currency, "USD"))
	for _, line := range lines {
		if line.ID == item.TargetLineID {
			return line.Amount
		}
	}
	return ""
}

// recodeCurrent checks a saved correction still matches the capture, the live transaction (by version), the open
// period, the selected line and the new account.
func recodeCurrent(ctx context.Context, db *sql.DB, closeID string, item billCorrection, sandbox quickbooks.Sandbox) error {
	if item.Realm != sandbox.Realm {
		return errors.New("QuickBooks company changed")
	}
	// A correction saved before rules were required is previewed again with one.
	if item.RuleReference == "" {
		return errors.New("Prepare this correction again with the accountant's rule it follows")
	}
	currency := cmp.Or(sandbox.Currency, "USD")
	captureID, evidenceID, captured, err := capturedTxn(ctx, db, closeID, item.Entity, item.BillID, sandbox.Realm)
	if err != nil || captureID != item.CaptureID || evidenceID != item.EvidenceID {
		return errors.New("Transaction capture changed; prepare a new correction")
	}
	_, _, date, lines, beforeErr := quickbooks.ParseRecodable(item.Entity, item.BeforeBill, currency)
	if beforeErr != nil || !quickbooks.SameRecodable(item.Entity, captured, item.BeforeBill, currency) {
		return errors.New("Captured transaction changed")
	}
	live, _, _, err := sandbox.ReadRecodable(ctx, item.Entity, item.BillID)
	if err != nil || !quickbooks.SameRecodable(item.Entity, live, item.BeforeBill, currency) {
		return errors.New("The live transaction differs from the reviewed capture; refresh before correcting")
	}
	if _, err := sandbox.OpenPeriod(ctx, date); err != nil {
		return err
	}
	i := slices.IndexFunc(lines, func(l quickbooks.RecodeLine) bool { return l.ID == item.TargetLineID })
	if i < 0 || lines[i].Account == item.TargetAccountID {
		return errors.New("Selected line changed")
	}
	classification := quickbooks.RecodeKinds[item.Entity].Classification
	if item.Entity == "Deposit" {
		if _, err := sandbox.ValidateTargetAccount(ctx, lines[i].Account, classification); err != nil {
			return errors.New("The deposit line no longer posts to an income account")
		}
	}
	name, err := sandbox.ValidateTargetAccount(ctx, item.TargetAccountID, classification)
	if err != nil || name != item.TargetAccountName {
		return errors.New("Target account changed")
	}
	return nil
}

func registerBillCorrection(mux *http.ServeMux, db *sql.DB) {
	for _, k := range recodeKinds {
		registerRecode(mux, db, k.path, k.kind, k.grant, k.label, k.treatments)
	}
}

func registerRecode(mux *http.ServeMux, db *sql.DB, segment, entity, grantKind, label string, treatments []string) {
	mux.HandleFunc("GET /v1/closes/{id}/quickbooks/"+segment+"/candidates", func(w http.ResponseWriter, r *http.Request) {
		var raw []byte
		err := db.QueryRowContext(r.Context(), `SELECT payload FROM quickbooks_entity_syncs WHERE close_id::text=$1 AND state='completed' AND connection_id=COALESCE(`+quickbooks.ReadingConnection("$1::bigint")+`,connection_id) ORDER BY id DESC LIMIT 1`, r.PathValue("id")).Scan(&raw)
		if err == sql.ErrNoRows {
			jsonReply(w, 200, map[string]any{"items": []any{}, "bills": []any{}, "message": "Refresh QuickBooks transactions to review " + label + " candidates."})
			return
		}
		if err != nil {
			problem(w, 503, "Candidates unavailable.")
			return
		}
		var capture struct {
			Records map[string][]json.RawMessage `json:"records"`
		}
		if json.Unmarshal(raw, &capture) != nil {
			problem(w, 503, "Transaction capture is invalid.")
			return
		}
		items := []map[string]string{}
		for _, row := range capture.Records[entity] {
			var t struct {
				ID                   string `json:"Id"`
				TxnDate              string
				TotalAmt             json.Number
				VendorRef, EntityRef quickbooks.Reference
			}
			if json.Unmarshal(row, &t) != nil || t.ID == "" {
				continue
			}
			items = append(items, map[string]string{"id": t.ID, "date": t.TxnDate, "amount": string(t.TotalAmt), "vendor_id": t.VendorRef.Value + t.EntityRef.Value})
			if len(items) == 100 {
				break
			}
		}
		// "bills" keeps the Bill panel's field.
		jsonReply(w, 200, map[string]any{"items": items, "bills": items, "has_more": len(capture.Records[entity]) > len(items)})
	})
	path := "/v1/closes/{id}/quickbooks/" + segment + "/{bill}/correction"
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		item, err := readBillCorrection(r.Context(), db, r.PathValue("id"), entity, r.PathValue("bill"))
		if err == sql.ErrNoRows {
			realm := sandboxConfiguration().Realm
			db.QueryRowContext(r.Context(), `SELECT realm FROM quickbooks_connections WHERE id=`+quickbooks.ReadingConnection("$1::bigint")+` AND environment='production'`, r.PathValue("id")).Scan(&realm)
			_, _, captured, captureErr := capturedTxn(r.Context(), db, r.PathValue("id"), entity, r.PathValue("bill"), realm)
			if captureErr != nil {
				problem(w, 409, captureErr.Error())
				return
			}
			jsonReply(w, 200, map[string]any{"state": "not_prepared", "entity": entity, "before_bill": captured})
			return
		}
		if err != nil {
			problem(w, 503, "Correction unavailable.")
			return
		}
		jsonReply(w, 200, item)
	})
	mux.HandleFunc("POST "+path, func(w http.ResponseWriter, r *http.Request) {
		a := auth.Actor(r.Context())
		if a == nil || a.Organization == "" {
			problem(w, 403, "A hosted firm session is required.")
			return
		}
		var in struct {
			Action          string `json:"action"`
			Digest          string `json:"digest"`
			Treatment       string `json:"treatment"`
			Reason          string `json:"reason"`
			RuleReference   string `json:"rule_reference"`
			TargetAccountID string `json:"target_account_id"`
			TargetLineID    string `json:"target_line_id"`
			Confirmed       bool   `json:"confirmed"`
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
		closeID, txnID := r.PathValue("id"), r.PathValue("bill")
		var entityName, period, environment string
		var signed bool
		if err := db.QueryRowContext(r.Context(), `SELECT entity,period,COALESCE((SELECT decision='signed_off' FROM close_signoffs WHERE close_id=client_closes.id ORDER BY id DESC LIMIT 1),false),COALESCE((SELECT x.environment FROM quickbooks_connections x WHERE x.id=`+quickbooks.ReadingConnection("client_closes.id")+`),'') FROM client_closes WHERE id::text=$1 AND organization_id=$2`, closeID, a.Organization).Scan(&entityName, &period, &signed, &environment); err != nil || (signed && in.Action != "verify") {
			problem(w, 409, "Use an open close for this client.")
			return
		}
		// A client's production company is corrected under its grant; otherwise the configured sandbox company.
		production := environment == "production"
		var sandbox quickbooks.Sandbox
		var grant productionGrant
		if production {
			if access, err := clientAccess(db, r, entityName); err != nil || !access {
				problem(w, 403, "Client access is required.")
				return
			}
			if !slices.Contains(postingKinds(), grantKind) {
				problem(w, 409, "Production "+label+" corrections are not enabled yet. They turn on after the QuickBooks behavior is checked in the sandbox.")
				return
			}
			saved, kind := "", grantKind
			if in.Action == "verify" {
				prior, err := readBillCorrection(r.Context(), db, closeID, entity, txnID)
				if err != nil || prior.GrantID == "" {
					problem(w, 409, "Only an attempted production correction can be verified.")
					return
				}
				saved, kind = prior.GrantID, ""
			}
			var err error
			if sandbox, grant, _, err = productionClient(r.Context(), db, a.Organization, entityName, kind, saved); err != nil {
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
		classification := quickbooks.RecodeKinds[entity].Classification
		if in.Action == "preview" {
			in.Reason, in.RuleReference = strings.TrimSpace(in.Reason), strings.TrimSpace(in.RuleReference)
			if !in.Confirmed || !slices.Contains(treatments, in.Treatment) || len(in.Reason) < 5 || len(in.Reason) > 2000 || len(in.RuleReference) < 5 || len(in.RuleReference) > 200 {
				problem(w, 422, "Confirm one accountant-reviewed coding exception, its reason and the accountant's rule it follows.")
				return
			}
			captureID, evidenceID, captured, err := capturedTxn(r.Context(), db, closeID, entity, txnID, sandbox.Realm)
			if err != nil {
				problem(w, 409, err.Error())
				return
			}
			live, lines, date, err := sandbox.ReadRecodable(r.Context(), entity, txnID)
			if err != nil || len(date) < 7 || date[:7] != period || !quickbooks.SameRecodable(entity, captured, live, cmp.Or(sandbox.Currency, "USD")) {
				problem(w, 409, "The "+label+" is unsupported or differs from the captured close month. Refresh transactions and review it.")
				return
			}
			if _, err := sandbox.OpenPeriod(r.Context(), date); err != nil {
				problem(w, 409, err.Error())
				return
			}
			i := slices.IndexFunc(lines, func(l quickbooks.RecodeLine) bool { return l.ID == in.TargetLineID })
			if i < 0 || lines[i].Account == in.TargetAccountID {
				problem(w, 422, "Choose one captured line and a different account.")
				return
			}
			if entity == "Deposit" {
				if _, err := sandbox.ValidateTargetAccount(r.Context(), lines[i].Account, classification); err != nil {
					problem(w, 422, "Only a deposit line that posts to an income account can be recoded.")
					return
				}
			}
			name, err := sandbox.ValidateTargetAccount(r.Context(), in.TargetAccountID, classification)
			if err != nil {
				problem(w, 422, err.Error())
				return
			}
			if production {
				limit, limitErr := prepaids.Amount(grant.MaxAmount)
				amount, amountErr := prepaids.Amount(lines[i].Amount)
				if limitErr != nil || amountErr != nil || amount > limit {
					problem(w, 409, "The line exceeds this client's grant limit.")
					return
				}
				if !slices.Contains(grant.AccountIDs, lines[i].Account) {
					problem(w, 409, "The line's current account is not allowed by this client's production grant.")
					return
				}
			}
			digest := preparation.Digest([]byte(entity + "/" + captureID + "/" + evidenceID + "/" + string(live) + "/" + in.Treatment + "/" + in.Reason + "/" + in.RuleReference + "/" + in.TargetLineID + "/" + in.TargetAccountID + "/" + name + "/" + grant.ID))
			result, err := db.ExecContext(r.Context(), `INSERT INTO bill_corrections(close_id,entity,capture_id,evidence_id,realm,bill_id,treatment,reason,rule_reference,target_account_id,target_line_id,target_account_name,before_bill,digest,state,proposed_by,grant_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'preview',$15,NULLIF($16,'')::bigint) ON CONFLICT(realm,entity,bill_id) DO UPDATE SET grant_id=EXCLUDED.grant_id,capture_id=EXCLUDED.capture_id,evidence_id=EXCLUDED.evidence_id,treatment=EXCLUDED.treatment,reason=EXCLUDED.reason,rule_reference=EXCLUDED.rule_reference,target_account_id=EXCLUDED.target_account_id,target_line_id=EXCLUDED.target_line_id,target_account_name=EXCLUDED.target_account_name,before_bill=EXCLUDED.before_bill,digest=EXCLUDED.digest,state='preview',proposed_by=EXCLUDED.proposed_by,approved_by=NULL,approval_expires_at=NULL WHERE bill_corrections.close_id=EXCLUDED.close_id AND bill_corrections.attempted_at IS NULL`, closeID, entity, captureID, evidenceID, sandbox.Realm, txnID, in.Treatment, in.Reason, in.RuleReference, in.TargetAccountID, in.TargetLineID, name, string(live), digest, a.Subject, grant.ID)
			if err != nil {
				problem(w, 503, "Could not save the correction preview.")
				return
			}
			n, _ := result.RowsAffected()
			if n != 1 {
				problem(w, 409, "This "+label+" already has an attempted correction. Verify its outcome.")
				return
			}
			item, err := readBillCorrection(r.Context(), db, closeID, entity, txnID)
			if err != nil {
				problem(w, 503, "Refresh the correction preview.")
				return
			}
			jsonReply(w, 200, item)
			return
		}
		item, err := readBillCorrection(r.Context(), db, closeID, entity, txnID)
		if err != nil || item.Digest == "" || item.Digest != in.Digest {
			problem(w, 409, "Refresh the exact saved correction.")
			return
		}
		if in.Action == "approve" || in.Action == "execute" {
			if item.GrantID != grant.ID {
				problem(w, 409, "This client's production grant changed; prepare the correction again.")
				return
			}
			if err = recodeCurrent(r.Context(), db, closeID, item, sandbox); err != nil {
				problem(w, 409, err.Error())
				return
			}
		}
		switch in.Action {
		case "approve":
			if !in.Confirmed || (item.State != "preview" && item.State != "approved") {
				problem(w, 409, "Review and confirm the exact correction first.")
				return
			}
			tx, err := db.BeginTx(r.Context(), nil)
			if err != nil {
				problem(w, 503, "Approval unavailable.")
				return
			}
			defer tx.Rollback()
			if issue, err := correctionIndependenceIssue(r.Context(), tx, closeID, item.ProposedBy, a.Subject, recodedAmount(item, sandbox)); err != nil {
				problem(w, 503, "Firm review policy could not be checked. No write was made.")
				return
			} else if issue != "" {
				problem(w, 409, issue)
				return
			}
			result, err := tx.ExecContext(r.Context(), `UPDATE bill_corrections SET state='approved',approved_by=$2,approval_expires_at=now()+interval '10 minutes' WHERE id::text=$1 AND digest=$3 AND state IN ('preview','approved')`, item.ID, a.Subject, item.Digest)
			if err != nil {
				problem(w, 503, "Approval unavailable.")
				return
			}
			n, _ := result.RowsAffected()
			if n != 1 {
				problem(w, 409, "Correction changed.")
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
			tx, beginErr := db.BeginTx(r.Context(), nil)
			if beginErr != nil {
				problem(w, 503, "Could not reserve one attempt.")
				return
			}
			defer tx.Rollback()
			var locked string
			if tx.QueryRowContext(r.Context(), `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, closeID).Scan(&locked) != nil {
				problem(w, 409, "Close changed.")
				return
			}
			var signedNow bool
			if tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT decision='signed_off' FROM close_signoffs WHERE close_id=$1 ORDER BY id DESC LIMIT 1),false)`, closeID).Scan(&signedNow) != nil || signedNow {
				problem(w, 409, "Close sign-off changed. Reopen and review before correcting.")
				return
			}
			if issue, err := correctionIndependenceIssue(r.Context(), tx, closeID, item.ProposedBy, item.ApprovedBy, recodedAmount(item, sandbox)); err != nil {
				problem(w, 503, "Firm review policy could not be checked. No write was made.")
				return
			} else if issue != "" {
				problem(w, 409, issue)
				return
			}
			var currentCapture string
			e := tx.QueryRowContext(r.Context(), `SELECT id::text FROM quickbooks_entity_syncs WHERE close_id::text=$1 AND state='completed' AND connection_id=COALESCE(`+quickbooks.ReadingConnection("$1::bigint")+`,connection_id) ORDER BY id DESC LIMIT 1`, closeID).Scan(&currentCapture)
			if e != nil && e != sql.ErrNoRows {
				problem(w, 503, "Could not verify the transaction capture.")
				return
			}
			if e != nil || currentCapture != item.CaptureID {
				problemCode(w, 409, "SOURCE_STALE", "Transaction capture changed. Review a new correction.")
				return
			}
			if production {
				var current string
				if tx.QueryRowContext(r.Context(), `SELECT id::text FROM production_posting_grants WHERE id=$1 AND revoked_at IS NULL AND expires_at>now() AND EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=production_posting_grants.organization_id AND subject=production_posting_grants.granted_by AND granted) FOR UPDATE`, item.GrantID).Scan(&current) != nil {
					problem(w, 409, "Production grant expired or was revoked.")
					return
				}
			}
			result, err := tx.ExecContext(r.Context(), `UPDATE bill_corrections SET state='executing',attempted_by=$2,attempted_at=now() WHERE id::text=$1 AND digest=$3 AND state='approved' AND approval_expires_at>now() AND approved_by=$5 AND EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=$4 AND subject=bill_corrections.approved_by AND granted)`, item.ID, a.Subject, item.Digest, a.Organization, item.ApprovedBy)
			if err != nil {
				problem(w, 503, "Could not reserve one attempt.")
				return
			}
			n, _ := result.RowsAffected()
			if n != 1 {
				problem(w, 409, "Approval changed; do not retry the write.")
				return
			}
			if tx.Commit() != nil {
				problem(w, 503, "The attempt reservation was not confirmed; inspect status before retrying.")
				return
			}
			writeErr := sandbox.Recode(r.Context(), entity, item.BeforeBill, item.TargetLineID, item.TargetAccountID)
			persist, stop := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
			defer stop()
			if writeErr != nil {
				_, _ = db.ExecContext(persist, `UPDATE bill_corrections SET state='unknown',last_error=$2 WHERE id::text=$1 AND state='executing'`, item.ID, writeErr.Error())
				problemCode(w, 502, "EXECUTION_UNKNOWN", "The "+label+" write outcome is uncertain. Inspect QuickBooks before any further action.")
				return
			}
			readback, readErr := sandbox.VerifyRecode(r.Context(), entity, item.BeforeBill, item.TargetLineID, item.TargetAccountID)
			if readErr != nil {
				_, _ = db.ExecContext(persist, `UPDATE bill_corrections SET state='unknown',last_error=$2 WHERE id::text=$1 AND state='executing'`, item.ID, readErr.Error())
				problemCode(w, 502, "EXECUTION_UNKNOWN", "The "+label+" update returned, but canonical readback is unverified.")
				return
			}
			if _, err = db.ExecContext(persist, `UPDATE bill_corrections SET state='verified',readback=$2,verified_at=now(),last_error='' WHERE id::text=$1`, item.ID, string(readback)); err != nil {
				problem(w, 503, "Verified receipt could not be saved.")
				return
			}
		case "verify":
			if item.State != "executing" && item.State != "unknown" && item.State != "verified" {
				problem(w, 409, "Only an attempted correction can be verified.")
				return
			}
			readback, err := sandbox.VerifyRecode(r.Context(), entity, item.BeforeBill, item.TargetLineID, item.TargetAccountID)
			if err != nil {
				if item.State == "verified" {
					_, _ = db.ExecContext(r.Context(), `UPDATE bill_corrections SET state='unknown',last_error=$2 WHERE id::text=$1 AND state='verified'`, item.ID, err.Error())
				}
				problem(w, 409, "Canonical readback differs. Investigate manually; do not retry.")
				return
			}
			if _, err = db.ExecContext(r.Context(), `UPDATE bill_corrections SET state='verified',readback=$2,verified_at=now(),last_error='' WHERE id::text=$1`, item.ID, string(readback)); err != nil {
				problem(w, 503, "Readback receipt could not be saved.")
				return
			}
		default:
			problem(w, 422, "Choose preview, approve, execute or verify.")
			return
		}
		updated, err := readBillCorrection(r.Context(), db, closeID, entity, txnID)
		if err != nil {
			problem(w, 503, "Refresh the correction status.")
			return
		}
		jsonReply(w, 200, updated)
	})
}
