package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/quickbooks"
)

type billCorrection struct {
	ID                string          `json:"id"`
	CaptureID         string          `json:"capture_id"`
	EvidenceID        string          `json:"evidence_id"`
	Realm             string          `json:"realm"`
	BillID            string          `json:"bill_id"`
	Treatment         string          `json:"treatment"`
	Reason            string          `json:"reason"`
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
}

func readBillCorrection(ctx context.Context, db *sql.DB, closeID, billID string) (billCorrection, error) {
	var b billCorrection
	err := db.QueryRowContext(ctx, `SELECT id::text,capture_id::text,evidence_id::text,realm,bill_id,treatment,reason,target_account_id,target_line_id,target_account_name,before_bill,digest,state,proposed_by,COALESCE(approved_by,''),approval_expires_at,COALESCE(attempted_by,''),last_error,COALESCE(readback,'null'::jsonb) FROM bill_corrections WHERE close_id::text=$1 AND bill_id=$2`, closeID, billID).Scan(&b.ID, &b.CaptureID, &b.EvidenceID, &b.Realm, &b.BillID, &b.Treatment, &b.Reason, &b.TargetAccountID, &b.TargetLineID, &b.TargetAccountName, &b.BeforeBill, &b.Digest, &b.State, &b.ProposedBy, &b.ApprovedBy, &b.Expires, &b.AttemptedBy, &b.Error, &b.Readback)
	return b, err
}

func capturedBill(ctx context.Context, db *sql.DB, closeID, billID, realm string) (string, string, quickbooks.Bill, error) {
	var captureID, evidenceID string
	var raw []byte
	err := db.QueryRowContext(ctx, `SELECT s.id::text,s.evidence_id::text,s.payload FROM quickbooks_entity_syncs s JOIN evidence e ON e.id=s.evidence_id AND e.close_id=s.close_id WHERE s.close_id::text=$1 AND s.state='completed' AND s.id=(SELECT max(id) FROM quickbooks_entity_syncs WHERE close_id=s.close_id AND state='completed') AND s.payload->>'realm'=$2 AND e.superseded_by IS NULL AND e.extraction_state='ready'`, closeID, realm).Scan(&captureID, &evidenceID, &raw)
	if err != nil {
		return "", "", quickbooks.Bill{}, errors.New("A current completed QuickBooks transaction capture is required")
	}
	var capture struct {
		Records map[string][]json.RawMessage `json:"records"`
	}
	if json.Unmarshal(raw, &capture) != nil {
		return "", "", quickbooks.Bill{}, errors.New("Transaction capture is invalid")
	}
	var found quickbooks.Bill
	for _, row := range capture.Records["Bill"] {
		var b quickbooks.Bill
		if json.Unmarshal(row, &b) == nil && b.ID == billID {
			found = b
			break
		}
	}
	if found.ID == "" {
		return "", "", quickbooks.Bill{}, errors.New("Select a supported Bill from the latest complete capture")
	}
	return captureID, evidenceID, found, nil
}

func billCorrectionCurrent(ctx context.Context, db *sql.DB, closeID string, item billCorrection, sandbox quickbooks.Sandbox) (quickbooks.Bill, error) {
	if item.Realm != sandbox.Realm {
		return quickbooks.Bill{}, errors.New("Sandbox company changed")
	}
	captureID, evidenceID, captured, err := capturedBill(ctx, db, closeID, item.BillID, sandbox.Realm)
	if err != nil || captureID != item.CaptureID || evidenceID != item.EvidenceID {
		return quickbooks.Bill{}, errors.New("Transaction capture changed; prepare a new correction")
	}
	var before quickbooks.Bill
	if json.Unmarshal(item.BeforeBill, &before) != nil || !reflect.DeepEqual(captured, before) {
		return quickbooks.Bill{}, errors.New("Captured Bill changed")
	}
	live, err := sandbox.ReadBill(ctx, item.BillID)
	if err != nil || !reflect.DeepEqual(live, before) {
		return quickbooks.Bill{}, errors.New("Live Bill differs from the reviewed capture; refresh before correcting")
	}
	if _, err := sandbox.OpenPeriod(ctx, before.TxnDate); err != nil {
		return quickbooks.Bill{}, err
	}
	selected := false
	for _, line := range before.Line {
		selected = selected || line.ID == item.TargetLineID && line.AccountBasedExpenseLineDetail.AccountRef.Value != item.TargetAccountID
	}
	if !selected {
		return quickbooks.Bill{}, errors.New("Selected Bill line changed")
	}
	name, err := sandbox.ValidateExpenseAccount(ctx, item.TargetAccountID)
	if err != nil || name != item.TargetAccountName {
		return quickbooks.Bill{}, errors.New("Target expense account changed")
	}
	return before, nil
}

func registerBillCorrection(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/{id}/quickbooks/bills/candidates", func(w http.ResponseWriter, r *http.Request) {
		var raw []byte
		err := db.QueryRowContext(r.Context(), `SELECT payload FROM quickbooks_entity_syncs WHERE close_id::text=$1 AND state='completed' ORDER BY id DESC LIMIT 1`, r.PathValue("id")).Scan(&raw)
		if err == sql.ErrNoRows {
			jsonReply(w, 200, map[string]any{"bills": []any{}, "message": "Refresh QuickBooks transactions to review Bill candidates."})
			return
		}
		if err != nil {
			problem(w, 503, "Bill candidates unavailable.")
			return
		}
		var capture struct {
			Records map[string][]json.RawMessage `json:"records"`
		}
		if json.Unmarshal(raw, &capture) != nil {
			problem(w, 503, "Bill capture is invalid.")
			return
		}
		items := []map[string]string{}
		for _, row := range capture.Records["Bill"] {
			var bill struct {
				ID        string `json:"Id"`
				TxnDate   string
				TotalAmt  json.Number
				VendorRef quickbooks.Reference
			}
			if json.Unmarshal(row, &bill) != nil || bill.ID == "" {
				continue
			}
			items = append(items, map[string]string{"id": bill.ID, "date": bill.TxnDate, "amount": string(bill.TotalAmt), "vendor_id": bill.VendorRef.Value})
			if len(items) == 100 {
				break
			}
		}
		jsonReply(w, 200, map[string]any{"bills": items, "has_more": len(capture.Records["Bill"]) > len(items)})
	})
	path := "/v1/closes/{id}/quickbooks/bills/{bill}/correction"
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		item, err := readBillCorrection(r.Context(), db, r.PathValue("id"), r.PathValue("bill"))
		if err == sql.ErrNoRows {
			_, _, captured, captureErr := capturedBill(r.Context(), db, r.PathValue("id"), r.PathValue("bill"), sandboxConfiguration().Realm)
			if captureErr != nil {
				problem(w, 409, captureErr.Error())
				return
			}
			jsonReply(w, 200, map[string]any{"state": "not_prepared", "before_bill": captured})
			return
		}
		if err != nil {
			problem(w, 503, "Bill correction unavailable.")
			return
		}
		jsonReply(w, 200, item)
	})
	mux.HandleFunc("POST "+path, func(w http.ResponseWriter, r *http.Request) {
		a := auth.Actor(r.Context())
		if a == nil || a.Organization == "" || a.Organization != os.Getenv("WORKOS_ORGANIZATION_ID") {
			problem(w, 403, "A hosted sandbox-company session is required.")
			return
		}
		var in struct {
			Action          string `json:"action"`
			Digest          string `json:"digest"`
			Treatment       string `json:"treatment"`
			Reason          string `json:"reason"`
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
		sandbox := sandboxConfiguration()
		if !sandbox.Ready() || sandbox.Production {
			problem(w, 503, "Configure the QuickBooks sandbox first.")
			return
		}
		closeID, billID := r.PathValue("id"), r.PathValue("bill")
		var entity, period string
		var signed bool
		if err := db.QueryRowContext(r.Context(), `SELECT entity,period,COALESCE((SELECT decision='signed_off' FROM close_signoffs WHERE close_id=client_closes.id ORDER BY id DESC LIMIT 1),false) FROM client_closes WHERE id::text=$1 AND organization_id=$2`, closeID, a.Organization).Scan(&entity, &period, &signed); err != nil || entity != sandbox.Entity || (signed && in.Action != "verify") {
			problem(w, 409, "Use an open close for the configured sandbox company.")
			return
		}
		if in.Action == "preview" {
			in.Reason = strings.TrimSpace(in.Reason)
			if !in.Confirmed || (in.Treatment != "uncategorized_expense" && in.Treatment != "inconsistent_coding") || len(in.Reason) < 5 || len(in.Reason) > 2000 {
				problem(w, 422, "Confirm one accountant-reviewed coding exception and its reason.")
				return
			}
			captureID, evidenceID, captured, err := capturedBill(r.Context(), db, closeID, billID, sandbox.Realm)
			if err != nil {
				problem(w, 409, err.Error())
				return
			}
			live, err := sandbox.ReadBill(r.Context(), billID)
			if err != nil || len(captured.TxnDate) < 7 || captured.TxnDate[:7] != period || !reflect.DeepEqual(captured, live) {
				problem(w, 409, "Bill is unsupported or differs from the captured close month. Refresh transactions and review it.")
				return
			}
			if _, err := sandbox.OpenPeriod(r.Context(), captured.TxnDate); err != nil {
				problem(w, 409, err.Error())
				return
			}
			selected := false
			for _, line := range live.Line {
				selected = selected || line.ID == in.TargetLineID && line.AccountBasedExpenseLineDetail.AccountRef.Value != in.TargetAccountID
			}
			if !selected {
				problem(w, 422, "Choose one captured Bill line and a different expense account.")
				return
			}
			name, err := sandbox.ValidateExpenseAccount(r.Context(), in.TargetAccountID)
			if err != nil {
				problem(w, 422, "Choose a different active USD expense account.")
				return
			}
			before, _ := json.Marshal(live)
			digest := preparation.Digest([]byte(captureID + "/" + evidenceID + "/" + string(before) + "/" + in.Treatment + "/" + in.Reason + "/" + in.TargetLineID + "/" + in.TargetAccountID + "/" + name))
			result, err := db.ExecContext(r.Context(), `INSERT INTO bill_corrections(close_id,capture_id,evidence_id,realm,bill_id,treatment,reason,target_account_id,target_line_id,target_account_name,before_bill,digest,state,proposed_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'preview',$13) ON CONFLICT(realm,bill_id) DO UPDATE SET capture_id=EXCLUDED.capture_id,evidence_id=EXCLUDED.evidence_id,treatment=EXCLUDED.treatment,reason=EXCLUDED.reason,target_account_id=EXCLUDED.target_account_id,target_line_id=EXCLUDED.target_line_id,target_account_name=EXCLUDED.target_account_name,before_bill=EXCLUDED.before_bill,digest=EXCLUDED.digest,state='preview',proposed_by=EXCLUDED.proposed_by,approved_by=NULL,approval_expires_at=NULL WHERE bill_corrections.close_id=EXCLUDED.close_id AND bill_corrections.attempted_at IS NULL`, closeID, captureID, evidenceID, sandbox.Realm, billID, in.Treatment, in.Reason, in.TargetAccountID, in.TargetLineID, name, string(before), digest, a.Subject)
			if err != nil {
				problem(w, 503, "Could not save Bill preview.")
				return
			}
			n, _ := result.RowsAffected()
			if n != 1 {
				problem(w, 409, "This Bill already has an attempted correction. Verify its outcome.")
				return
			}
			item, err := readBillCorrection(r.Context(), db, closeID, billID)
			if err != nil {
				problem(w, 503, "Refresh the correction preview.")
				return
			}
			jsonReply(w, 200, item)
			return
		}
		item, err := readBillCorrection(r.Context(), db, closeID, billID)
		if err != nil || item.Digest == "" || item.Digest != in.Digest {
			problem(w, 409, "Refresh the exact saved Bill correction.")
			return
		}
		if in.Action == "approve" || in.Action == "execute" {
			if _, err = billCorrectionCurrent(r.Context(), db, closeID, item, sandbox); err != nil {
				problem(w, 409, err.Error())
				return
			}
		}
		switch in.Action {
		case "approve":
			if !in.Confirmed || (item.State != "preview" && item.State != "approved") {
				problem(w, 409, "Review and confirm the exact Bill correction first.")
				return
			}
			result, err := db.ExecContext(r.Context(), `UPDATE bill_corrections SET state='approved',approved_by=$2,approval_expires_at=now()+interval '10 minutes' WHERE id::text=$1 AND digest=$3 AND state IN ('preview','approved')`, item.ID, a.Subject, item.Digest)
			if err != nil {
				problem(w, 503, "Approval unavailable.")
				return
			}
			n, _ := result.RowsAffected()
			if n != 1 {
				problem(w, 409, "Correction changed.")
				return
			}
		case "execute":
			if !in.Confirmed || item.State != "approved" || item.ApprovedBy == "" || item.Expires == nil || time.Now().After(*item.Expires) {
				problem(w, 409, "A current accountant approval is required.")
				return
			}
			var before quickbooks.Bill
			if json.Unmarshal(item.BeforeBill, &before) != nil {
				problem(w, 503, "Saved Bill preview is invalid.")
				return
			}
			tx, beginErr := db.BeginTx(r.Context(), nil)
			if beginErr != nil {
				problem(w, 503, "Could not reserve one Bill attempt.")
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
			var currentCapture string
			if tx.QueryRowContext(r.Context(), `SELECT id::text FROM quickbooks_entity_syncs WHERE close_id::text=$1 AND state='completed' ORDER BY id DESC LIMIT 1`, closeID).Scan(&currentCapture) != nil || currentCapture != item.CaptureID {
				problem(w, 409, "Transaction capture changed. Review a new correction.")
				return
			}
			result, err := tx.ExecContext(r.Context(), `UPDATE bill_corrections SET state='executing',attempted_by=$2,attempted_at=now() WHERE id::text=$1 AND digest=$3 AND state='approved' AND approval_expires_at>now() AND approved_by IS NOT NULL AND EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=$4 AND subject=bill_corrections.approved_by AND granted)`, item.ID, a.Subject, item.Digest, a.Organization)
			if err != nil {
				problem(w, 503, "Could not reserve one Bill attempt.")
				return
			}
			n, _ := result.RowsAffected()
			if n != 1 {
				problem(w, 409, "Approval changed; do not retry the Bill write.")
				return
			}
			if tx.Commit() != nil {
				problem(w, 503, "Bill attempt reservation was not confirmed; inspect status before retrying.")
				return
			}
			_, writeErr := sandbox.CorrectBillAccount(r.Context(), before, item.TargetLineID, item.TargetAccountID)
			persist, stop := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
			defer stop()
			if writeErr != nil {
				_, _ = db.ExecContext(persist, `UPDATE bill_corrections SET state='unknown',last_error=$2 WHERE id::text=$1 AND state='executing'`, item.ID, writeErr.Error())
				problem(w, 502, "Bill write outcome is uncertain. Inspect QuickBooks before any further action.")
				return
			}
			readback, readErr := sandbox.VerifyBillCorrection(r.Context(), before, item.TargetLineID, item.TargetAccountID)
			if readErr != nil {
				_, _ = db.ExecContext(persist, `UPDATE bill_corrections SET state='unknown',last_error=$2 WHERE id::text=$1 AND state='executing'`, item.ID, readErr.Error())
				problem(w, 502, "Bill update returned, but canonical readback is unverified.")
				return
			}
			body, _ := json.Marshal(readback)
			if _, err = db.ExecContext(persist, `UPDATE bill_corrections SET state='verified',readback=$2,verified_at=now(),last_error='' WHERE id::text=$1`, item.ID, string(body)); err != nil {
				problem(w, 503, "Verified Bill receipt could not be saved.")
				return
			}
		case "verify":
			if item.State != "executing" && item.State != "unknown" && item.State != "verified" {
				problem(w, 409, "Only an attempted Bill can be verified.")
				return
			}
			var before quickbooks.Bill
			if json.Unmarshal(item.BeforeBill, &before) != nil {
				problem(w, 503, "Saved Bill preview is invalid.")
				return
			}
			readback, err := sandbox.VerifyBillCorrection(r.Context(), before, item.TargetLineID, item.TargetAccountID)
			if err != nil {
				if item.State == "verified" {
					_, _ = db.ExecContext(r.Context(), `UPDATE bill_corrections SET state='unknown',last_error=$2 WHERE id::text=$1 AND state='verified'`, item.ID, err.Error())
				}
				problem(w, 409, "Canonical Bill readback differs. Investigate manually; do not retry.")
				return
			}
			body, _ := json.Marshal(readback)
			if _, err = db.ExecContext(r.Context(), `UPDATE bill_corrections SET state='verified',readback=$2,verified_at=now(),last_error='' WHERE id::text=$1`, item.ID, string(body)); err != nil {
				problem(w, 503, "Readback receipt could not be saved.")
				return
			}
		default:
			problem(w, 422, "Choose preview, approve, execute or verify.")
			return
		}
		updated, err := readBillCorrection(r.Context(), db, closeID, billID)
		if err != nil {
			problem(w, 503, "Refresh Bill correction status.")
			return
		}
		jsonReply(w, 200, updated)
	})
}
