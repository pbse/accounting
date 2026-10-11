package transport

import (
	"close.local/backend/internal/auth"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/xero"
	"github.com/jackc/pgx/v5/pgconn"
)

// Xero posting keeps the QuickBooks production controls: a person's client grant, a preview from live Xero reads, a
// person's ten-minute approval, one durable attempt by a person with posting:execute, and a readback.

var xeroID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// postedElsewhere reports whether the other ledger's table already holds an attempt for this draft or its source.
// Both ledgers ask under the close lock before reserving, so a draft or source reaches at most one of them.
func postedElsewhere(ctx context.Context, q querier, other, closeID, paperID, sourceKey string) (bool, error) {
	var posted bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+other+` WHERE close_id::text=$1 AND (workpaper_id::text=$2 OR source_key=$3) AND attempted_at IS NOT NULL)`, closeID, paperID, sourceKey).Scan(&posted)
	return posted, err
}

type xeroGrant struct {
	ID           string    `json:"id"`
	ConnectionID string    `json:"connection_id"`
	TenantID     string    `json:"tenant_id"`
	AccountIDs   []string  `json:"account_ids"`
	Kinds        []string  `json:"kinds"`
	MaxAmount    string    `json:"max_amount"`
	Expires      time.Time `json:"expires_at"`
	Reason       string    `json:"reason"`
	GrantedBy    string    `json:"granted_by"`
}

const xeroGrantColumns = `id::text,connection_id::text,tenant_id,to_jsonb(account_ids)::text,to_jsonb(kinds)::text,max_amount::text,expires_at,reason,granted_by FROM xero_posting_grants`

func scanXeroGrant(row *sql.Row) (xeroGrant, error) {
	var g xeroGrant
	var accounts, kinds string
	err := row.Scan(&g.ID, &g.ConnectionID, &g.TenantID, &accounts, &kinds, &g.MaxAmount, &g.Expires, &g.Reason, &g.GrantedBy)
	if err == nil && (json.Unmarshal([]byte(accounts), &g.AccountIDs) != nil || json.Unmarshal([]byte(kinds), &g.Kinds) != nil) {
		err = errors.New("saved grant is invalid")
	}
	return g, err
}

type xeroPosting struct {
	ID           string          `json:"id"`
	RequestID    string          `json:"request_id"`
	State        string          `json:"state"`
	Digest       string          `json:"digest"`
	GrantID      string          `json:"grant_id"`
	ConnectionID string          `json:"connection_id"`
	TenantID     string          `json:"tenant_id"`
	Journal      json.RawMessage `json:"journal"`
	LedgerBefore json.RawMessage `json:"ledger_before"`
	ApprovedBy   string          `json:"approved_by"`
	Expires      *time.Time      `json:"approval_expires_at"`
	ExternalID   string          `json:"external_id"`
	Readback     json.RawMessage `json:"readback"`
	Error        string          `json:"error"`
}

func readXeroPosting(ctx context.Context, db querier, closeID, paperID string) (xeroPosting, error) {
	var p xeroPosting
	err := db.QueryRowContext(ctx, `SELECT id::text,request_id::text,state,digest,grant_id::text,connection_id::text,tenant_id,journal,ledger_before,COALESCE(approved_by,''),approval_expires_at,COALESCE(external_id,''),COALESCE(readback,'null'::jsonb),last_error FROM xero_postings WHERE close_id::text=$1 AND workpaper_id::text=$2`, closeID, paperID).Scan(&p.ID, &p.RequestID, &p.State, &p.Digest, &p.GrantID, &p.ConnectionID, &p.TenantID, &p.Journal, &p.LedgerBefore, &p.ApprovedBy, &p.Expires, &p.ExternalID, &p.Readback, &p.Error)
	return p, err
}

// xeroPostingKinds are the draft kinds enabled for Xero posting; every kind stays off until the operator has checked
// Xero's behavior for it.
func xeroPostingKinds() []string {
	kinds := []string{}
	for _, kind := range strings.Split(os.Getenv("XERO_POSTING_KINDS"), ",") {
		if kind = strings.TrimSpace(kind); kind != "" {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

type xeroPlan struct {
	source    string
	grant     xeroGrant
	journal   xero.ManualJournal
	before    map[string]string
	digest    string
	sourceKey string
	access    string
	tenant    string
}

// planXeroPosting builds one manual journal for a draft from live Xero reads. Each entry account must match exactly
// one active account the grant lists, the date must be after the organisation's lock dates, and the digest covers
// the journal accounts' Trial Balance at that date, so any change before approval or execution refuses.
func planXeroPosting(ctx context.Context, db *sql.DB, org, entity, closeID, paperID, kind, period, paperDigest string, paperRaw []byte) (xeroPlan, error) {
	var plan xeroPlan
	var entries []prepaids.Adjustment
	var source string
	if kind == "prepaid" {
		var paper prepaids.Workpaper
		month, err := time.Parse("2006-01", period)
		if err != nil || json.Unmarshal(paperRaw, &paper) != nil || paper.Proposal == nil || paper.Proposal.Date != month.AddDate(0, 1, -1).Format("2006-01-02") {
			return plan, refusal{409, "Only a prepaid entry dated at the close month-end can be posted."}
		}
		entries, source = []prepaids.Adjustment{*paper.Proposal}, paper.Input.SourceReference
	} else {
		var err error
		if entries, source, err = postable(kind, paperRaw, period); err != nil {
			return plan, refusal{409, err.Error()}
		}
	}
	sourceKey, err := postingSource(ctx, db, closeID, paperID, kind, paperRaw, source)
	if err != nil {
		return plan, err
	}
	g, err := xeroBooksGrant(ctx, db, org, entity, closeID)
	if err != nil || time.Now().After(g.Expires) {
		return plan, refusal{409, "An unexpired client-specific Xero posting grant is required."}
	}
	if !slices.Contains(g.Kinds, kind) {
		return plan, refusal{409, "This client's Xero grant does not cover " + kind + " drafts. Ask for a new grant that includes them."}
	}
	access, tenant, err := xeroAccess(ctx, db, org, g.ConnectionID)
	if err != nil {
		return plan, err
	}
	if tenant != g.TenantID {
		return plan, refusal{409, "The Xero connection now points at a different organisation than the grant."}
	}
	if !xero.CanPostJournals(access) {
		return plan, refusal{409, "Reconnect Xero to allow manual journals before posting."}
	}
	accountsRaw, err := xero.Get(ctx, access, tenant, "Accounts", 4<<20)
	organisationRaw, organisationErr := xero.Get(ctx, access, tenant, "Organisation", 256<<10)
	var directory struct {
		Accounts []struct{ AccountID, Name, Status string }
	}
	var organisation struct {
		Organisations []struct{ PeriodLockDate, EndOfYearLockDate string }
	}
	if err != nil || organisationErr != nil || json.Unmarshal(accountsRaw, &directory) != nil || json.Unmarshal(organisationRaw, &organisation) != nil || len(organisation.Organisations) != 1 {
		return plan, refusal{502, "Xero accounts or organisation could not be read."}
	}
	find := func(name string) (string, error) {
		match := ""
		for _, a := range directory.Accounts {
			if strings.EqualFold(strings.TrimSpace(a.Name), strings.TrimSpace(name)) && a.Status == "ACTIVE" && slices.Contains(g.AccountIDs, a.AccountID) {
				if match != "" {
					return "", refusal{422, "More than one granted, active Xero account is named " + name + "."}
				}
				match = a.AccountID
			}
		}
		if match == "" {
			return "", refusal{422, "No granted, active Xero account is named " + name + "."}
		}
		return match, nil
	}
	date := entries[0].Date
	day, err := time.Parse("2006-01-02", date)
	if err != nil {
		return plan, refusal{409, "The entry date is unreadable."}
	}
	for _, lock := range []string{organisation.Organisations[0].PeriodLockDate, organisation.Organisations[0].EndOfYearLockDate} {
		if lock == "" {
			continue
		}
		if at, e := xero.Day(lock); e != nil || !day.After(at) {
			return plan, refusal{409, "The Xero organisation's lock date covers " + date + "; the journal cannot be posted."}
		}
	}
	journal := xero.ManualJournal{Narration: "Approved " + kind + " draft #" + paperID + ": " + source, Date: date, Status: "POSTED", LineAmountTypes: "NoTax"}
	var total int64
	ids := []string{}
	for _, entry := range entries {
		if entry.Date != date {
			return plan, refusal{409, "A draft's entries must share one date to post as one journal."}
		}
		debit, err := find(entry.DebitAccount)
		if err != nil {
			return plan, err
		}
		credit, err := find(entry.CreditAccount)
		if err != nil {
			return plan, err
		}
		cents, err := prepaids.Amount(entry.Amount)
		if err != nil || cents <= 0 || debit == credit {
			return plan, refusal{422, "Unsupported journal amount or account pair."}
		}
		total += cents
		amount := prepaids.Money(cents)
		journal.JournalLines = append(journal.JournalLines, xero.JournalLine{AccountID: debit, LineAmount: json.Number(amount), Description: entry.DebitAccount}, xero.JournalLine{AccountID: credit, LineAmount: json.Number("-" + amount), Description: entry.CreditAccount})
		ids = append(ids, debit, credit)
	}
	if limit, err := prepaids.Amount(g.MaxAmount); err != nil || total > limit {
		return plan, refusal{409, "Journal exceeds this client's Xero grant limit."}
	}
	report, err := xero.TrialBalanceAt(ctx, access, tenant, day)
	if err != nil {
		return plan, refusal{502, err.Error()}
	}
	before, err := xero.Net(report, ids)
	if err != nil {
		return plan, refusal{502, err.Error()}
	}
	j, _ := json.Marshal(journal)
	b, _ := json.Marshal(before)
	locks, _ := json.Marshal(organisation.Organisations[0])
	return xeroPlan{source: source, grant: g, journal: journal, before: before, sourceKey: sourceKey, access: access, tenant: tenant,
		digest: preparation.Digest([]byte(strings.Join([]string{g.ID, paperDigest, string(j), string(b), string(locks)}, "\x00")))}, nil
}

// verifyXero reads the saved manual journal back and marks the attempt verified only when it matches exactly.
func verifyXero(w http.ResponseWriter, r *http.Request, db *sql.DB, access, tenant string, record xeroPosting, externalID string) {
	closeID, paperID := r.PathValue("id"), r.PathValue("paper")
	var expected xero.ManualJournal
	json.Unmarshal(record.Journal, &expected)
	got, err := xero.ReadManualJournal(r.Context(), access, tenant, externalID)
	if err != nil || !xero.SameJournal(expected, got) {
		problemCode(w, 502, "EXECUTION_UNKNOWN", "The manual journal could not be read back exactly as approved. Investigate it in Xero; never post it again.")
		return
	}
	raw, _ := json.Marshal(got)
	if _, err = db.ExecContext(r.Context(), `UPDATE xero_postings SET state='verified',external_id=$2,readback=$3,verified_at=now(),last_error='' WHERE id::text=$1 AND state IN ('unknown','executing')`, record.ID, externalID, string(raw)); err != nil {
		problem(w, 503, "The verified journal could not be recorded. Verify again; never post it again.")
		return
	}
	p, err := readXeroPosting(r.Context(), db, closeID, paperID)
	if err != nil {
		problem(w, 503, "Posting status unavailable.")
		return
	}
	jsonReply(w, 200, p)
}

func registerXeroPosting(mux *http.ServeMux, db *sql.DB) {
	scope := func(w http.ResponseWriter, r *http.Request, permission string) (string, string, bool) {
		a, ok := productionActor(w, r, permission)
		if !ok {
			return "", "", false
		}
		entity, err := productionScope(r.Context(), db, r.PathValue("id"), a.Organization)
		if err != nil {
			problem(w, 404, "Client close not found.")
			return "", "", false
		}
		if access, err := clientAccess(db, r, entity); err != nil || !access {
			problem(w, 403, "Client access is required.")
			return "", "", false
		}
		return a.Organization, entity, true
	}
	grantPath := "/v1/closes/{id}/xero-grant"
	mux.HandleFunc("GET "+grantPath, func(w http.ResponseWriter, r *http.Request) {
		if org, entity, ok := scope(w, r, ""); ok {
			xeroGrantRead(w, r, db, org, entity)
		}
	})
	mux.HandleFunc("POST "+grantPath, func(w http.ResponseWriter, r *http.Request) {
		if _, entity, ok := scope(w, r, "posting:approve"); ok {
			a, _ := productionActor(w, r, "posting:approve")
			xeroGrantChange(w, r, db, a, entity)
		}
	})

	path := "/v1/closes/{id}/workpapers/{paper}/xero-posting"
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := scope(w, r, ""); !ok {
			return
		}
		p, err := readXeroPosting(r.Context(), db, r.PathValue("id"), r.PathValue("paper"))
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
		org, entity, ok := scope(w, r, permission)
		if !ok {
			return
		}
		a, _ := productionActor(w, r, permission)
		closeID, paperID := r.PathValue("id"), r.PathValue("paper")
		record, recordErr := readXeroPosting(r.Context(), db, closeID, paperID)
		if in.Action == "verify" {
			if recordErr != nil || record.State == "preview" || record.State == "approved" || record.State == "verified" {
				problem(w, 409, "No unresolved Xero attempt is saved for this draft. Never retry an unknown write.")
				return
			}
			external := record.ExternalID
			if external == "" && xeroID.MatchString(in.ExternalID) {
				external = in.ExternalID
			}
			if external == "" || (in.ExternalID != "" && !strings.EqualFold(in.ExternalID, external)) {
				problem(w, 422, "Enter the exact Xero ManualJournalID found for this saved attempt.")
				return
			}
			// A reconnection replaces the connection; the attempt stays bound to its organisation.
			var connection string
			if err := db.QueryRowContext(r.Context(), `SELECT id::text FROM xero_connections WHERE organization_id=$1 AND client_identity(organization_id,client_entity)=client_identity($1,$2) AND tenant_id=$3 AND state='active' ORDER BY lower(client_entity)=lower($2) DESC,id DESC LIMIT 1`, org, entity, record.TenantID).Scan(&connection); err != nil {
				problem(w, 409, "Connect the same Xero organisation for this client to verify the attempt.")
				return
			}
			access, tenant, err := xeroAccess(r.Context(), db, org, connection)
			if err != nil {
				refuse(w, err)
				return
			}
			if tenant != record.TenantID {
				problem(w, 409, "The Xero connection now points at a different organisation than the attempt.")
				return
			}
			verifyXero(w, r, db, access, tenant, record, external)
			return
		}
		if in.Action != "preview" && in.Action != "approve" && in.Action != "execute" {
			problem(w, 422, "Choose preview, approve, execute or verify.")
			return
		}
		var paperRaw []byte
		var paperDigest, kind, period string
		if err := db.QueryRowContext(r.Context(), `SELECT w.paper,w.digest,w.kind,c.period FROM workpapers w JOIN client_closes c ON c.id=w.close_id WHERE w.id::text=$1 AND w.close_id::text=$2 AND c.organization_id=$3`, paperID, closeID, org).Scan(&paperRaw, &paperDigest, &kind, &period); err != nil || !slices.Contains(postableKinds, kind) {
			problem(w, 404, "Saved workpaper with postable entries not found.")
			return
		}
		var head struct {
			Input struct {
				Entity      string `json:"entity"`
				ClosePeriod string `json:"close_period"`
			} `json:"input"`
		}
		if json.Unmarshal(paperRaw, &head) != nil || strings.TrimSpace(head.Input.Entity) != entity || head.Input.ClosePeriod != period {
			problem(w, 409, "Workpaper client or period does not match this close.")
			return
		}
		if !slices.Contains(xeroPostingKinds(), kind) {
			problem(w, 409, "Xero posting for "+kind+" drafts is not enabled yet. It turns on after Xero's behavior is checked for it.")
			return
		}
		if in.Action != "verify" && refuseLedgerWrites(w, r.Context(), db, org) {
			return
		}
		if err := preparation.CurrencyIssue(r.Context(), db, closeID, paperRaw); err != nil {
			problem(w, 409, err.Error())
			return
		}
		plan, err := planXeroPosting(r.Context(), db, org, entity, closeID, paperID, kind, period, paperDigest, paperRaw)
		if err != nil {
			refuse(w, err)
			return
		}
		if in.Action == "preview" {
			journal, _ := json.Marshal(plan.journal)
			before, _ := json.Marshal(plan.before)
			saved, err := db.ExecContext(r.Context(), `INSERT INTO xero_postings(close_id,workpaper_id,source_key,grant_id,connection_id,tenant_id,digest,journal,ledger_before,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'preview') ON CONFLICT(workpaper_id) DO UPDATE SET grant_id=EXCLUDED.grant_id,connection_id=EXCLUDED.connection_id,tenant_id=EXCLUDED.tenant_id,digest=EXCLUDED.digest,journal=EXCLUDED.journal,ledger_before=EXCLUDED.ledger_before,state='preview',approved_by=NULL,approval_expires_at=NULL WHERE xero_postings.attempted_at IS NULL`, closeID, paperID, plan.sourceKey, plan.grant.ID, plan.grant.ConnectionID, plan.tenant, plan.digest, string(journal), string(before))
			var unique *pgconn.PgError
			if errors.As(err, &unique) && unique.Code == "23505" {
				problem(w, 409, "Another draft of this source already has a Xero intent. Review that intent before posting.")
				return
			}
			if err != nil {
				problem(w, 503, "Xero preview could not be saved.")
				return
			}
			if n, _ := saved.RowsAffected(); n != 1 {
				problem(w, 409, "An execution attempt already exists. Investigate the saved result; never create a second intent.")
				return
			}
			p, err := readXeroPosting(r.Context(), db, closeID, paperID)
			if err != nil {
				problem(w, 503, "Posting status unavailable.")
				return
			}
			jsonReply(w, 200, p)
			return
		}
		if recordErr != nil || record.Digest != in.Digest || record.Digest != plan.digest || record.GrantID != plan.grant.ID {
			problem(w, 409, "Intent, grant or live Xero ledger changed. No write was made; refresh the preview.")
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
		var activeMember bool
		if err = tx.QueryRowContext(r.Context(), `SELECT enabled AND EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=$1 AND subject=$2 AND granted) FROM organizations WHERE id=$1 FOR UPDATE`, org, a.Subject).Scan(&activeMember); err != nil || !activeMember {
			problem(w, 403, "Firm or member access was revoked before this posting decision.")
			return
		}
		if refuseLedgerWrites(w, r.Context(), tx, org) {
			return
		}
		if status, message := draftApproved(r.Context(), tx, closeID, paperID, a, in.Action == "approve"); status != 0 {
			problem(w, status, message)
			return
		}
		var reviewer string
		if err = tx.QueryRowContext(r.Context(), `SELECT actor FROM workpaper_reviews WHERE workpaper_id::text=$1 ORDER BY id DESC LIMIT 1`, paperID).Scan(&reviewer); err != nil {
			problem(w, 409, "A current accountant-approved workpaper is required.")
			return
		}
		if basis, e := preparation.BasisIssue(r.Context(), tx, closeID, kind, paperRaw); e != nil || basis != "" {
			problem(w, 409, "The client's accounting basis does not allow this entry. "+basis)
			return
		}
		if issue, e := independenceIssue(r.Context(), tx, closeID, paperID, reviewer); e != nil || issue != "" {
			problem(w, 409, "Posting needs an approval that meets the firm's independent-review policy. "+issue)
			return
		}
		// Under the close lock: no newer draft of the source arrived, and no other approved or unsettled Xero intent uses
		// these accounts, so two journals cannot both be posted from the same balances.
		if _, err = postingSource(r.Context(), tx, closeID, paperID, kind, paperRaw, plan.source); err != nil {
			refuse(w, err)
			return
		}
		accounts := []string{}
		for _, line := range plan.journal.JournalLines {
			accounts = append(accounts, line.AccountID)
		}
		var competing bool
		if err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM xero_postings p WHERE p.tenant_id=$1 AND p.id::text<>$2 AND (p.state IN ('executing','unknown') OR (p.state='approved' AND p.approval_expires_at>now())) AND EXISTS(SELECT 1 FROM jsonb_array_elements(p.journal->'JournalLines') l WHERE l->>'AccountID'=ANY($3)))`, plan.tenant, record.ID, accounts).Scan(&competing); err != nil || competing {
			problem(w, 409, "Another approved or unsettled Xero posting uses these accounts. Resolve it, then refresh this preview.")
			return
		}
		var current string
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM xero_posting_grants WHERE id::text=$1 AND revoked_at IS NULL AND expires_at>now() AND EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=xero_posting_grants.organization_id AND subject=xero_posting_grants.granted_by AND granted) FOR UPDATE`, plan.grant.ID).Scan(&current); err != nil {
			problem(w, 409, "Xero grant expired or was revoked.")
			return
		}
		if in.Action == "approve" {
			if in.Confirmation != "APPROVE XERO DRAFT" {
				problem(w, 422, "Type APPROVE XERO DRAFT.")
				return
			}
			result, e := tx.ExecContext(r.Context(), `UPDATE xero_postings SET state='approved',approved_by=$2,approval_expires_at=now()+interval '10 minutes' WHERE id::text=$1 AND state IN ('preview','approved')`, record.ID, a.Subject)
			if e != nil {
				problem(w, 503, "Approval could not be saved.")
				return
			}
			if n, _ := result.RowsAffected(); n != 1 || tx.Commit() != nil {
				problem(w, 409, "An attempt already exists or the approval could not be saved.")
				return
			}
			p, _ := readXeroPosting(r.Context(), db, closeID, paperID)
			jsonReply(w, 200, p)
			return
		}
		if in.Confirmation != "POST TO XERO" {
			problem(w, 422, "Type POST TO XERO.")
			return
		}
		var connection string
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM xero_connections WHERE id::text=$1 AND state='active' AND tenant_id=$2 AND organization_id=$3 AND client_identity(organization_id,client_entity)=client_identity($3,$4) FOR UPDATE`, plan.grant.ConnectionID, plan.grant.TenantID, org, entity).Scan(&connection); err != nil {
			problem(w, 409, "The Xero connection changed before this decision.")
			return
		}
		if elsewhere, e := postedElsewhere(r.Context(), tx, "production_postings", closeID, paperID, plan.sourceKey); e != nil || elsewhere {
			problem(w, 409, "This draft or its source was already posted to QuickBooks.")
			return
		}
		result, e := tx.ExecContext(r.Context(), `UPDATE xero_postings SET state='executing',attempted_by=$2,attempted_at=now() WHERE id::text=$1 AND state='approved' AND approval_expires_at>now() AND approved_by IS NOT NULL AND EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=$3 AND subject=xero_postings.approved_by AND granted)`, record.ID, a.Subject, org)
		if e != nil {
			problem(w, 503, "Attempt reservation failed; no provider call was made.")
			return
		}
		if n, _ := result.RowsAffected(); n != 1 {
			if approvalLapsed(record.State, record.Expires) {
				problemCode(w, 409, "APPROVAL_EXPIRED", lapsedMessage)
				return
			}
			problem(w, 409, "Approval expired or an attempt already exists.")
			return
		}
		if err = tx.Commit(); err != nil {
			problem(w, 503, "Decision save could not be confirmed; no provider call was made.")
			return
		}
		id, callErr := xero.PostManualJournal(r.Context(), plan.access, plan.tenant, record.RequestID, plan.journal)
		persist, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		defer cancel()
		message := ""
		if callErr != nil {
			message = callErr.Error()
		}
		if _, err = db.ExecContext(persist, `UPDATE xero_postings SET state='unknown',external_id=NULLIF($2,''),last_error=$3 WHERE id::text=$1 AND state='executing'`, record.ID, id, message); err != nil {
			problem(w, 503, "Attempt outcome could not be recorded. Never retry this write.")
			return
		}
		if callErr != nil {
			problemCode(w, 502, "EXECUTION_UNKNOWN", "Xero outcome unknown. Look for the journal in Xero and verify it here with its ID; do not repost.")
			return
		}
		verifyXero(w, r, db, plan.access, plan.tenant, record, id)
	})
}

// xeroGrantRead answers with the client entity's current Xero posting grant, or that it has none.
func xeroGrantRead(w http.ResponseWriter, r *http.Request, db *sql.DB, org, entity string) {
	g, err := scanXeroGrant(db.QueryRowContext(r.Context(), `SELECT `+xeroGrantColumns+` WHERE organization_id=$1 AND client_identity(organization_id,client_entity)=client_identity($1,$2) AND revoked_at IS NULL ORDER BY lower(client_entity)=lower($2) DESC,id DESC LIMIT 1`, org, entity))
	if err == sql.ErrNoRows {
		jsonReply(w, 200, map[string]string{"state": "not_granted"})
		return
	}
	if err != nil {
		problem(w, 503, "Grant unavailable.")
		return
	}
	jsonReply(w, 200, g)
}

// xeroGrantChange grants the client entity bounded Xero posting, or revokes its grant, as a member with posting:approve
// decides.
func xeroGrantChange(w http.ResponseWriter, r *http.Request, db *sql.DB, a *auth.Claims, entity string) {
	org := a.Organization
	var in struct {
		Action       string    `json:"action"`
		ConnectionID string    `json:"connection_id"`
		GrantID      string    `json:"grant_id"`
		AccountIDs   []string  `json:"account_ids"`
		Kinds        []string  `json:"kinds"`
		MaxAmount    string    `json:"max_amount"`
		Reason       string    `json:"reason"`
		Confirmation string    `json:"confirmation"`
		ExpiresAt    time.Time `json:"expires_at"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Action == "revoke" {
		if in.Confirmation != "REVOKE XERO POSTING" {
			problem(w, 422, "Type REVOKE XERO POSTING.")
			return
		}
		result, err := db.ExecContext(r.Context(), `UPDATE xero_posting_grants SET revoked_at=now(),revoked_by=$4 WHERE id::text=$1 AND organization_id=$2 AND lower(client_entity)=lower($3) AND revoked_at IS NULL`, in.GrantID, org, entity, a.Subject)
		if err != nil {
			problem(w, 503, "Grant revocation unavailable.")
			return
		}
		if n, _ := result.RowsAffected(); n != 1 {
			problem(w, 409, "Grant changed. Refresh before deciding.")
			return
		}
		jsonReply(w, 200, map[string]string{"state": "revoked"})
		return
	}
	amount, amountErr := prepaids.Amount(in.MaxAmount)
	valid := len(in.AccountIDs) >= 2 && len(in.AccountIDs) <= 20 && len(in.Kinds) > 0
	for i, id := range in.AccountIDs {
		valid = valid && xeroID.MatchString(id) && !slices.Contains(in.AccountIDs[:i], id)
	}
	for i, kind := range in.Kinds {
		valid = valid && (slices.Contains(postableKinds, kind) || kind == "bill_correction") && !slices.Contains(in.Kinds[:i], kind)
	}
	if in.Action != "grant" || in.Confirmation != "GRANT XERO POSTING" || !valid || amountErr != nil || amount <= 0 || in.ExpiresAt.Before(time.Now()) || in.ExpiresAt.After(time.Now().Add(30*24*time.Hour)) || len(strings.TrimSpace(in.Reason)) < 10 || len(in.Reason) > 2000 {
		problem(w, 422, "Confirm 2–20 distinct Xero account IDs, the draft kinds covered, a positive limit, expiry within 30 days and a reason of at least 10 characters.")
		return
	}
	var tenant string
	if err := db.QueryRowContext(r.Context(), `SELECT tenant_id FROM xero_connections WHERE id::text=$1 AND organization_id=$2 AND lower(client_entity)=lower($3) AND state='active'`, in.ConnectionID, org, entity).Scan(&tenant); err != nil || tenant == "" {
		problem(w, 409, "Confirm the client's Xero organisation first.")
		return
	}
	accounts, _ := json.Marshal(in.AccountIDs)
	kinds, _ := json.Marshal(in.Kinds)
	var id string
	if err := db.QueryRowContext(r.Context(), `INSERT INTO xero_posting_grants(organization_id,client_entity,connection_id,tenant_id,account_ids,kinds,max_amount,expires_at,reason,granted_by) VALUES($1,$2,$3,$4,ARRAY(SELECT jsonb_array_elements_text($5::jsonb)),ARRAY(SELECT jsonb_array_elements_text($6::jsonb)),$7,$8,$9,$10) ON CONFLICT DO NOTHING RETURNING id::text`, org, entity, in.ConnectionID, tenant, string(accounts), string(kinds), in.MaxAmount, in.ExpiresAt, strings.TrimSpace(in.Reason), a.Subject).Scan(&id); err != nil {
		problem(w, 409, "A current grant exists or the grant could not be saved. Refresh before deciding.")
		return
	}
	jsonReply(w, 200, map[string]string{"id": id, "state": "granted"})
}

// xeroBooksGrant is the client's Xero posting grant on the organisation month closeID's books were captured from
// (before any capture, the month's working connection), under any of the client's names: the proposal's source and its
// ledger effect are then the same organisation.
func xeroBooksGrant(ctx context.Context, db *sql.DB, org, entity, closeID string) (xeroGrant, error) {
	return scanXeroGrant(db.QueryRowContext(ctx, `SELECT `+xeroGrantColumns+` WHERE organization_id=$1 AND client_identity(organization_id,client_entity)=client_identity($1,$2) AND revoked_at IS NULL
 AND connection_id=COALESCE((SELECT s.connection_id FROM xero_snapshots s WHERE s.close_id::text=$3 ORDER BY s.captured_at DESC,s.id DESC LIMIT 1),
  (SELECT x.id FROM xero_connections x JOIN client_closes c ON c.id::text=$3 WHERE x.organization_id=$1 AND client_identity(x.organization_id,x.client_entity)=client_identity(c.organization_id,c.entity) AND x.state='active'
   ORDER BY lower(x.client_entity)=lower(c.entity) DESC,x.id DESC LIMIT 1)) ORDER BY id DESC LIMIT 1`, org, entity, closeID))
}
