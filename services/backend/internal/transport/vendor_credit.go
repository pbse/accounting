package transport

import (
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/quickbooks"
	"github.com/jackc/pgx/v5/pgconn"
)

type vendorCreditIntent struct {
	ID                  string          `json:"id"`
	RequestID           string          `json:"request_id"`
	Digest              string          `json:"digest"`
	Realm               string          `json:"realm"`
	State               string          `json:"state"`
	Credit              json.RawMessage `json:"credit"`
	ReviewID            string          `json:"review_id"`
	EvidenceID          string          `json:"evidence_id"`
	EvidenceDigest      string          `json:"evidence_digest"`
	Reference           string          `json:"credit_reference"`
	VendorName          string          `json:"vendor_name"`
	UnbookedConfirmedBy string          `json:"unbooked_confirmed_by"`
	ApprovedBy          string          `json:"approved_by"`
	Expires             *time.Time      `json:"approval_expires_at"`
	ExternalID          string          `json:"external_id"`
	Error               string          `json:"error"`
	Readback            json.RawMessage `json:"readback,omitempty"`
	GrantID             string          `json:"grant_id,omitempty"`
}

type vendorCreditQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readVendorCredit(ctx context.Context, db *sql.DB, closeID, paperID string) (vendorCreditIntent, error) {
	var item vendorCreditIntent
	err := db.QueryRowContext(ctx, `SELECT id::text,request_id::text,digest,realm,state,credit,review_id,evidence_id::text,evidence_digest,credit_reference,vendor_name,unbooked_confirmed_by,COALESCE(approved_by,''),approval_expires_at,COALESCE(external_id,''),last_error,COALESCE(readback,'null'::jsonb),COALESCE(grant_id::text,'') FROM vendor_credit_postings WHERE close_id::text=$1 AND workpaper_id::text=$2`, closeID, paperID).Scan(&item.ID, &item.RequestID, &item.Digest, &item.Realm, &item.State, &item.Credit, &item.ReviewID, &item.EvidenceID, &item.EvidenceDigest, &item.Reference, &item.VendorName, &item.UnbookedConfirmedBy, &item.ApprovedBy, &item.Expires, &item.ExternalID, &item.Error, &item.Readback, &item.GrantID)
	return item, err
}

func reviewedSupplierCredit(ctx context.Context, db vendorCreditQuerier, closeID, paperID, reference, evidenceID, currency string) (prepaids.Workpaper, string, string, string, error) {
	var paper prepaids.Workpaper
	var raw []byte
	var state, reviewID, digest, entity, period string
	err := db.QueryRowContext(ctx, `SELECT w.paper,w.review_state,w.review_id,w.digest,c.entity,c.period FROM workpaper_review_state w JOIN client_closes c ON c.id=w.close_id WHERE w.id::text=$1 AND w.close_id::text=$2 AND w.kind='prepaid'`, paperID, closeID).Scan(&raw, &state, &reviewID, &digest, &entity, &period)
	if err != nil || state != "approved" || json.Unmarshal(raw, &paper) != nil || paper.Input.Entity != entity || paper.Input.ClosePeriod != period || paper.Input.Currency != currency || paper.Input.Schedule != "" || paper.Input.CancellationMethod != "" || paper.Input.PrepaidAccount == "" {
		return paper, "", "", "", fmt.Errorf("A current approved prepaid credit workpaper in the client's currency is required")
	}
	var newer bool
	if err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM workpapers WHERE close_id::text=$1 AND paper->>'revises'=$2)`, closeID, paperID).Scan(&newer); err != nil || newer {
		return paper, "", "", "", fmt.Errorf("A newer version of this workpaper exists")
	}
	var linked struct {
		Sources []struct {
			ID     string `json:"id"`
			Digest string `json:"digest"`
		} `json:"sources"`
	}
	if json.Unmarshal(raw, &linked) != nil {
		return paper, "", "", "", fmt.Errorf("Reviewed source links are invalid")
	}
	amount := ""
	if paper.Input.CreditReference == reference && paper.Input.CreditPeriod == period {
		amount = paper.Input.CreditAmount
	}
	for _, credit := range paper.Input.Credits {
		if credit.Reference == reference && credit.RecordedPeriod == period {
			if amount != "" {
				return paper, "", "", "", fmt.Errorf("Credit reference is ambiguous")
			}
			amount = credit.Amount
		}
	}
	if _, err = prepaids.Amount(amount); err != nil || amount == "0.00" || len(reference) < 3 || len(reference) > 200 {
		return paper, "", "", "", fmt.Errorf("Choose one reviewed supplier credit recorded in this close")
	}
	var sourceDigest, content string
	err = db.QueryRowContext(ctx, `SELECT digest,content FROM evidence WHERE id::text=$1 AND close_id::text=$2 AND superseded_by IS NULL AND extraction_state='ready'`, evidenceID, closeID).Scan(&sourceDigest, &content)
	if err != nil || !strings.Contains(strings.ToLower(content), strings.ToLower(reference)) {
		return paper, "", "", "", fmt.Errorf("Select current supplier credit evidence containing the exact reviewed reference")
	}
	linkedSource := false
	for _, source := range linked.Sources {
		if source.ID == evidenceID && source.Digest == sourceDigest {
			linkedSource = true
		}
	}
	if !linkedSource {
		return paper, "", "", "", fmt.Errorf("Supplier credit evidence must be linked to this approved workpaper")
	}
	return paper, reviewID, sourceDigest, amount, nil
}

func vendorCreditDigest(realm, paperID, reviewID, sourceDigest string, credit quickbooks.VendorCredit) string {
	raw, _ := json.Marshal([]any{realm, paperID, reviewID, sourceDigest, credit.VendorName, credit})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func registerVendorCredit(mux *http.ServeMux, db *sql.DB) {
	path := "/v1/closes/{id}/workpapers/{paper}/vendor-credit"
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		item, err := readVendorCredit(r.Context(), db, r.PathValue("id"), r.PathValue("paper"))
		if err == sql.ErrNoRows {
			var environment string
			db.QueryRowContext(r.Context(), `SELECT environment FROM quickbooks_connections WHERE id=`+quickbooks.ReadingConnection("$1::bigint"), r.PathValue("id")).Scan(&environment)
			production := environment == "production" && slices.Contains(postingKinds(), "vendor_credit")
			jsonReply(w, 200, map[string]any{"state": "not_prepared", "production": production, "configured": production || sandboxConfiguration().Ready() && organization(r) == os.Getenv("WORKOS_ORGANIZATION_ID")})
			return
		}
		if err != nil {
			problem(w, 503, "Vendor credit status unavailable.")
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
			Action            string `json:"action"`
			Digest            string `json:"digest"`
			Confirmation      string `json:"confirmation"`
			VendorID          string `json:"vendor_id"`
			APAccountID       string `json:"ap_account_id"`
			EvidenceID        string `json:"evidence_id"`
			CreditReference   string `json:"credit_reference"`
			TxnDate           string `json:"txn_date"`
			MethodConfirmed   bool   `json:"method_confirmed"`
			UnbookedConfirmed bool   `json:"unbooked_confirmed"`
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
		closeID, paperID := r.PathValue("id"), r.PathValue("paper")
		var entity, environment string
		if err := db.QueryRowContext(r.Context(), `SELECT entity,COALESCE((SELECT x.environment FROM quickbooks_connections x WHERE x.id=`+quickbooks.ReadingConnection("client_closes.id")+`),'') FROM client_closes WHERE id::text=$1 AND organization_id=$2`, closeID, organization(r)).Scan(&entity, &environment); err != nil {
			problem(w, 404, "Client close not found.")
			return
		}
		// A client's production company receives the credit under its grant; otherwise the configured sandbox company.
		production := environment == "production"
		var sandbox quickbooks.Sandbox
		var grant productionGrant
		if production {
			if access, err := clientAccess(db, r, entity); err != nil || !access {
				problem(w, 403, "Client access is required.")
				return
			}
			if !slices.Contains(postingKinds(), "vendor_credit") {
				problem(w, 409, "Production supplier credits are not enabled yet. They turn on after the QuickBooks behavior is checked in the sandbox.")
				return
			}
			if in.Action != "verify" && refuseLedgerWrites(w, r.Context(), db, organization(r)) {
				return
			}
			saved, kind := "", "vendor_credit"
			if in.Action == "verify" {
				prior, err := readVendorCredit(r.Context(), db, closeID, paperID)
				if err != nil || prior.GrantID == "" {
					problem(w, 409, "Only an attempted production supplier credit can be verified.")
					return
				}
				saved, kind = prior.GrantID, ""
			}
			var err error
			if sandbox, grant, _, err = productionClient(r.Context(), db, a.Organization, entity, kind, saved); err != nil {
				refuse(w, err)
				return
			}
		} else {
			sandbox = sandboxConfiguration()
			if a.Organization != os.Getenv("WORKOS_ORGANIZATION_ID") || !sandbox.Ready() || entity != sandbox.Entity {
				problem(w, 409, "The client close does not match the configured QuickBooks sandbox company.")
				return
			}
		}
		if in.Action == "preview" {
			if !in.MethodConfirmed || !in.UnbookedConfirmed {
				problem(w, 422, "The client accountant must confirm the treatment, example and that the credit is not already booked.")
				return
			}
			var period, decision string
			if err := db.QueryRowContext(r.Context(), `SELECT period FROM client_closes WHERE id::text=$1`, closeID).Scan(&period); err != nil {
				problem(w, 404, "Close unavailable.")
				return
			}
			if err := db.QueryRowContext(r.Context(), `SELECT decision FROM close_signoffs WHERE close_id::text=$1 ORDER BY id DESC LIMIT 1`, closeID).Scan(&decision); err != nil && err != sql.ErrNoRows {
				problem(w, 503, "Sign-off unavailable.")
				return
			}
			date, err := time.Parse("2006-01-02", in.TxnDate)
			if decision == "signed_off" || err != nil || date.Format("2006-01-02") != in.TxnDate || date.Format("2006-01") != period {
				problem(w, 422, "Choose an exact supplier-credit date in an open close month.")
				return
			}
			paper, review, sourceDigest, amount, err := reviewedSupplierCredit(r.Context(), db, closeID, paperID, in.CreditReference, in.EvidenceID, cmp.Or(sandbox.Currency, "USD"))
			if err != nil {
				problem(w, 409, err.Error())
				return
			}
			var prior bool
			if err = db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM vendor_credit_postings WHERE close_id::text=$1 AND lower(credit_reference)=lower($2) AND workpaper_id::text<>$3 AND state<>'preview')`, closeID, in.CreditReference, paperID).Scan(&prior); err != nil || prior {
				problem(w, 409, "This supplier credit already has a posting attempt in this close.")
				return
			}
			if err = db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM sandbox_postings WHERE workpaper_id::text=$1 AND state IN ('executing','unknown','verified')) OR EXISTS(SELECT 1 FROM production_postings WHERE workpaper_id::text=$1 AND state IN ('executing','unknown','verified'))`, paperID).Scan(&prior); err != nil || prior {
				problem(w, 409, "This workpaper already has a journal attempt; reconcile it before considering a native supplier credit.")
				return
			}
			prepaidID := sandbox.DebitAccount
			if production {
				limit, e := prepaids.Amount(grant.MaxAmount)
				if cents, e2 := prepaids.Amount(amount); e != nil || e2 != nil || cents > limit {
					problem(w, 409, "The supplier credit exceeds this client's grant limit.")
					return
				}
				if prepaidID, err = sandbox.AccountNamed(r.Context(), paper.Input.PrepaidAccount); err != nil {
					problem(w, 422, err.Error())
					return
				}
			}
			credit, err := sandbox.PrepareVendorCredit(r.Context(), in.VendorID, in.APAccountID, prepaidID, paper.Input.PrepaidAccount, in.TxnDate, amount, in.CreditReference)
			if err != nil {
				problem(w, 422, err.Error())
				return
			}
			raw, _ := json.Marshal(credit)
			digest := vendorCreditDigest(sandbox.Realm, paperID, review, sourceDigest, credit)
			_, err = db.ExecContext(r.Context(), `INSERT INTO vendor_credit_postings(close_id,workpaper_id,evidence_id,evidence_digest,review_id,credit_reference,vendor_name,unbooked_confirmed_by,realm,digest,credit,state,grant_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'preview',NULLIF($12,'')::bigint) ON CONFLICT(workpaper_id) DO UPDATE SET grant_id=EXCLUDED.grant_id,evidence_id=EXCLUDED.evidence_id,evidence_digest=EXCLUDED.evidence_digest,review_id=EXCLUDED.review_id,credit_reference=EXCLUDED.credit_reference,vendor_name=EXCLUDED.vendor_name,unbooked_confirmed_by=EXCLUDED.unbooked_confirmed_by,unbooked_confirmed_at=now(),realm=EXCLUDED.realm,digest=EXCLUDED.digest,credit=EXCLUDED.credit,state='preview',request_id=gen_random_uuid(),approved_by=NULL,approval_expires_at=NULL WHERE vendor_credit_postings.state IN ('preview','approved')`, closeID, paperID, in.EvidenceID, sourceDigest, review, in.CreditReference, credit.VendorName, a.Subject, sandbox.Realm, digest, string(raw), grant.ID)
			if err != nil {
				problem(w, 503, "Could not save VendorCredit preview.")
				return
			}
			item, err := readVendorCredit(r.Context(), db, closeID, paperID)
			if err != nil || item.Digest != digest {
				problem(w, 409, "A different supplier credit already has an intent for this workpaper.")
				return
			}
			jsonReply(w, 200, item)
			return
		}
		item, err := readVendorCredit(r.Context(), db, closeID, paperID)
		if err != nil || item.Digest != in.Digest || item.Realm != sandbox.Realm {
			problem(w, 409, "Refresh the exact saved vendor credit intent.")
			return
		}
		var credit quickbooks.VendorCredit
		if json.Unmarshal(item.Credit, &credit) != nil || len(credit.Line) != 1 {
			problem(w, 503, "Saved VendorCredit is invalid.")
			return
		}
		credit.VendorName = item.VendorName
		if (in.Action == "approve" || in.Action == "execute") && item.GrantID != grant.ID {
			problem(w, 409, "This client's production grant changed; prepare the supplier credit again.")
			return
		}
		if in.Action == "approve" || in.Action == "execute" {
			paper, review, sourceDigest, amount, e := reviewedSupplierCredit(r.Context(), db, closeID, paperID, item.Reference, item.EvidenceID, cmp.Or(sandbox.Currency, "USD"))
			if e != nil || review != item.ReviewID || sourceDigest != item.EvidenceDigest || paper.Input.PrepaidAccount == "" {
				problem(w, 409, "Review or supplier credit evidence changed. Start a new reviewed intent.")
				return
			}
			fresh, e := sandbox.PrepareVendorCredit(r.Context(), credit.VendorRef.Value, credit.APAccountRef.Value, credit.Line[0].AccountBasedExpenseLineDetail.AccountRef.Value, paper.Input.PrepaidAccount, credit.TxnDate, amount, item.Reference)
			if e != nil {
				problem(w, 409, e.Error())
				return
			}
			if vendorCreditDigest(sandbox.Realm, paperID, review, sourceDigest, fresh) != item.Digest {
				problem(w, 409, "Provider identity or account mapping changed.")
				return
			}
		}
		switch in.Action {
		case "approve":
			if in.Confirmation != "APPROVE VENDOR CREDIT" {
				problem(w, 422, "Type APPROVE VENDOR CREDIT.")
				return
			}
			result, e := db.ExecContext(r.Context(), `UPDATE vendor_credit_postings SET state='approved',approved_by=$2,approval_expires_at=now()+interval '10 minutes' WHERE id::text=$1 AND state IN ('preview','approved')`, item.ID, a.Subject)
			if e != nil {
				var constraint *pgconn.PgError
				if errors.As(e, &constraint) && constraint.Code == "23505" && constraint.ConstraintName == "vendor_credit_one_reference" {
					problem(w, 409, "This supplier credit reference already has an approved or attempted posting in this close.")
					return
				}
				problem(w, 503, "Approval could not be saved.")
				return
			}
			n, _ := result.RowsAffected()
			if n != 1 {
				problem(w, 409, "Posting attempt already exists.")
				return
			}
		case "execute":
			if in.Confirmation != "POST VENDOR CREDIT" {
				problem(w, 422, "Type POST VENDOR CREDIT.")
				return
			}
			tx, e := db.BeginTx(r.Context(), nil)
			if e != nil {
				problem(w, 503, "Attempt could not be reserved; no provider write was made.")
				return
			}
			defer tx.Rollback()
			var locked string
			e = tx.QueryRowContext(r.Context(), `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, closeID).Scan(&locked)
			if e == nil {
				_, currentReview, currentSource, _, checkErr := reviewedSupplierCredit(r.Context(), tx, closeID, paperID, item.Reference, item.EvidenceID, cmp.Or(sandbox.Currency, "USD"))
				if checkErr != nil || currentReview != item.ReviewID || currentSource != item.EvidenceDigest {
					problem(w, 409, "The workpaper or credit source changed before posting.")
					return
				}
			}
			if e != nil {
				problem(w, 503, "Could not lock the close; no provider write was made.")
				return
			}
			var signed bool
			if tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT decision='signed_off' FROM close_signoffs WHERE close_id=$1::bigint ORDER BY id DESC LIMIT 1),false)`, locked).Scan(&signed) != nil || signed {
				problem(w, 409, "Close sign-off changed. Reopen and review before posting.")
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
			var alreadyReserved bool
			if e = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM vendor_credit_postings WHERE close_id::text=$1 AND lower(credit_reference)=lower($2) AND id::text<>$3 AND state<>'preview') OR EXISTS(SELECT 1 FROM sandbox_postings WHERE workpaper_id::text=$4 AND state IN ('executing','unknown','verified')) OR EXISTS(SELECT 1 FROM production_postings WHERE workpaper_id::text=$4 AND state IN ('executing','unknown','verified'))`, closeID, item.Reference, item.ID, paperID).Scan(&alreadyReserved); e != nil || alreadyReserved {
				problem(w, 409, "This supplier credit already has a posting attempt in this close.")
				return
			}
			result, e := tx.ExecContext(r.Context(), `UPDATE vendor_credit_postings SET state='executing',attempted_by=$2,attempted_at=now() WHERE id::text=$1 AND state='approved' AND approved_by=$2 AND approval_expires_at>now()`, item.ID, a.Subject)
			if e != nil {
				problem(w, 503, "Attempt could not be reserved; no provider write was made.")
				return
			}
			n, _ := result.RowsAffected()
			if n != 1 {
				if approvalLapsed(item.State, item.Expires) {
					problemCode(w, 409, "APPROVAL_EXPIRED", lapsedMessage)
					return
				}
				problem(w, 409, "Approval expired or attempt already exists.")
				return
			}
			if e = tx.Commit(); e != nil {
				problem(w, 503, "Attempt reservation could not be committed; no provider write was made.")
				return
			}
			id, callErr := sandbox.CreateVendorCredit(r.Context(), credit, item.RequestID)
			persist, stop := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
			defer stop()
			message := ""
			if callErr != nil {
				message = callErr.Error()
			}
			if _, e = db.ExecContext(persist, `UPDATE vendor_credit_postings SET state='unknown',external_id=NULLIF($2,''),last_error=$3 WHERE id::text=$1`, item.ID, id, message); e != nil {
				problem(w, 503, "Outcome could not be recorded. Investigate; do not retry.")
				return
			}
			if callErr != nil {
				problemCode(w, 502, "EXECUTION_UNKNOWN", "Posting outcome is unknown. Investigate; do not retry.")
				return
			}
			readback, e := sandbox.VerifyVendorCredit(r.Context(), id, credit)
			receipt, finish := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
			defer finish()
			if e != nil {
				db.ExecContext(receipt, `UPDATE vendor_credit_postings SET last_error=$2 WHERE id::text=$1`, item.ID, e.Error())
				problemCode(w, 502, "EXECUTION_UNKNOWN", "VendorCredit ID was returned but readback is unverified.")
				return
			}
			body, _ := json.Marshal(readback)
			if _, e = db.ExecContext(receipt, `UPDATE vendor_credit_postings SET state='verified',readback=$2,verified_at=now(),last_error='' WHERE id::text=$1`, item.ID, string(body)); e != nil {
				problem(w, 503, "Readback passed but receipt could not be saved.")
				return
			}
		case "verify":
			if item.ExternalID == "" {
				problem(w, 409, "No external ID was recorded; investigate the attempted write.")
				return
			}
			readback, e := sandbox.VerifyVendorCredit(r.Context(), item.ExternalID, credit)
			if e != nil {
				if item.State == "verified" {
					_, _ = db.ExecContext(r.Context(), `UPDATE vendor_credit_postings SET state='unknown',last_error=$2 WHERE id::text=$1 AND state='verified'`, item.ID, e.Error())
				}
				problem(w, 422, e.Error())
				return
			}
			body, _ := json.Marshal(readback)
			if _, e = db.ExecContext(r.Context(), `UPDATE vendor_credit_postings SET state='verified',readback=$2,verified_at=now(),last_error='' WHERE id::text=$1`, item.ID, string(body)); e != nil {
				problem(w, 503, "Readback passed but receipt could not be saved.")
				return
			}
		default:
			problem(w, 422, "Choose preview, approve, execute or verify.")
			return
		}
		updated, e := readVendorCredit(r.Context(), db, closeID, paperID)
		if e != nil {
			problem(w, 503, "Refresh the posting status.")
			return
		}
		jsonReply(w, 200, updated)
	})
}
