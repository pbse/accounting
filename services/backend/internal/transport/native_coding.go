package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/quickbooks"
)

// nativeCodingCandidate reads an explicit account rule; it never chooses a treatment from a vendor name.
func nativeCodingCandidate(row preparation.LedgerRow, books *preparation.Ledger, policy preparation.Procedure, source preparation.Source, entity, period, currency string) (billCorrection, string) {
	hold := func(why string) (billCorrection, string) { return billCorrection{}, why }
	value := func(label string) string {
		v, ok := policy.InstructionValue(label)
		if !ok {
			return ""
		}
		return v
	}
	if policy.ID == "" || policy.Kind != "coding" || !policy.Enabled || policy.Method != "" || !strings.EqualFold(policy.ClientEntity, entity) || source.ID == "" || source.Digest == "" || value("Automatic native correction candidates") != "true" {
		return hold("apply this client's explicit approved native coding rule")
	}
	if currency == "" || value("Home currency") != currency || books == nil || books.Capture == "" || books.Source.ID == "" || books.ChartSource.ID == "" || books.ReportPeriod != period {
		return hold("current home-currency transaction and account captures are required")
	}
	company := value("QuickBooks company")
	if company == "" || company != books.Realm || company != books.CaptureRealm || company != books.ChartRealm {
		return hold("the approved rule must name the current QuickBooks company")
	}
	if _, supported := quickbooks.RecodeKinds[row.Kind]; !supported || row.Currency != currency {
		return hold("this is not a supported home-currency native transaction")
	}
	id, _, date, lines, err := quickbooks.ParseRecodable(row.Kind, row.Record, currency)
	if err != nil || id != row.ProviderID || date != row.Date || len(lines) != 1 {
		return hold("one supported captured line with an unchanged identity is required")
	}
	if parsed, e := time.Parse("2006-01-02", date); e != nil || parsed.Format("2006-01") != period {
		return hold("the captured transaction must belong to this close month")
	}
	var credit struct{ Credit bool }
	json.Unmarshal(row.Record, &credit)
	if credit.Credit {
		return hold("a credit needs a person")
	}
	rules, why := nativeCodingRules(row)
	if why != "" || len(rules) != 1 {
		return hold("the captured line must identify one approved counterparty")
	}
	prefix, party := rules[0].prefix, rules[0].party
	number, e := strconv.ParseUint(party, 10, 64)
	if e != nil || number == 0 || strconv.FormatUint(number, 10) != party {
		return hold("the captured counterparty ID must be explicit")
	}
	from, to := value(prefix+"source account"), value(prefix+"target account")
	if from == "" || from != lines[0].Account || to == "" || to == from {
		return hold("the captured line must match one explicit source and target account rule")
	}
	amount, e1 := prepaids.Amount(lines[0].Amount)
	limit, e2 := prepaids.Amount(value(prefix + "maximum line amount"))
	reference := value(prefix + "rule reference")
	if e1 != nil || e2 != nil || amount <= 0 || limit <= 0 || amount > limit || len(reference) < 5 || len(reference) > 200 {
		return hold("the line must stay within the approved amount limit and cited coding rule")
	}
	allowed := []string{"Expense", "Other Expense"}
	if row.Kind == "Deposit" {
		allowed = []string{"Income", "Other Income"}
	}
	var accounts []struct {
		ID, Name, AccountType string
		Active                bool
	}
	if json.Unmarshal(books.AccountCatalog, &accounts) != nil {
		return hold("current active account details are required")
	}
	for _, account := range []string{from, to} {
		matches := 0
		for _, a := range accounts {
			if a.ID == account {
				if !a.Active || a.Name == "" || a.Name != books.Accounts[account] || a.AccountType != books.Types[account] || !slices.Contains(allowed, a.AccountType) {
					return hold("source and target must be active accounts of the supported classification")
				}
				matches++
			}
		}
		if matches != 1 {
			return hold("source and target must be unique current accounts")
		}
	}
	candidate := billCorrection{PolicyID: policy.ID, PolicyEvidenceID: source.ID, AccountEvidenceID: books.ChartSource.ID, Entity: row.Kind, CaptureID: books.Capture, EvidenceID: books.Source.ID, Realm: company, BillID: id, Treatment: "inconsistent_coding", Reason: fmt.Sprintf("Proposed under approved coding procedure #%s; review the captured line and its exact target account.", policy.ID), RuleReference: reference, TargetAccountID: to, TargetLineID: lines[0].ID, TargetAccountName: books.Accounts[to], BeforeBill: row.Record, State: "candidate"}
	// Bind the decision to the transaction and rule; an unchanged capture refresh is not a new accounting decision.
	raw, _ := json.Marshal([]any{candidate.Entity, candidate.Realm, candidate.BillID, candidate.BeforeBill, candidate.TargetLineID, candidate.TargetAccountID, candidate.TargetAccountName, policy.ID, source.Digest, reference})
	candidate.Digest = preparation.Digest(raw)
	return candidate, ""
}

type nativeCodingLine struct{ prefix, party, account string }

// Parse each line's counterparty and account once for both eligibility and scope questions.
func nativeCodingRules(row preparation.LedgerRow) ([]nativeCodingLine, string) {
	var native struct {
		VendorRef struct{ Value string }
		EntityRef struct{ Value, Type string }
		Line      []struct {
			AccountBasedExpenseLineDetail struct{ AccountRef struct{ Value string } }
			DepositLineDetail             struct {
				AccountRef struct{ Value string }
				Entity     struct{ Value, Type string }
			}
		}
	}
	if json.Unmarshal(row.Record, &native) != nil {
		return nil, "the captured transaction is unreadable"
	}
	party, role := native.VendorRef.Value, "vendor"
	switch row.Kind {
	case "Bill":
	case "Purchase":
		if native.EntityRef.Type != "Vendor" {
			return nil, "the expense must identify an approved vendor"
		}
		party = native.EntityRef.Value
	case "Deposit":
		role = "customer"
	default:
		return nil, "this is not a supported native transaction"
	}
	rules := []nativeCodingLine{}
	for _, line := range native.Line {
		account := line.AccountBasedExpenseLineDetail.AccountRef.Value
		if row.Kind == "Deposit" {
			if line.DepositLineDetail.Entity.Type != "Customer" {
				continue
			}
			party, account = line.DepositLineDetail.Entity.Value, line.DepositLineDetail.AccountRef.Value
		}
		rules = append(rules, nativeCodingLine{row.Kind + " " + role + " " + party + " ", party, account})
	}
	return rules, ""
}

type nativeCodingAuthority struct {
	entity, period, currency string
	policy                   preparation.Procedure
	source                   preparation.Source
	books                    *preparation.Ledger
	active                   bool
	need                     string
}

func readNativeCodingAuthority(ctx context.Context, tx *sql.Tx, closeID string) (a nativeCodingAuthority, err error) {
	if err = tx.QueryRowContext(ctx, `SELECT entity,period FROM client_closes WHERE id::text=$1`, closeID).Scan(&a.entity, &a.period); err != nil {
		return
	}
	a.policy, err = preparation.CurrentProcedure(ctx, tx, closeID, "coding")
	if err == sql.ErrNoRows {
		err = nil
		return
	}
	if err != nil {
		return
	}
	opt, _ := a.policy.InstructionValue("Automatic native correction candidates")
	a.active = a.policy.Enabled && strings.EqualFold(a.policy.ClientEntity, a.entity) && opt == "true"
	if !a.active {
		return
	}
	a.source, err = preparation.ProcedureEvidence(ctx, tx, closeID, a.policy.ID)
	if err != nil {
		return
	}
	if a.source.ID == "" {
		a.need = "Apply the current approved client coding procedure to this close before preparing native corrections."
		return
	}
	a.books, err = preparation.CloseLedger(ctx, tx, closeID)
	if err != nil {
		return
	}
	profile, e := preparation.ClientProfile(ctx, tx, closeID)
	if e != nil {
		err = e
		return
	}
	a.currency = profile.Currency
	if profile.State != "detected" && profile.State != "confirmed" || a.currency == "" {
		a.need = "Confirm this client's home currency before preparing native corrections."
		return
	}
	company, _ := a.policy.InstructionValue("QuickBooks company")
	currency, _ := a.policy.InstructionValue("Home currency")
	if a.books == nil || a.books.Capture == "" || a.books.ChartSource.ID == "" || a.books.ReportPeriod != a.period {
		a.need = "Refresh current QuickBooks transactions and accounts for this close before preparing native corrections."
		return
	}
	if company == "" || company != a.books.Realm || company != a.books.CaptureRealm || company != a.books.ChartRealm || currency != a.currency {
		a.need = "Review the approved coding rule's QuickBooks company and home currency against this client's current captures."
	}
	return
}

func nativeCodingNeeds(ctx context.Context, tx *sql.Tx, closeID string) ([]string, error) {
	a, err := readNativeCodingAuthority(ctx, tx, closeID)
	if err != nil {
		return nil, err
	}
	if !a.active {
		return nil, nil
	}
	if a.need != "" {
		return []string{a.need}, nil
	}
	needs := []string{}
	total := 0
	for _, row := range a.books.Rows {
		rules, why := nativeCodingRules(row)
		if why != "" {
			continue
		}
		matched := false
		for _, line := range rules {
			from, ok := a.policy.InstructionValue(line.prefix + "source account")
			if ok && from != "" && from == line.account {
				matched = true
			}
		}
		if !matched {
			continue
		}
		if _, why = nativeCodingCandidate(row, a.books, a.policy, a.source, a.entity, a.period, a.currency); why != "" {
			total++
			if len(needs) < 20 {
				needs = append(needs, fmt.Sprintf("Review %s #%s in Books: %s. Resolve the source or approved rule; the goal will recheck it automatically.", row.Kind, row.ProviderID, why))
			}
		}
	}
	if total > len(needs) {
		needs = append(needs, fmt.Sprintf("%d additional native coding decisions need review in Books.", total-len(needs)))
	}
	return needs, nil
}

// prepareNativeCoding runs inside the authorized goal/close transaction, whose caller holds the close row.
func prepareNativeCoding(ctx context.Context, tx *sql.Tx, closeID, actor string) (int, error) {
	withdraw := func(kept []string) error {
		if kept == nil {
			kept = []string{}
		}
		_, err := tx.ExecContext(ctx, `UPDATE bill_corrections SET state='withdrawn',last_error='The current approved rule or capture no longer supports this automatic proposal.',candidate_reviews=candidate_reviews||jsonb_build_array(jsonb_build_object('action','withdrawn','actor',$3::text,'reason','Current approved rule or capture no longer supports this proposal','digest',digest,'capture_id',capture_id::text,'capture_evidence_id',evidence_id::text,'policy_version_id',policy_version_id::text,'policy_evidence_id',policy_evidence_id::text,'account_evidence_id',account_evidence_id::text,'at',now())) WHERE close_id::text=$1 AND state='candidate' AND NOT(digest=ANY($2::text[]))`, closeID, kept, actor)
		return err
	}
	a, err := readNativeCodingAuthority(ctx, tx, closeID)
	if err != nil {
		return 0, err
	}
	if !a.active || a.need != "" {
		return 0, withdraw(nil)
	}
	entity, period, policy, source, books := a.entity, a.period, a.policy, a.source, a.books
	rows, err := tx.QueryContext(ctx, `SELECT entity,bill_id,state,digest FROM bill_corrections WHERE close_id::text=$1 AND realm=$2`, closeID, books.Realm)
	if err != nil {
		return 0, err
	}
	existing := map[string]struct{ state, digest string }{}
	for rows.Next() {
		var kind, id, state, digest string
		if err = rows.Scan(&kind, &id, &state, &digest); err != nil {
			rows.Close()
			return 0, err
		}
		existing[kind+"/"+id] = struct{ state, digest string }{state, digest}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	prepared := 0
	kept := []string{}
	for _, row := range books.Rows {
		candidate, why := nativeCodingCandidate(row, books, policy, source, entity, period, a.currency)
		if why != "" {
			continue
		}
		old, found := existing[candidate.Entity+"/"+candidate.BillID]
		if found && old.state != "candidate" && old.state != "dismissed" && old.state != "withdrawn" {
			continue
		}
		if found && old.state == "dismissed" && old.digest == candidate.Digest {
			continue
		}
		kept = append(kept, candidate.Digest)
		history, _ := json.Marshal([]map[string]any{{"action": "prepared", "at": time.Now().UTC().Format(time.RFC3339Nano), "actor": actor, "digest": candidate.Digest, "policy_version_id": policy.ID, "policy_evidence_id": source.ID, "account_evidence_id": books.ChartSource.ID, "capture_evidence_id": books.Source.ID}})
		result, e := tx.ExecContext(ctx, `INSERT INTO bill_corrections(close_id,entity,capture_id,evidence_id,realm,bill_id,treatment,reason,rule_reference,target_account_id,target_line_id,target_account_name,before_bill,digest,state,proposed_by,policy_version_id,policy_evidence_id,account_evidence_id,candidate_reviews)
   VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'candidate',$15,$16,$17,$18,$19)
   ON CONFLICT(realm,entity,bill_id) DO UPDATE SET capture_id=EXCLUDED.capture_id,evidence_id=EXCLUDED.evidence_id,treatment=EXCLUDED.treatment,reason=EXCLUDED.reason,rule_reference=EXCLUDED.rule_reference,target_account_id=EXCLUDED.target_account_id,target_line_id=EXCLUDED.target_line_id,target_account_name=EXCLUDED.target_account_name,before_bill=EXCLUDED.before_bill,digest=EXCLUDED.digest,state='candidate',proposed_by=EXCLUDED.proposed_by,policy_version_id=EXCLUDED.policy_version_id,policy_evidence_id=EXCLUDED.policy_evidence_id,account_evidence_id=EXCLUDED.account_evidence_id,
   last_error='',candidate_reviews=CASE WHEN bill_corrections.digest<>EXCLUDED.digest OR bill_corrections.state='withdrawn' THEN bill_corrections.candidate_reviews||EXCLUDED.candidate_reviews ELSE bill_corrections.candidate_reviews END
   WHERE bill_corrections.close_id=EXCLUDED.close_id AND bill_corrections.attempted_at IS NULL AND (bill_corrections.state IN ('candidate','withdrawn') OR bill_corrections.state='dismissed' AND bill_corrections.digest<>EXCLUDED.digest)
   AND (bill_corrections.state='withdrawn' OR bill_corrections.digest<>EXCLUDED.digest OR bill_corrections.capture_id<>EXCLUDED.capture_id OR bill_corrections.evidence_id<>EXCLUDED.evidence_id OR bill_corrections.account_evidence_id IS DISTINCT FROM EXCLUDED.account_evidence_id OR bill_corrections.policy_evidence_id IS DISTINCT FROM EXCLUDED.policy_evidence_id)`, closeID, candidate.Entity, candidate.CaptureID, candidate.EvidenceID, candidate.Realm, candidate.BillID, candidate.Treatment, candidate.Reason, candidate.RuleReference, candidate.TargetAccountID, candidate.TargetLineID, candidate.TargetAccountName, string(candidate.BeforeBill), candidate.Digest, actor, candidate.PolicyID, candidate.PolicyEvidenceID, candidate.AccountEvidenceID, string(history))
		if e != nil {
			return 0, e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return 0, e
		}
		if n > 0 && (!found || old.digest != candidate.Digest || old.state == "withdrawn") {
			prepared++
		}
	}
	return prepared, withdraw(kept)
}

type nativeCodingReview struct {
	ID     string `json:"id"`
	Entity string `json:"entity"`
	BillID string `json:"transaction_id"`
	Target string `json:"target_account"`
	Rule   string `json:"rule_reference"`
}

func nativeCodingProgress(ctx context.Context, tx *sql.Tx, closeID string) ([]nativeCodingReview, int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id::text,entity,bill_id,target_account_name,rule_reference,count(*) OVER() FROM bill_corrections WHERE close_id::text=$1 AND state='candidate' ORDER BY id LIMIT 20`, closeID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []nativeCodingReview{}
	total := 0
	for rows.Next() {
		var item nativeCodingReview
		if err = rows.Scan(&item.ID, &item.Entity, &item.BillID, &item.Target, &item.Rule, &total); err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func registerNativeCandidateReview(mux *http.ServeMux, db *sql.DB, segment, entity string) {
	mux.HandleFunc("POST /v1/closes/{id}/quickbooks/"+segment+"/{bill}/candidate-review", func(w http.ResponseWriter, r *http.Request) {
		actor, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var in struct {
			Digest    string `json:"digest"`
			Reason    string `json:"reason"`
			Confirmed bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Reason = strings.TrimSpace(in.Reason)
		if !in.Confirmed || in.Digest == "" || len(in.Reason) < 5 || len(in.Reason) > 2000 {
			problem(w, 422, "Confirm why this coding candidate does not apply.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Candidate review unavailable.")
			return
		}
		defer tx.Rollback()
		var closeID string
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM client_closes WHERE id::text=$1 AND organization_id=$2 FOR UPDATE`, r.PathValue("id"), organization(r)).Scan(&closeID); err != nil {
			problem(w, 404, "Close unavailable.")
			return
		}
		signed, err := signedOff(r.Context(), tx, closeID)
		if err != nil || signed {
			problem(w, 409, "Reopen this close before changing a candidate review.")
			return
		}
		item, err := readBillCorrection(r.Context(), tx, closeID, entity, r.PathValue("bill"))
		if err != nil || item.State != "candidate" || item.Digest != in.Digest {
			problem(w, 409, "Candidate changed. Refresh its exact source and rule.")
			return
		}
		result, err := tx.ExecContext(r.Context(), `UPDATE bill_corrections SET state='dismissed',candidate_reviews=candidate_reviews||jsonb_build_array(jsonb_build_object('action','dismissed','actor',$3::text,'reason',$4::text,'digest',digest,'at',now())) WHERE id::text=$1 AND digest=$2 AND state='candidate' AND attempted_at IS NULL`, item.ID, in.Digest, actor, in.Reason)
		if err != nil {
			problem(w, 503, "Could not record the candidate review.")
			return
		}
		n, err := result.RowsAffected()
		if err != nil || n != 1 {
			problem(w, 409, "Candidate changed. Refresh before reviewing it.")
			return
		}
		item, err = readBillCorrection(r.Context(), tx, closeID, entity, r.PathValue("bill"))
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "Refresh the saved candidate review.")
			return
		}
		jsonReply(w, 200, item)
	})
}
