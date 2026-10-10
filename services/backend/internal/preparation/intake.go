package preparation

import (
	"close.local/backend/internal/llm"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/reconciliation"
)

type Candidate struct {
	Value    string `json:"value"`
	SourceID string `json:"source_id"`
	Location string `json:"location"`
	Quote    string `json:"quote"`
}
type Fact struct {
	Optional   bool        `json:"optional,omitempty"`
	Field      string      `json:"field"`
	Label      string      `json:"label"`
	Value      string      `json:"value"`
	Candidates []Candidate `json:"candidates"`
	Question   string      `json:"question"`
}
type Proposal struct {
	Kind            string   `json:"kind,omitempty"`
	Engine          string   `json:"engine,omitempty"`
	Summary         string   `json:"summary,omitempty"`
	Questions       []string `json:"questions,omitempty"`
	InventoryDigest string   `json:"inventory_digest,omitempty"`
	Model           string   `json:"model,omitempty"`
	ResponseID      string   `json:"response_id,omitempty"`
	InputTokens     int      `json:"input_tokens,omitempty"`
	OutputTokens    int      `json:"output_tokens,omitempty"`
	// EntityName is the one other client name an identity-blocked source gives, for confirming as an alias.
	EntityName string   `json:"entity_name,omitempty"`
	Facts      []Fact   `json:"facts"`
	Sources    []Source `json:"sources"`
	Unselected []Source `json:"unselected,omitempty"`
	// Lines are the rows of a prior accountant's schedule, read on the client's opening month.
	Lines []ScheduleLine `json:"lines,omitempty"`
}

// ScheduleLine is one row of a prior accountant's schedule: the row as quoted, its facts and, once decided, why it was
// set aside rather than imported.
type ScheduleLine struct {
	Line     int    `json:"line"`
	Row      string `json:"row"`
	Facts    []Fact `json:"facts"`
	SetAside string `json:"set_aside,omitempty"`
}
type IntakeSource struct {
	Source
	Content string
}

// ponytail: local proposals recognize exact key:value labels only; semantic
// invoice/reply interpretation belongs to the explicitly configured AI provider.
// Nothing in a document can confirm policy, identity, or authorize execution.
// IdentityFacts name who issued a prepaid invoice, who it bills and its number, as printed. They are not calculator inputs: the
// straight-through check compares them with the client and the captured bill, and the confirmation form leaves them out.
var IdentityFacts = []string{"vendor", "billed_to", "invoice_number"}

func Propose(sources []IntakeSource) Proposal {
	p := Proposal{Kind: "prepaid", Engine: "local", Facts: []Fact{}, Sources: []Source{}}
	for _, s := range sources {
		p.Sources = append(p.Sources, s.Source)
	}
	definitions := []struct{ field, label, question string }{
		{"amount", "Invoice amount", "What is the invoice amount in the client's home currency, excluding any unsupported credits?"},
		{"service_start", "Service start", "What is the service start date (YYYY-MM-DD)?"},
		{"service_end", "Service end", "What is the service end date (YYYY-MM-DD)?"},
		{"currency", "Currency", "Which currency is the invoice in? This method supports the client's home currency only."},
		{"expense_account", "Expense account", "Which expense account contains the original invoice booking?"},
		{"prepaid_account", "Prepaid account", "Which prepaid asset account should receive the remaining balance?"},
	}
	for _, field := range append([]string{"credit_amount", "credit_period", "credit_reference", "booked_balance", "booking_reference", "cancellation_period", "cancellation_reference"}, IdentityFacts...) {
		definitions = append(definitions, struct{ field, label, question string }{field, strings.ReplaceAll(field, "_", " "), ""})
	}
	for _, source := range sources {
		if source.Role == "invoice" && strings.Contains("\n"+source.Content+"\n", "\nPreparation kind: accrual\n") {
			p.Kind = "accrual"
		}
	}
	if p.Kind == "accrual" {
		definitions = nil
		for _, field := range []string{"amount", "service_start", "service_end", "currency", "expense_account", "liability_account", "booked_balance", "booking_reference", "estimation_basis", "estimate_amount", "estimate_reference", "actual_reference"} {
			label := strings.ReplaceAll(field, "_", " ")
			question := "Confirm " + label + " from incurred-service and ledger evidence."
			if field == "estimate_amount" || field == "estimate_reference" || field == "actual_reference" {
				question = ""
			}
			definitions = append(definitions, struct{ field, label, question string }{field, label, question})
		}
	}
	for _, def := range definitions {
		f := Fact{Optional: def.question == "", Field: def.field, Label: def.label, Candidates: []Candidate{}}
		values := map[string]bool{}
		for _, s := range sources {
			for i, line := range strings.Split(s.Content, "\n") {
				key, value, ok := strings.Cut(line, ":")
				key = strings.ToLower(strings.Join(strings.Fields(strings.ReplaceAll(key, "_", " ")), " "))
				value = strings.TrimSpace(value)
				if ok && (key == strings.ReplaceAll(def.field, "_", " ") || key == strings.ToLower(def.label)) && len(value) > 0 && len(value) <= 200 {
					// Bounded candidates, but every value participates in conflict detection.
					values[value] = true
					if len(f.Candidates) < 10 {
						f.Candidates = append(f.Candidates, Candidate{value, s.ID, fmt.Sprintf("line %d", i+1), strings.TrimSpace(line)})
					}
				}
			}
		}
		if len(values) == 1 {
			for v := range values {
				f.Value = v
			}
		}
		if f.Value == "" && (!f.Optional || len(values) > 1) {
			f.Question = def.question
			if len(values) > 1 {
				f.Question = "Conflicting source values. Confirm " + def.label + " against the cited records. " + f.Question
			}
		}
		p.Facts = append(p.Facts, f)
	}
	return p
}

// RecognizeExport identifies a known structured export without a model. The file
// selects the work type only; it cannot confirm policy or authorize execution.
// The agent pairs and reads id,date,amount CSVs itself, so only the local path asks for them.
func RecognizeExport(sources []IntakeSource, period string, reconciliationCSV bool) (Proposal, bool) {
	for _, s := range sources {
		if s.Role != "invoice" {
			continue
		}
		kind, failed := "settlement", "This Stripe payout export cannot be traced yet."
		summary, ok, err := reconciliation.StripeReportSummary(s.Content, period)
		if !ok && reconciliationCSV {
			kind, failed = "reconciliation", "This reconciliation CSV cannot be reconciled yet."
			summary, ok, err = reconciliation.CSVSummary(s.Content, period)
		}
		if !ok {
			return Proposal{}, false
		}
		p := Proposal{Kind: kind, Engine: "recognized_export", Summary: summary, Facts: []Fact{}, Sources: []Source{}}
		for _, source := range sources {
			p.Sources = append(p.Sources, source.Source)
		}
		if err != nil {
			p.Kind, p.Summary, p.Questions = "unsupported", failed, []string{err.Error()}
		}
		return p, true
	}
	return Proposal{}, false
}

// StartIntake is in the upload transaction, so an accepted file cannot lose its job.
func StartIntake(ctx context.Context, tx *sql.Tx, closeID, sourceID, actor string) (string, error) {
	for _, kind := range []string{"prepaid", "reconciliation", "accrual"} {
		p, err := CurrentProcedure(ctx, tx, closeID, kind)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err = AttachProcedure(ctx, tx, closeID, p, false, actor); err != nil {
			return "", err
		}
	}
	var id string
	err := tx.QueryRowContext(ctx, `INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by,agent) VALUES($1,$2,'[]','waiting_evidence',$3,NULLIF($4,'')) ON CONFLICT(close_id,digest) DO UPDATE SET digest=preparation_jobs.digest RETURNING id::text`, closeID, "intake:"+sourceID, actor, auth.Agent(ctx)).Scan(&id)
	if err == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO preparation_intakes(job_id,source_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, sourceID)
	}
	return id, err
}

func IntakeSources(ctx context.Context, tx *sql.Tx, jobID string) ([]IntakeSource, error) {
	rows, err := tx.QueryContext(ctx, `SELECT e.id::text,e.digest,e.name,e.content,CASE WHEN e.id=i.source_id THEN 'invoice' WHEN e.procedure_id IS NOT NULL THEN 'support' ELSE 'client_reply' END FROM preparation_intakes i JOIN preparation_jobs j ON j.id=i.job_id JOIN evidence e ON e.close_id=j.close_id AND (e.id=i.source_id OR e.procedure_id IS NOT NULL OR e.id IN (SELECT r.source_id FROM work_replies r JOIN request_jobs q ON q.request_id=r.request_id WHERE q.job_id=j.id)) WHERE i.job_id=$1 AND e.superseded_by IS NULL AND e.extraction_state='ready' ORDER BY e.id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sources := []IntakeSource{}
	for rows.Next() {
		var s IntakeSource
		if err = rows.Scan(&s.ID, &s.Digest, &s.Name, &s.Content, &s.Role); err != nil {
			return nil, err
		}
		s.Location = "Full extracted text"
		sources = append(sources, s)
	}
	return sources, rows.Err()
}

func ProcessIntake(ctx context.Context, db *sql.DB) (bool, error) {
	if llm.Model(ctx, "preparation") != "" {
		return ProcessAgent(ctx, db)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var id, closeID, entity, period string
	err = tx.QueryRowContext(ctx, `SELECT j.id::text,j.close_id::text FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id JOIN evidence e ON e.id=i.source_id WHERE j.state='waiting_evidence' AND (e.extraction_state IN ('ready','failed') OR e.superseded_by IS NOT NULL) AND j.close_id IN (SELECT c.id FROM client_closes c JOIN organizations o ON o.id=c.organization_id WHERE o.enabled AND c.organization_id=COALESCE($1,c.organization_id)) ORDER BY j.id FOR UPDATE OF j SKIP LOCKED LIMIT 1`, OrganizationScope(ctx)).Scan(&id, &closeID)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT id::text,entity,period FROM client_closes WHERE id=$1 FOR UPDATE`, closeID).Scan(&closeID, &entity, &period); err != nil {
		return false, err
	}
	var ready bool
	if err = tx.QueryRowContext(ctx, `SELECT e.extraction_state='ready' AND e.superseded_by IS NULL FROM preparation_intakes i JOIN evidence e ON e.id=i.source_id WHERE i.job_id=$1`, id).Scan(&ready); err != nil {
		return false, err
	}
	state, message := "needs_confirmation", ""
	if !ready {
		state, message = "failed", "Source extraction failed or evidence was replaced. Inspect the evidence before retrying."
	}
	sources, err := IntakeSources(ctx, tx, id)
	if err != nil {
		return false, err
	}
	aliases, err := ClientAliases(ctx, tx, closeID)
	if err != nil {
		return false, err
	}
	proposed, recognized := RecognizeExport(sources, period, true)
	if !recognized {
		proposed = Propose(sources)
	}
	for _, source := range sources {
		if source.Role == "invoice" {
			if blocked, ok := blockedIdentity(source.ID, entity, aliases, sources); ok {
				proposed = blocked
			}
			break
		}
	}
	proposal, _ := json.Marshal(proposed)
	if _, err = tx.ExecContext(ctx, `UPDATE preparation_intakes SET proposal=$2,revision=revision+1 WHERE job_id=$1`, id, string(proposal)); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE preparation_jobs SET state=$2,error=$3 WHERE id=$1`, id, state, message); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

type scopeKey struct{}

// WithinOrganization limits the work a queue step takes to one firm's; without it a step takes the next job of any
// enabled firm. The demonstration seed works its own firm's queue this way.
func WithinOrganization(ctx context.Context, organization string) context.Context {
	return context.WithValue(ctx, scopeKey{}, organization)
}

// OrganizationScope is the firm a step is limited to, or NULL for every firm.
func OrganizationScope(ctx context.Context) sql.NullString {
	org, _ := ctx.Value(scopeKey{}).(string)
	return sql.NullString{String: org, Valid: org != ""}
}
