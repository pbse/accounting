package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/xero"
)

// Xero bill corrections follow the QuickBooks line recode: a preview from live reads equal to the close's latest
// capture, the accountant's cited rule, a person's ten-minute approval under the firm's independence rule, one attempt
// and a readback. They need the client's Xero grant to cover bill_correction and XERO_POSTING_KINDS to enable it.

type xeroCorrection struct {
	ID                string          `json:"id"`
	SnapshotID        string          `json:"snapshot_id"`
	GrantID           string          `json:"grant_id"`
	TenantID          string          `json:"tenant_id"`
	InvoiceID         string          `json:"invoice_id"`
	TargetLineID      string          `json:"target_line_id"`
	TargetAccountID   string          `json:"target_account_id"`
	TargetAccountCode string          `json:"target_account_code"`
	TargetAccountName string          `json:"target_account_name"`
	Reason            string          `json:"reason"`
	RuleReference     string          `json:"rule_reference"`
	BeforeBill        json.RawMessage `json:"before_bill"`
	Digest            string          `json:"digest"`
	RequestID         string          `json:"request_id"`
	State             string          `json:"state"`
	ProposedBy        string          `json:"proposed_by"`
	ApprovedBy        string          `json:"approved_by"`
	Expires           *time.Time      `json:"approval_expires_at"`
	Readback          json.RawMessage `json:"readback"`
	Error             string          `json:"error"`
}

func readXeroCorrection(ctx context.Context, q querier, closeID, invoiceID string) (xeroCorrection, error) {
	var c xeroCorrection
	err := q.QueryRowContext(ctx, `SELECT id::text,snapshot_id::text,grant_id::text,tenant_id,invoice_id,target_line_id,target_account_id,target_account_code,target_account_name,reason,rule_reference,before_bill,digest,request_id::text,state,proposed_by,COALESCE(approved_by,''),approval_expires_at,COALESCE(readback,'null'::jsonb),last_error FROM xero_corrections WHERE close_id::text=$1 AND invoice_id=$2`, closeID, invoiceID).Scan(&c.ID, &c.SnapshotID, &c.GrantID, &c.TenantID, &c.InvoiceID, &c.TargetLineID, &c.TargetAccountID, &c.TargetAccountCode, &c.TargetAccountName, &c.Reason, &c.RuleReference, &c.BeforeBill, &c.Digest, &c.RequestID, &c.State, &c.ProposedBy, &c.ApprovedBy, &c.Expires, &c.Readback, &c.Error)
	return c, err
}

// latestXeroBills returns the bills read by the close's latest Xero capture.
func latestXeroBills(ctx context.Context, q querier, closeID string) (snapshot, connection, tenant string, bills []xero.Bill, err error) {
	var raw []byte
	if err = q.QueryRowContext(ctx, `SELECT id::text,connection_id::text,tenant_id,COALESCE(payload->'transactions'->'records'->'Invoices','[]') FROM xero_snapshots WHERE close_id::text=$1 AND `+sourceAllows("xero_snapshots.close_id", "xero")+` ORDER BY captured_at DESC,xero_snapshots.id DESC LIMIT 1`, closeID).Scan(&snapshot, &connection, &tenant, &raw); err != nil {
		return
	}
	var records []json.RawMessage
	json.Unmarshal(raw, &records)
	for _, record := range records {
		if bill, e := xero.ParseBill(record); e == nil && bill.Type == "ACCPAY" {
			bills = append(bills, bill)
		}
	}
	return
}

func zeroAmount(n json.Number) bool {
	cents, err := prepaids.Amount(string(n))
	return err == nil && cents == 0
}

type xeroRecode struct {
	grant          xeroGrant
	snapshot       string
	bill           xero.Bill
	target         struct{ AccountID, Code, Name string }
	amount         string
	access, tenant string
	digest         string
}

// planXeroRecode checks one bill line recode against the client's grant, the close's latest capture and live Xero reads.
// The digest covers what was checked, so any change before approval or execution refuses.
func planXeroRecode(ctx context.Context, db *sql.DB, org, entity, closeID, period, invoiceID, lineID, targetID, reason, rule string) (xeroRecode, error) {
	var p xeroRecode
	// The client's grant under any of its names, the one on the connection the month's latest capture came from first,
	// then the month's own name's; that the capture came from the granted connection is checked below.
	g, err := scanXeroGrant(db.QueryRowContext(ctx, `SELECT `+xeroGrantColumns+` WHERE organization_id=$1 AND client_identity(organization_id,client_entity)=client_identity($1,$2) AND revoked_at IS NULL
 ORDER BY connection_id IS NOT DISTINCT FROM (SELECT s.connection_id FROM xero_snapshots s WHERE s.close_id::text=$3 ORDER BY s.captured_at DESC,s.id DESC LIMIT 1) DESC,lower(client_entity)=lower($2) DESC,xero_posting_grants.id DESC LIMIT 1`, org, entity, closeID))
	if err != nil || time.Now().After(g.Expires) || !slices.Contains(g.Kinds, "bill_correction") {
		return p, refusal{409, "An unexpired Xero grant that covers bill corrections is required."}
	}
	access, tenant, err := xeroAccess(ctx, db, org, g.ConnectionID)
	if err != nil {
		return p, err
	}
	if tenant != g.TenantID {
		return p, refusal{409, "The Xero connection now points at a different organisation than the grant."}
	}
	if !xero.CanWriteInvoices(access) {
		return p, refusal{409, "Reconnect Xero to allow bill corrections."}
	}
	snapshot, connection, captured, bills, err := latestXeroBills(ctx, db, closeID)
	if err != nil || connection != g.ConnectionID || captured != tenant {
		return p, refusal{409, "Capture Xero for this close with the granted connection first."}
	}
	i := slices.IndexFunc(bills, func(b xero.Bill) bool { return b.InvoiceID == invoiceID })
	if i < 0 {
		return p, refusal{409, "Select a bill from the close's latest Xero capture."}
	}
	live, err := xero.ReadBill(ctx, access, tenant, invoiceID)
	if err != nil {
		return p, refusal{502, "The bill could not be read from Xero."}
	}
	if !xero.SameBill(bills[i], live) {
		return p, refusal{409, "The bill changed in Xero since the latest capture. Capture Xero again and review it."}
	}
	day, err := xero.Day(live.Date)
	if err != nil || day.Format("2006-01") != period || live.Status != "AUTHORISED" || !zeroAmount(live.AmountPaid) || !zeroAmount(live.AmountCredited) || !zeroAmount(live.TotalTax) || len(live.LineItems) > 10 {
		return p, refusal{409, "Only an approved bill in the close month with nothing paid or credited, no tax and up to ten lines can be corrected."}
	}
	accountsRaw, err := xero.Get(ctx, access, tenant, "Accounts", 4<<20)
	organisationRaw, organisationErr := xero.Get(ctx, access, tenant, "Organisation", 256<<10)
	var directory struct {
		Accounts []struct{ AccountID, Code, Name, Status, Class string }
	}
	var organisation struct {
		Organisations []struct{ PeriodLockDate, EndOfYearLockDate string }
	}
	if err != nil || organisationErr != nil || json.Unmarshal(accountsRaw, &directory) != nil || json.Unmarshal(organisationRaw, &organisation) != nil || len(organisation.Organisations) != 1 {
		return p, refusal{502, "Xero accounts or organisation could not be read."}
	}
	for _, lock := range []string{organisation.Organisations[0].PeriodLockDate, organisation.Organisations[0].EndOfYearLockDate} {
		if at, e := xero.Day(lock); lock != "" && (e != nil || !day.After(at)) {
			return p, refusal{409, "The Xero organisation's lock date covers the bill; it cannot be corrected."}
		}
	}
	expense := func(id string) int {
		return slices.IndexFunc(directory.Accounts, func(a struct{ AccountID, Code, Name, Status, Class string }) bool {
			return id != "" && a.AccountID == id && a.Status == "ACTIVE" && a.Class == "EXPENSE" && slices.Contains(g.AccountIDs, id)
		})
	}
	j := slices.IndexFunc(live.LineItems, func(l xero.BillLine) bool { return l.LineItemID == lineID })
	if j < 0 {
		return p, refusal{422, "Choose one line of the bill."}
	}
	line := live.LineItems[j]
	if k := expense(line.AccountID); k < 0 || directory.Accounts[k].Code != line.AccountCode || line.ItemCode != "" {
		return p, refusal{409, "The line must have no item and post to a granted, active expense account."}
	}
	k := expense(targetID)
	if k < 0 || directory.Accounts[k].Code == "" || targetID == line.AccountID {
		return p, refusal{422, "Choose a different granted, active expense account that has a code."}
	}
	cents, amountErr := prepaids.Amount(string(line.LineAmount))
	limit, limitErr := prepaids.Amount(g.MaxAmount)
	if amountErr != nil || limitErr != nil || cents <= 0 || cents > limit {
		return p, refusal{409, "The line must be a positive amount within this client's Xero grant limit."}
	}
	target := directory.Accounts[k]
	bill, _ := json.Marshal(live)
	locks, _ := json.Marshal(organisation.Organisations[0])
	p = xeroRecode{grant: g, snapshot: snapshot, bill: live, amount: prepaids.Money(cents), access: access, tenant: tenant,
		digest: preparation.Digest([]byte(strings.Join([]string{g.ID, snapshot, string(bill), lineID, target.AccountID, target.Code, target.Name, reason, rule, string(locks)}, "\x00")))}
	p.target.AccountID, p.target.Code, p.target.Name = target.AccountID, target.Code, target.Name
	return p, nil
}

// verifyXeroRecode reads the bill back and settles the attempt: verified when only the approved line's account changed.
func verifyXeroRecode(ctx context.Context, db *sql.DB, access, tenant string, c xeroCorrection) error {
	var before xero.Bill
	json.Unmarshal(c.BeforeBill, &before)
	after, err := xero.ReadBill(ctx, access, tenant, c.InvoiceID)
	if err == nil && !xero.SameBill(xero.Recoded(before, c.TargetLineID, c.TargetAccountID, c.TargetAccountCode), after) {
		err = refusal{409, "The bill reads back differently from the approved correction. Investigate it in Xero; never correct it again."}
	}
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err != nil {
		db.ExecContext(persist, `UPDATE xero_corrections SET state='unknown',last_error=$2 WHERE id::text=$1 AND state IN ('executing','verified')`, c.ID, err.Error())
		return err
	}
	raw, _ := json.Marshal(after)
	if _, err = db.ExecContext(persist, `UPDATE xero_corrections SET state='verified',readback=$2,verified_at=COALESCE(verified_at,now()),last_error='' WHERE id::text=$1 AND state IN ('executing','unknown','verified')`, c.ID, string(raw)); err != nil {
		return refusal{503, "The verified readback could not be recorded. Verify again; never correct the bill again."}
	}
	return nil
}

func registerXeroCorrections(mux *http.ServeMux, db *sql.DB) {
	scope := func(w http.ResponseWriter, r *http.Request, permission string) (string, string, string, bool) {
		a, ok := productionActor(w, r, permission)
		if !ok {
			return "", "", "", false
		}
		var entity, period string
		if err := db.QueryRowContext(r.Context(), `SELECT entity,period FROM client_closes WHERE id::text=$1 AND organization_id=$2`, r.PathValue("id"), a.Organization).Scan(&entity, &period); err != nil {
			problem(w, 404, "Client close not found.")
			return "", "", "", false
		}
		if access, err := clientAccess(db, r, entity); err != nil || !access {
			problem(w, 403, "Client access is required.")
			return "", "", "", false
		}
		return a.Organization, entity, period, true
	}
	mux.HandleFunc("GET /v1/closes/{id}/xero/bills/candidates", func(w http.ResponseWriter, r *http.Request) {
		_, _, period, ok := scope(w, r, "")
		if !ok {
			return
		}
		_, _, _, bills, err := latestXeroBills(r.Context(), db, r.PathValue("id"))
		if err == sql.ErrNoRows {
			jsonReply(w, 200, map[string]any{"items": []any{}, "message": "Capture Xero to review bill candidates."})
			return
		}
		if err != nil {
			problem(w, 503, "Candidates unavailable.")
			return
		}
		items := []map[string]string{}
		for _, b := range bills {
			day, e := xero.Day(b.Date)
			if e == nil && day.Format("2006-01") == period && zeroAmount(b.AmountPaid) && zeroAmount(b.AmountCredited) && zeroAmount(b.TotalTax) && len(items) < 100 {
				items = append(items, map[string]string{"id": b.InvoiceID, "number": b.InvoiceNumber, "date": day.Format("2006-01-02"), "total": string(b.Total), "currency": b.CurrencyCode})
			}
		}
		jsonReply(w, 200, map[string]any{"items": items})
	})
	path := "/v1/closes/{id}/xero/bills/{invoice}/correction"
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		if _, _, _, ok := scope(w, r, ""); !ok {
			return
		}
		c, err := readXeroCorrection(r.Context(), db, r.PathValue("id"), r.PathValue("invoice"))
		if err == sql.ErrNoRows {
			_, _, _, bills, _ := latestXeroBills(r.Context(), db, r.PathValue("id"))
			if i := slices.IndexFunc(bills, func(b xero.Bill) bool { return b.InvoiceID == r.PathValue("invoice") }); i >= 0 {
				before, _ := json.Marshal(bills[i])
				jsonReply(w, 200, map[string]any{"state": "not_prepared", "before_bill": json.RawMessage(before)})
				return
			}
			problem(w, 404, "Select a bill from the close's latest Xero capture.")
			return
		}
		if err != nil {
			problem(w, 503, "Correction unavailable.")
			return
		}
		jsonReply(w, 200, c)
	})
	mux.HandleFunc("POST "+path, func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Action          string `json:"action"`
			Digest          string `json:"digest"`
			Reason          string `json:"reason"`
			RuleReference   string `json:"rule_reference"`
			TargetLineID    string `json:"target_line_id"`
			TargetAccountID string `json:"target_account_id"`
			Confirmed       bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		permission := "posting:approve"
		if in.Action == "execute" || in.Action == "verify" {
			permission = "posting:execute"
		}
		org, entity, period, ok := scope(w, r, permission)
		if !ok {
			return
		}
		a, _ := productionActor(w, r, permission)
		closeID, invoiceID := r.PathValue("id"), r.PathValue("invoice")
		record, recordErr := readXeroCorrection(r.Context(), db, closeID, invoiceID)
		// An attempt is settled through any active connection to the same organisation, even after its grant ends.
		if in.Action == "verify" {
			if recordErr != nil || (record.State != "executing" && record.State != "unknown" && record.State != "verified") {
				problem(w, 409, "Only an attempted correction can be verified.")
				return
			}
			var connection string
			if err := db.QueryRowContext(r.Context(), `SELECT id::text FROM xero_connections WHERE organization_id=$1 AND client_identity(organization_id,client_entity)=client_identity($1,$2) AND tenant_id=$3 AND state='active' ORDER BY lower(client_entity)=lower($2) DESC,xero_connections.id DESC LIMIT 1`, org, entity, record.TenantID).Scan(&connection); err != nil {
				problem(w, 409, "Connect the same Xero organisation for this client to verify the attempt.")
				return
			}
			access, tenant, err := xeroAccess(r.Context(), db, org, connection)
			if err == nil && tenant != record.TenantID {
				err = refusal{409, "The Xero connection now points at a different organisation than the attempt."}
			}
			if err == nil {
				err = verifyXeroRecode(r.Context(), db, access, tenant, record)
			}
			if err != nil {
				refuse(w, err)
				return
			}
			record, _ = readXeroCorrection(r.Context(), db, closeID, invoiceID)
			jsonReply(w, 200, record)
			return
		}
		if in.Action != "preview" && in.Action != "approve" && in.Action != "execute" {
			problem(w, 422, "Choose preview, approve, execute or verify.")
			return
		}
		if !slices.Contains(xeroPostingKinds(), "bill_correction") {
			problem(w, 409, "Xero bill corrections are not enabled yet. They turn on after Xero's behavior is checked on a demo company.")
			return
		}
		if in.Action != "verify" && refuseLedgerWrites(w, r.Context(), db, org) {
			return
		}
		var signed bool
		if err := db.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT decision='signed_off' FROM close_signoffs WHERE close_id::text=$1 ORDER BY id DESC LIMIT 1),false)`, closeID).Scan(&signed); err != nil || signed {
			problem(w, 409, "Use an open close for this client.")
			return
		}
		if in.Action == "preview" {
			in.Reason, in.RuleReference = strings.TrimSpace(in.Reason), strings.TrimSpace(in.RuleReference)
			if !in.Confirmed || len(in.Reason) < 5 || len(in.Reason) > 2000 || len(in.RuleReference) < 5 || len(in.RuleReference) > 200 {
				problem(w, 422, "Confirm one accountant-reviewed coding exception, its reason and the accountant's rule it follows.")
				return
			}
			plan, err := planXeroRecode(r.Context(), db, org, entity, closeID, period, invoiceID, in.TargetLineID, in.TargetAccountID, in.Reason, in.RuleReference)
			if err != nil {
				refuse(w, err)
				return
			}
			before, _ := json.Marshal(plan.bill)
			saved, err := db.ExecContext(r.Context(), `INSERT INTO xero_corrections(close_id,snapshot_id,grant_id,connection_id,tenant_id,invoice_id,target_line_id,target_account_id,target_account_code,target_account_name,reason,rule_reference,before_bill,digest,state,proposed_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'preview',$15)
 ON CONFLICT(tenant_id,invoice_id) DO UPDATE SET snapshot_id=EXCLUDED.snapshot_id,grant_id=EXCLUDED.grant_id,connection_id=EXCLUDED.connection_id,target_line_id=EXCLUDED.target_line_id,target_account_id=EXCLUDED.target_account_id,target_account_code=EXCLUDED.target_account_code,target_account_name=EXCLUDED.target_account_name,reason=EXCLUDED.reason,rule_reference=EXCLUDED.rule_reference,before_bill=EXCLUDED.before_bill,digest=EXCLUDED.digest,state='preview',proposed_by=EXCLUDED.proposed_by,approved_by=NULL,approval_expires_at=NULL
 WHERE xero_corrections.close_id=EXCLUDED.close_id AND xero_corrections.attempted_at IS NULL`, closeID, plan.snapshot, plan.grant.ID, plan.grant.ConnectionID, plan.tenant, invoiceID, in.TargetLineID, plan.target.AccountID, plan.target.Code, plan.target.Name, in.Reason, in.RuleReference, string(before), plan.digest, a.Subject)
			if err != nil {
				problem(w, 503, "Could not save the correction preview.")
				return
			}
			if n, _ := saved.RowsAffected(); n != 1 {
				problem(w, 409, "This bill already has an attempted correction, possibly from another close. Verify its outcome.")
				return
			}
			record, _ = readXeroCorrection(r.Context(), db, closeID, invoiceID)
			jsonReply(w, 200, record)
			return
		}
		if recordErr != nil || record.Digest != in.Digest || !in.Confirmed {
			problem(w, 409, "Refresh and confirm the exact saved correction.")
			return
		}
		plan, err := planXeroRecode(r.Context(), db, org, entity, closeID, period, invoiceID, record.TargetLineID, record.TargetAccountID, record.Reason, record.RuleReference)
		if err != nil {
			refuse(w, err)
			return
		}
		if plan.digest != record.Digest || plan.grant.ID != record.GrantID {
			problem(w, 409, "The bill, grant or Xero accounts changed. No write was made; preview the correction again.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Review state unavailable.")
			return
		}
		defer tx.Rollback()
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM client_closes WHERE id::text=$1 AND organization_id=$2 FOR UPDATE`, closeID, org).Scan(&closeID); err != nil {
			problem(w, 409, "Close changed.")
			return
		}
		if refuseSignedOff(w, r.Context(), tx, closeID) {
			return
		}
		if refuseLedgerWrites(w, r.Context(), tx, org) {
			return
		}
		approver := a.Subject
		if in.Action == "execute" {
			approver = record.ApprovedBy
		}
		if issue, err := correctionIndependenceIssue(r.Context(), tx, closeID, record.ProposedBy, approver, plan.amount); err != nil {
			problem(w, 503, "Firm review policy could not be checked. No write was made.")
			return
		} else if issue != "" {
			problem(w, 409, issue)
			return
		}
		if in.Action == "approve" {
			result, err := tx.ExecContext(r.Context(), `UPDATE xero_corrections SET state='approved',approved_by=$2,approval_expires_at=now()+interval '10 minutes' WHERE id::text=$1 AND digest=$3 AND state IN ('preview','approved')`, record.ID, a.Subject, record.Digest)
			if err != nil {
				problem(w, 503, "Approval unavailable.")
				return
			}
			if n, _ := result.RowsAffected(); n != 1 || tx.Commit() != nil {
				problem(w, 409, "The correction changed or was attempted. Refresh it.")
				return
			}
			record, _ = readXeroCorrection(r.Context(), db, closeID, invoiceID)
			jsonReply(w, 200, record)
			return
		}
		var current string
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM xero_posting_grants WHERE id::text=$1 AND revoked_at IS NULL AND expires_at>now() AND EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=xero_posting_grants.organization_id AND subject=xero_posting_grants.granted_by AND granted) FOR UPDATE`, record.GrantID).Scan(&current); err != nil {
			problem(w, 409, "Xero grant expired or was revoked.")
			return
		}
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM xero_connections WHERE id::text=$1 AND state='active' AND tenant_id=$2 FOR UPDATE`, plan.grant.ConnectionID, record.TenantID).Scan(&current); err != nil {
			problem(w, 409, "The Xero connection changed before this decision.")
			return
		}
		result, err := tx.ExecContext(r.Context(), `UPDATE xero_corrections SET state='executing',attempted_by=$2,attempted_at=now() WHERE id::text=$1 AND digest=$3 AND state='approved' AND approval_expires_at>now() AND approved_by=$4 AND EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=$5 AND subject=xero_corrections.approved_by AND granted)`, record.ID, a.Subject, record.Digest, record.ApprovedBy, org)
		if err != nil {
			problem(w, 503, "Could not reserve one attempt; no Xero call was made.")
			return
		}
		if n, _ := result.RowsAffected(); n != 1 {
			if approvalLapsed(record.State, record.Expires) {
				problemCode(w, 409, "APPROVAL_EXPIRED", "The approval expired after 10 minutes. Approve it again; no write was made.")
				return
			}
			problem(w, 409, "A current approval is required; no Xero call was made.")
			return
		}
		if tx.Commit() != nil {
			problem(w, 503, "The attempt reservation was not confirmed; inspect the status before any retry.")
			return
		}
		if err = xero.RecodeBill(r.Context(), plan.access, plan.tenant, record.RequestID, plan.bill, record.TargetLineID, record.TargetAccountID, record.TargetAccountCode); err != nil {
			persist, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
			defer cancel()
			db.ExecContext(persist, `UPDATE xero_corrections SET state='unknown',last_error=$2 WHERE id::text=$1 AND state='executing'`, record.ID, err.Error())
			problemCode(w, 502, "EXECUTION_UNKNOWN", "The bill update outcome is uncertain. Inspect the bill in Xero and verify it here; never correct it again.")
			return
		}
		if err = verifyXeroRecode(r.Context(), db, plan.access, plan.tenant, record); err != nil {
			problemCode(w, 502, "EXECUTION_UNKNOWN", "The bill update returned, but its readback is unverified. Inspect it in Xero; never correct it again.")
			return
		}
		record, _ = readXeroCorrection(r.Context(), db, closeID, invoiceID)
		jsonReply(w, 200, record)
	})
}
