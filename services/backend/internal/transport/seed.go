package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/documents"
	"close.local/backend/internal/llm"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/quickbooks"
	"close.local/backend/internal/seed"
	"github.com/golang-jwt/jwt/v5"
)

// The demonstration seed (docs/seed-data.md) fills a firm with the clients, months, documents and decisions of
// internal/seed, through the same routes a person uses and the capture path the acceptance run uses. It is a story
// about one firm, so it runs once per firm; Remove takes it out again by client name.

// SeedOptions name the firm and the people the seed works as.
type SeedOptions struct {
	// Organization is the firm; Subject the administrator every close is worked as and most closes are owned by.
	Organization, Subject string
	// Members are the firm's other members, who own some clients' closes; their subjects must already be members.
	Members []string
	// FirmName names a firm the seed has to create (a local database). An existing firm keeps its name. Hosted says the
	// database is not on this machine: the firm must exist already, and the seed creates nothing but its clients.
	FirmName string
	Hosted   bool
	// Log receives one line per step.
	Log func(format string, args ...any)
}

// firmAdminPermissions is what the firm-admin role holds (docs/mcp.md).
var firmAdminPermissions = []string{"clients:all", "workpapers:review", "posting:approve", "posting:execute", "members:manage", "org:manage", "requests:send", "integrations:manage", "close:signoff", "procedures:manage", "data:manage", "org:enroll"}

type seeder struct {
	db      *sql.DB
	mux     http.Handler
	o       SeedOptions
	firm    seed.Firm
	answers sync.Map // evidence ID → recorded proposal JSON
	// closeOf is each document's close; captures each close's capture evidence (the snapshot and the transaction
	// index), which a recorded answer names as "$ledger" and "$index".
	closeOf  sync.Map
	captures sync.Map
	// filesOf is each close's documents by file name, so a recorded answer can name another document as "$source:<file>".
	filesOf sync.Map
	model   *llm.Client
	// problems collects every refused step, so one client's trouble does not stop the others.
	problems []string
	today    time.Time
	// The manifest: what the seed added that no client name identifies, recorded for removal.
	createdFirm                                                       bool
	policyBefore                                                      []byte
	chatSessions, invitations, roleChanges, agentAccess, procedureIDs []int64
	attentionKeys                                                     []string
	modelCallDays                                                     []string
}

func (s *seeder) log(format string, args ...any) {
	if s.o.Log != nil {
		s.o.Log(format, args...)
	}
}

func (s *seeder) problem(format string, args ...any) {
	s.problems = append(s.problems, fmt.Sprintf(format, args...))
	s.log("  ! "+format, args...)
}

// actor is the context of a request made as one of the firm's people.
func (s *seeder) actor(ctx context.Context, subject string) context.Context {
	return auth.WithActor(ctx, &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: s.o.Organization, Permissions: firmAdminPermissions})
}

// call makes one request in-process as the administrator and returns the status and body.
func (s *seeder) call(ctx context.Context, method, path string, body any) (int, []byte) {
	return s.callAs(ctx, s.o.Subject, method, path, body)
}

func (s *seeder) callAs(ctx context.Context, subject, method, path string, body any) (int, []byte) {
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	r := httptest.NewRequestWithContext(s.actor(ctx, subject), method, path, reader)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, r)
	if os.Getenv("SEED_TRACE") != "" {
		s.log("    %s %s → %d %.300s", method, path, w.Code, w.Body.String())
	}
	return w.Code, w.Body.Bytes()
}

// must makes a request that has to succeed; a refusal is recorded as a problem and returns false.
func (s *seeder) must(ctx context.Context, what, method, path string, body any) ([]byte, bool) {
	code, out := s.call(ctx, method, path, body)
	if code < 200 || code > 299 {
		s.problem("%s: %s %s → %d %s", what, method, path, code, strings.TrimSpace(string(out)))
		return out, false
	}
	return out, true
}

func field(raw []byte, path ...string) any {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	for _, p := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[p]
	}
	return v
}

func str(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	return ""
}

// recordedModel answers each reading with the proposal recorded for the document being read, as the acceptance run's
// recorded client does; a reading of a document with no answer is refused and the pipeline records a failed reading.
func (s *seeder) recordedModel() *llm.Client {
	reply := func(status int, body any) *http.Response {
		raw, _ := json.Marshal(body)
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(raw))}
	}
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		var request struct {
			Input []struct {
				Content string `json:"content"`
			} `json:"input"`
			Text struct {
				Format struct {
					Name string `json:"name"`
				} `json:"format"`
			} `json:"text"`
		}
		json.Unmarshal(raw, &request)
		var given struct {
			Primary string `json:"primary_source_id"`
		}
		for _, item := range request.Input {
			json.Unmarshal([]byte(item.Content), &given)
		}
		answer, ok := s.answers.Load(given.Primary)
		if !ok {
			return reply(400, map[string]any{"error": map[string]string{"message": "no recorded answer for evidence " + given.Primary}}), nil
		}
		text := strings.ReplaceAll(answer.(string), `"$primary"`, strconv.Quote(given.Primary))
		if closeID, ok := s.closeOf.Load(given.Primary); ok {
			if c, ok := s.captures.Load(closeID); ok {
				capture := c.([2]string)
				text = strings.ReplaceAll(text, `"$ledger"`, strconv.Quote(capture[0]))
				text = strings.ReplaceAll(text, `"$index"`, strconv.Quote(capture[1]))
			}
			if f, ok := s.filesOf.Load(closeID); ok {
				for file, id := range f.(map[string]string) {
					text = strings.ReplaceAll(text, `"$source:`+file+`"`, strconv.Quote(id))
				}
			}
		}
		// A large close asks which evidence to read first: the recorded reading's own sources are the selection.
		if request.Text.Format.Name == "evidence_selection" {
			var recorded struct {
				Sources []struct {
					ID string `json:"id"`
				} `json:"sources"`
			}
			json.Unmarshal([]byte(text), &recorded)
			ids := []string{}
			for _, source := range recorded.Sources {
				if !strings.HasPrefix(source.ID, "$") {
					ids = append(ids, source.ID)
				}
			}
			if !slices.Contains(ids, given.Primary) {
				ids = append([]string{given.Primary}, ids...)
			}
			selected, _ := json.Marshal(map[string]any{"selected_ids": ids})
			text = string(selected)
		}
		return reply(200, map[string]any{"id": "seed-" + given.Primary, "status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": text}}}}}), nil
	})
	client, err := llm.New(llm.Config{APIKey: "seed-recorded", Model: "recorded", HTTP: &http.Client{Transport: rt}})
	if err != nil {
		panic(err)
	}
	return client
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// answerFor is the recorded proposal for a document: the work's answer with its sources and empty lines.
func answerFor(w seed.Work) string {
	sources := map[string][]map[string]string{
		"prepaid":        {{"id": "$primary", "role": "invoice"}},
		"accrual":        {{"id": "$primary", "role": "invoice"}},
		"loan":           {{"id": "$primary", "role": "statement"}, {"id": "$ledger", "role": "ledger"}, {"id": "$index", "role": "support"}},
		"payroll":        {{"id": "$primary", "role": "support"}, {"id": "$ledger", "role": "ledger"}},
		"owner":          {{"id": "$primary", "role": "support"}, {"id": "$ledger", "role": "ledger"}},
		"reconciliation": {{"id": "$primary", "role": "support"}},
		"unsupported":    {{"id": "$primary", "role": "support"}},
	}[w.Kind]
	if w.Sources != nil {
		sources = w.Sources
	}
	answer := map[string]any{"lines": []any{}, "sources": sources}
	for k, v := range w.Answer {
		answer[k] = v
	}
	raw, _ := json.Marshal(answer)
	return string(raw)
}

// settle works the queue as the worker does, one step of each kind at a time, until a round finds nothing to do.
func (s *seeder) settle(ctx context.Context) error {
	// Only the demonstration's own closes are worked: any other job, this firm's or another's, is the worker's.
	var closes []int64
	if err := s.db.QueryRowContext(ctx, `SELECT close_ids FROM demonstration_seeds WHERE organization_id=$1`, s.o.Organization).Scan(&closes); err != nil {
		return fmt.Errorf("the seed's closes could not be read: %w", err)
	}
	if len(closes) == 0 {
		return nil
	}
	ctx = documents.WithinCloses(preparation.WithinCloses(ctx, closes), closes)
	reading := llm.WithClient(ctx, s.model)
	for round := 0; ; round++ {
		if round == 500 {
			return errors.New("the queue did not settle in 500 rounds")
		}
		extracted, e1 := documents.ProcessOne(ctx, s.db)
		read, e2 := preparation.ProcessIntake(reading, s.db)
		prepared, e3 := preparation.ProcessOne(ctx, s.db)
		if err := errors.Join(e1, e2, e3); err != nil {
			return err
		}
		if !extracted && !read && !prepared {
			return nil
		}
	}
}

// SeedFirm writes the demonstration firm into db. The firm row is created when missing; its administrator and members
// are granted membership when they lack it.
func SeedFirm(ctx context.Context, db *sql.DB, firm seed.Firm, o SeedOptions) ([]string, error) {
	if o.Organization == "" || o.Subject == "" {
		return nil, errors.New("the seed needs the firm's organization ID and its administrator's subject")
	}
	s := &seeder{db: db, mux: savedCloses(db, strings.Repeat("s", 32)), o: o, firm: firm, today: time.Now().UTC()}
	s.model = s.recordedModel()
	if err := s.firmRow(ctx); err != nil {
		return nil, err
	}
	if err := s.isolated(ctx); err != nil {
		return nil, err
	}
	if err := s.members(ctx); err != nil {
		return nil, err
	}
	if !s.createdFirm && s.o.Organization != "local-development" {
		// The seed acts with the administrator's authority, so the firm's own records must say the person has it.
		admin, err := s.administrator(ctx, s.o.Subject)
		if err != nil {
			return nil, err
		}
		if !admin {
			return nil, fmt.Errorf("%s is not recorded as an administrator of this firm (its creator, or given the firm-admin role); the seed acts with that authority only", s.o.Subject)
		}
	}
	if err := s.db.QueryRowContext(ctx, `SELECT to_jsonb(o)-'id'-'name'-'created_at' FROM (SELECT id,name,created_at,require_independent_review,require_link_passcode,COALESCE(independent_review_min_amount::text,'') AS independent_review_min_amount,COALESCE(materiality_amount::text,'') AS materiality_amount,reminder_after_days,reminder_max,agent_chat,agent_chat_stopped,assistant_model_answers,allow_ledger_writes FROM organizations WHERE id=$1) o`, s.o.Organization).Scan(&s.policyBefore); err != nil {
		return nil, fmt.Errorf("the firm's policy could not be read: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO demonstration_seeds(organization_id,subject,members,created_firm,policy) VALUES($1,$2,COALESCE($3::text[],'{}'),$4,$5)`, s.o.Organization, s.o.Subject, s.o.Members, s.createdFirm, string(s.policyBefore)); err != nil {
		return nil, fmt.Errorf("the seed's record could not be written (is migration 140 applied?): %w", err)
	}
	if err := s.people(ctx); err != nil {
		return nil, err
	}
	s.policies(ctx)
	s.roster(ctx)
	s.procedures(ctx)
	for i := range firm.Clients {
		c := &firm.Clients[i]
		if c.Scenario == seed.NotOpened {
			continue
		}
		if err := s.client(ctx, c, i); err != nil {
			return s.problems, fmt.Errorf("%s: %w", c.Name, err)
		}
	}
	s.afterwards(ctx)
	if err := s.manifest(ctx); err != nil {
		return s.problems, err
	}
	return s.problems, nil
}

func (s *seeder) firmRow(ctx context.Context) error {
	name := s.o.FirmName
	if name == "" {
		name = s.firm.Name
	}
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM organizations WHERE id=$1)`, s.o.Organization).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	if s.o.Hosted {
		return fmt.Errorf("no firm %s exists; a hosted firm is never created by the seed", s.o.Organization)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO organizations(id,name) VALUES($1,$2)`, s.o.Organization, name); err != nil {
		return err
	}
	s.createdFirm = true
	return nil
}

// administrator says whether the firm's own records make the subject one of its administrators now: their latest
// settled role change decides when there is one; otherwise the person who created the firm or arrived first, or
// someone enrolled by a firm-admin invitation.
func (s *seeder) administrator(ctx context.Context, subject string) (bool, error) {
	var latest sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT to_role FROM member_role_changes WHERE organization_id=$1 AND subject=$2 AND state NOT IN ('pending','failed','unknown') ORDER BY id DESC LIMIT 1`, s.o.Organization, subject).Scan(&latest); err != nil && err != sql.ErrNoRows {
		return false, err
	}
	if latest.Valid {
		return latest.String == "firm-admin", nil
	}
	var admin bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM organization_member_access WHERE organization_id=$1 AND subject=$2 AND actor=$2 AND (reason LIKE 'Created the firm%' OR reason LIKE 'First member of the firm%'))
 OR EXISTS(SELECT 1 FROM member_invitations WHERE organization_id=$1 AND subject=$2 AND role_slug='firm-admin' AND state='accepted')`, s.o.Organization, subject).Scan(&admin)
	return admin, err
}

// isolated refuses a firm that already holds the demonstration, or any client the demonstration would add: the seed
// is one story, told once, and removal must not touch work that was there before it.
func (s *seeder) isolated(ctx context.Context) error {
	var seeded bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM demonstration_seeds WHERE organization_id=$1)`, s.o.Organization).Scan(&seeded); err != nil {
		return fmt.Errorf("the seed's record could not be read (is migration 140 applied?): %w", err)
	}
	if seeded {
		return errors.New("this firm already holds the demonstration; remove it first (-remove)")
	}
	var independent bool
	s.db.QueryRowContext(ctx, `SELECT require_independent_review OR independent_review_min_amount IS NOT NULL FROM organizations WHERE id=$1`, s.o.Organization).Scan(&independent)
	if independent {
		return errors.New("the firm requires an independent reviewer; the seed records every decision as the administrator, so it cannot run here")
	}
	names := s.clientNames()
	var taken []string
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT entity FROM (SELECT entity FROM client_closes WHERE organization_id=$1 AND lower(entity)=ANY($2)
 UNION SELECT client_entity FROM client_roster WHERE organization_id=$1 AND lower(client_entity)=ANY($2)
 UNION SELECT client_entity FROM client_aliases WHERE organization_id=$1 AND retired_at IS NULL AND (lower(client_entity)=ANY($2) OR alias IN (SELECT trim(regexp_replace(n,'[^[:alnum:]]+',' ','g')) FROM unnest($2::text[]) n))
 UNION SELECT client_entity FROM budget_versions WHERE organization_id=$1 AND lower(client_entity)=ANY($2)
 UNION SELECT client_entity FROM cash_obligations WHERE organization_id=$1 AND lower(client_entity)=ANY($2)
 UNION SELECT client_entity FROM cash_scenarios WHERE organization_id=$1 AND lower(client_entity)=ANY($2)
 UNION SELECT client_entity FROM open_item_actions WHERE organization_id=$1 AND lower(client_entity)=ANY($2)
 UNION SELECT client_entity FROM collection_contact_preferences WHERE organization_id=$1 AND lower(client_entity)=ANY($2)
 UNION SELECT client_entity FROM customer_contact_authority WHERE organization_id=$1 AND lower(client_entity)=ANY($2)
 UNION SELECT client_entity FROM close_routines WHERE organization_id=$1 AND lower(client_entity)=ANY($2)
 UNION SELECT client_entity FROM quickbooks_connections WHERE organization_id=$1 AND lower(client_entity)=ANY($2)
 UNION SELECT client_entity FROM client_access WHERE organization_id=$1 AND lower(client_entity)=ANY($2)
 UNION SELECT client_entity FROM procedure_versions WHERE organization_id=$1 AND lower(client_entity)=ANY($2)) t ORDER BY 1`, s.o.Organization, names)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		rows.Scan(&name)
		taken = append(taken, name)
	}
	if len(taken) > 0 {
		return fmt.Errorf("the firm already has clients named %s; the demonstration's clients cannot share a name with real ones", strings.Join(taken, ", "))
	}
	return nil
}

func (s *seeder) clientNames() []string {
	names := []string{}
	for _, c := range s.firm.Clients {
		names = append(names, strings.ToLower(c.Name))
	}
	return names
}

// record adds one ID or key to the seed's record as soon as the row exists, so a run that stops can still be removed.
func (s *seeder) record(ctx context.Context, column string, value any) {
	s.db.ExecContext(ctx, fmt.Sprintf(`UPDATE demonstration_seeds SET %s=array_append(%s,$2) WHERE organization_id=$1`, column, column), s.o.Organization, value)
}

// recordRow records a client-scoped row the seed made, by its table and ID, for removal.
func (s *seeder) recordRow(ctx context.Context, table string, id string) {
	if id == "" {
		return
	}
	s.db.ExecContext(ctx, `UPDATE demonstration_seeds SET policy=policy||jsonb_build_object('rows',COALESCE(policy->'rows','{}'::jsonb)||jsonb_build_object($2::text,COALESCE(policy->'rows'->$2::text,'[]'::jsonb)||to_jsonb($3::text))) WHERE organization_id=$1`, s.o.Organization, table, id)
}

// recordNewest records the row a route just made for a client: the newest of that client's in the table.
func (s *seeder) recordNewest(ctx context.Context, table, entity string) {
	var id string
	s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT max(id)::text FROM %s WHERE organization_id=$1 AND lower(client_entity)=lower($2)`, table), s.o.Organization, entity).Scan(&id)
	s.recordRow(ctx, table, id)
}

// manifest marks the run finished; everything removal needs was recorded as it was made.
func (s *seeder) manifest(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE demonstration_seeds SET finished_at=now() WHERE organization_id=$1`, s.o.Organization)
	return err
}

// people checks that the administrator and the members are current members of the firm (a firm the seed created
// gets its memberships from the seed), and gives them their own settings.
// members checks that the administrator and the members are current members of the firm; a firm the seed created
// gets its memberships from the seed.
func (s *seeder) members(ctx context.Context) error {
	for _, subject := range append([]string{s.o.Subject}, s.o.Members...) {
		var granted sql.NullBool
		s.db.QueryRowContext(ctx, `SELECT granted FROM current_organization_members WHERE organization_id=$1 AND subject=$2`, s.o.Organization, subject).Scan(&granted)
		if s.o.Organization == "local-development" {
			continue // local development shares one identity, so everyone is in
		}
		if granted.Valid && granted.Bool {
			// A teammate who will own closes must be able to work them: an administrator by the firm's own records.
			if subject != s.o.Subject {
				if admin, err := s.administrator(ctx, subject); err != nil {
					return err
				} else if !admin {
					return fmt.Errorf("%s is not recorded as an administrator of this firm, so the seed cannot give them closes to work", subject)
				}
			}
			continue
		}
		if !s.createdFirm {
			return fmt.Errorf("%s is not a current member of the firm; the seed does not change who may enter it", subject)
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,$2,true,'Joined the firm (demonstration seed)',$3)`, s.o.Organization, subject, s.o.Subject); err != nil {
			return err
		}
	}
	return nil
}

// people gives the administrator their own settings where they have none, the invitations a growing firm has, and
// the administrator's agent access.
func (s *seeder) people(ctx context.Context) error {
	// The administrator's own settings are set only where they have none, and taken out with the seed.
	var hasZone, hasPreferences bool
	s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM member_settings WHERE organization_id=$1 AND subject=$2),EXISTS(SELECT 1 FROM attention_preferences WHERE organization_id=$1 AND subject=$2)`, s.o.Organization, s.o.Subject).Scan(&hasZone, &hasPreferences)
	if !hasZone {
		if _, ok := s.must(ctx, "time zone", "POST", "/v1/closes/me", map[string]any{"time_zone": "America/New_York"}); ok {
			s.db.ExecContext(ctx, `UPDATE demonstration_seeds SET policy=policy||'{"set_time_zone":true}'::jsonb WHERE organization_id=$1`, s.o.Organization)
		}
	}
	if !hasPreferences {
		if _, ok := s.must(ctx, "notification preferences", "POST", "/v1/closes/attention/preferences", map[string]any{"kinds": seedKinds, "browser": false}); ok {
			s.db.ExecContext(ctx, `UPDATE demonstration_seeds SET policy=policy||'{"set_preferences":true}'::jsonb WHERE organization_id=$1`, s.o.Organization)
		}
	}
	// Invitations: one accepted in July, one still pending; and the role history a growing firm has.
	var id int64
	if err := s.db.QueryRowContext(ctx, `INSERT INTO member_invitations(organization_id,email,role_slug,role_name,workos_id,state,invited_by,reason,expires_at,created_at,decided_at)
 VALUES($1,'r.castillo@example.com','member','Member','invitation_01SEEDACCEPTED'||right($1,8),'accepted',$2,'Joins as a staff accountant for the dental and landscaping clients',$3,$4,$5) RETURNING id`, s.o.Organization, s.o.Subject, s.today.AddDate(0, 0, -80), s.today.AddDate(0, 0, -87), s.today.AddDate(0, 0, -86)).Scan(&id); err != nil {
		s.problem("invitation: %v", err)
	} else {
		s.record(ctx, "invitation_ids", id)
	}
	if err := s.db.QueryRowContext(ctx, `INSERT INTO member_invitations(organization_id,email,role_slug,role_name,workos_id,state,invited_by,reason,expires_at,created_at)
 VALUES($1,'j.okonkwo@example.com','member','Member','invitation_01SEEDPENDING'||right($1,8),'pending',$2,'Seasonal help for the September and October closes',$3,$4) RETURNING id`, s.o.Organization, s.o.Subject, s.today.AddDate(0, 0, 5), s.today.AddDate(0, 0, -2)).Scan(&id); err != nil {
		s.problem("invitation: %v", err)
	} else {
		s.record(ctx, "invitation_ids", id)
	}
	// The administrator's own agent connection, approved when the firm turned the agent on.
	if code, out := s.call(ctx, "POST", "/v1/closes/agent-access", map[string]any{"action": "request", "scope": "prepare", "reason": "Use the agent from my editor for the monthly preparation work", "confirmed": true}); code == 200 {
		s.call(ctx, "POST", "/v1/closes/agent-access", map[string]any{"action": "approve", "subject": s.o.Subject, "scope": "prepare", "previous_id": str(field(out, "id")), "reason": "Approved for the firm's administrator", "confirmed": true})
		rows, err := s.db.QueryContext(ctx, `SELECT id FROM agent_access WHERE organization_id=$1 AND subject=$2 AND created_at>=(SELECT started_at FROM demonstration_seeds WHERE organization_id=$1)`, s.o.Organization, s.o.Subject)
		if err == nil {
			for rows.Next() {
				if rows.Scan(&id) == nil {
					s.record(ctx, "agent_access_ids", id)
				}
			}
			rows.Close()
		}
	}
	return nil
}

// policies are the firm's rules as a careful small firm sets them, each change recorded with its reason.
// seedKinds are the notification kinds the seed chooses for an administrator who had chosen none.
var seedKinds = []string{"answered", "confirm", "failed", "approve", "changed", "fix", "send", "unanswered", "goal", "handoff"}

// seedPolicy is what the seed sets, by the firm's column, and policyInputs the route's name for each.
var seedPolicy = map[string]any{"agent_chat": "firm", "materiality_amount": "250.00", "require_link_passcode": true, "reminder_after_days": 3, "reminder_max": 2}
var policyInputs = map[string]string{"agent_chat": "agent_chat", "materiality_amount": "materiality_amount", "require_link_passcode": "link_passcode", "reminder_after_days": "reminder_after_days", "reminder_max": "reminder_max"}

// policies are the firm's rules as a careful small firm sets them, each change recorded with its reason. The review
// rules are never touched.
func (s *seeder) policies(ctx context.Context) {
	// A firm with clients of its own keeps its policy: a change would reach their work too.
	var others bool
	s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM client_closes WHERE organization_id=$1 AND NOT lower(entity)=ANY($2)) OR EXISTS(SELECT 1 FROM client_roster WHERE organization_id=$1 AND NOT lower(client_entity)=ANY($2))`, s.o.Organization, s.clientNames()).Scan(&others)
	if others {
		s.log("  the firm has clients of its own, so its policy is left as it is")
		return
	}
	changes := []map[string]any{
		{"agent_chat": "firm", "reason": "Turn the agent on for the whole firm after the June pilot with two clients"},
		{"materiality_amount": "250.00", "reason": "Differences under $250 are noted, not chased, for our client sizes"},
		{"link_passcode": true, "reason": "Clients asked for a passcode on upload links after the phishing drill"},
		{"reminder_after_days": 3, "reminder_max": 2, "reason": "Two reminders, three days apart, then a phone call"},
	}
	for _, change := range changes {
		if _, ok := s.must(ctx, "policy", "POST", "/v1/closes/organization/policy", change); !ok {
			continue
		}
		for input := range change {
			for column, name := range policyInputs {
				if name == input {
					s.db.ExecContext(ctx, `UPDATE demonstration_seeds SET policy=policy||jsonb_build_object('changed',COALESCE(policy->'changed','[]'::jsonb)||to_jsonb($2::text)) WHERE organization_id=$1`, s.o.Organization, column)
				}
			}
		}
	}
}

// roster puts every client on the roster with the firm's monthly template; a client who left is marked inactive
// after its last month (in client()).
func (s *seeder) roster(ctx context.Context) {
	out, ok := s.must(ctx, "roster", "GET", "/v1/closes/roster", nil)
	if !ok {
		return
	}
	// The firm's own monthly template stays; a firm without one gets the demonstration's.
	if field(out, "template") == nil {
		s.must(ctx, "template", "POST", "/v1/closes/close-template", map[string]any{"due_days": s.firm.Template.DueDays, "owner": "", "reason": "Every month is due on the 15th of the next; owners are set per client", "rev": ""})
	} else if days, ok := field(out, "template", "due_days").(float64); ok && days > 0 {
		s.firm.Template.DueDays = int(days)
	}
	for _, c := range s.firm.Clients {
		entry := map[string]any{"entity": c.Name, "active": true, "since": c.Since, "reason": "Engaged for monthly close and management reporting", "rev": ""}
		if c.DueDays > 0 {
			entry["due_days"] = c.DueDays
			entry["reason"] = fmt.Sprintf("Engaged for monthly close; the client wants its month closed within %d days", c.DueDays)
		}
		if c.Since > seed.Months[0] {
			entry["reason"] = "New client; first month to close is " + c.Since
		}
		code, out := s.call(ctx, "POST", "/v1/closes/roster", entry)
		if code != 200 && !strings.Contains(string(out), "already on the roster") {
			s.problem("roster %s: %d %s", c.Name, code, out)
		}
		if code == 200 {
			s.recordNewest(ctx, "client_roster", c.Name)
		}
	}
	// Names clients are also known by on their documents.
	for _, alias := range [][2]string{{"Harbor Fixture LLC", "harbor fixture"}, {"Cedar & Pine Coffee Co.", "cedar pine coffee"}, {"Meridian Physical Therapy PC", "meridian pt"}, {"Bluegrass Dental PLLC", "bluegrass dental"}} {
		if result, err := s.db.ExecContext(ctx, `INSERT INTO client_aliases(organization_id,client_entity,alias,confirmed_by) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, s.o.Organization, alias[0], alias[1], s.o.Subject); err == nil {
			if n, _ := result.RowsAffected(); n == 1 {
				s.recordRow(ctx, "client_aliases", alias[0]+" | "+alias[1])
			}
		}
	}
}

// procedures are the firm's approved methods: firm-wide for payroll, loans, owner items, accruals and bank
// reconciliations, and per client for prepaids (each client's expense account differs).
// procedures publishes nothing firm-wide: a firm's own defaults stay as they are, and each demonstration client gets
// its own versions (clientProcedures), which reach no other client.
func (s *seeder) procedures(ctx context.Context) {}

// clientProcedures are a client's own rules, published once its first month exists: its accounting basis, its entity
// type for owner items, the accounts its prepaids use, and the methods its payroll, loan, accruals and
// reconciliations follow.
func (s *seeder) clientProcedures(ctx context.Context, c *seed.Client) {
	publish := s.publishProcedure(ctx)
	publish(c.Name, "basis", "accrual", c.Name+" keeps its books on the accrual basis; the month-end capture is read on that basis.", "", "")
	if c.Payroll != nil {
		publish(c.Name, "payroll", "payroll_summary_v1", "Record payroll from "+c.Payroll.Provider+"'s monthly summary: wages and employer taxes to expense, the provider's debits cleared through Payroll Clearing.", "", "")
	}
	if c.Loan != nil {
		publish(c.Name, "loan", "lender_statement_v1", "Take principal, interest and the closing balance from "+c.Loan.Lender+"'s statement; accrue interest from the last payment to month-end when the note is paid mid-month.", "", "")
	}
	if len(c.Owners) > 0 {
		publish(c.Name, "owner", c.EntityType, "Personal purchases paid by the business are reclassified to the owner's distributions once the owner confirms in writing; the entity type decides the treatment.", "", "")
	}
	if len(c.Accruals) > 0 {
		publish(c.Name, "accrual", "", "Accrue services incurred but not yet invoiced from the vendor's estimate, reversing on the first of the next month.", "", "")
	}
	if c.Reconcile {
		publish(c.Name, "reconciliation", "", "Reconcile Checking to the bank's export every month; timing items need their clearing reference.", "", "")
	}
	if len(c.Prepaids) > 0 {
		id := publish(c.Name, "prepaid", "", "Spread annual policies and subscriptions over equal full months from the first month of service.", seed.ExpenseName(c.Prepaids[0].Expense), "Prepaid Expenses")
		// The equal-full-months rule is the method procedure learning records on a prepaid procedure; the route itself
		// records only the accounts and the instructions. Only the version just published takes it.
		if id != "" {
			s.db.ExecContext(ctx, `UPDATE procedure_versions SET method='equal_full_months_v1' WHERE organization_id=$1 AND id::text=$2 AND kind='prepaid' AND method=''`, s.o.Organization, id)
		}
	}
}

func (s *seeder) publishProcedure(ctx context.Context) func(client, kind, method, instructions, expense, prepaid string) string {
	return func(client, kind, method, instructions, expense, prepaid string) string {
		query := url.Values{"impact_kind": {kind}}
		if client != "" {
			query.Set("client_entity", client)
		}
		out, ok := s.must(ctx, "procedure impact "+kind, "GET", "/v1/closes/procedures?"+query.Encode(), nil)
		if !ok {
			return ""
		}
		body := map[string]any{"client_entity": client, "kind": kind, "method": method, "instructions": instructions, "enabled": true, "reason": "Firm procedure adopted at onboarding", "confirmed": true, "impact_digest": str(field(out, "impact_digest")), "previous_id": str(field(out, "previous_id"))}
		if expense != "" {
			body["expense_account"], body["prepaid_account"] = expense, prepaid
		}
		out, ok = s.must(ctx, "procedure "+kind+" "+client, "POST", "/v1/closes/procedures", body)
		if !ok {
			return ""
		}
		if id, err := strconv.ParseInt(str(field(out, "id")), 10, 64); err == nil {
			s.record(ctx, "procedure_ids", id)
		}
		return str(field(out, "id"))
	}
}

// owner is the member who owns a client's closes.
func (s *seeder) owner(c *seed.Client) string {
	people := append([]string{s.o.Subject}, s.o.Members...)
	return people[c.OwnerSlot%len(people)]
}

// client works one client through its months.
func (s *seeder) client(ctx context.Context, c *seed.Client, index int) error {
	last := seed.Months[len(seed.Months)-1]
	if c.LeftIn != "" {
		last = previous(c.LeftIn)
	}
	books := seed.BooksFor(c, last)
	s.log("%s: %s through %s (%s)", c.Name, seed.Months[0], last, c.Scenario)
	var connection string
	var earlier []seed.Books
	for n, b := range books {
		period := b.Period
		if period > last {
			break
		}
		final := period == last && c.LeftIn == ""
		closeID, err := s.openClose(ctx, c, period)
		if err != nil {
			return err
		}
		s.plan(ctx, c, closeID, period, n, final)
		if !(final && c.Scenario == seed.Uncaptured) {
			if connection, err = s.capture(ctx, c, closeID, connection, b, earlier, n); err != nil {
				return fmt.Errorf("%s capture: %w", period, err)
			}
		}
		work := c.WorkFor(period, b)
		jobs, evidenceIDs := s.documents(ctx, closeID, work)
		if err := s.settle(ctx); err != nil {
			return err
		}
		s.decide(ctx, c, closeID, period, work, jobs, evidenceIDs, final)
		if !(final && c.Scenario == seed.Uncaptured) {
			s.recurring(ctx, c, closeID, period)
		}
		s.effort(ctx, c, closeID, period, final)
		if final && c.Scenario != seed.Signed && c.Scenario != seed.Reopened {
			s.september(ctx, c, closeID, work, jobs)
			earlier = append(earlier, b)
			continue
		}
		s.review(ctx, closeID, c, period, final)
		s.cover(ctx, closeID, b)
		if n == 0 {
			// The opening baseline is reviewed once the first month's drafts stand, as its tie-out shows them.
			s.takeover(ctx, closeID)
		}
		s.scope(ctx, closeID, c, period)
		if c.LeftIn != "" && period == last {
			s.leave(ctx, c)
		}
		signed := s.signOff(ctx, closeID, c, period)
		if signed {
			if err := s.settle(ctx); err != nil {
				return err
			}
			// Sign-off carried the schedules into the next month and opened it; it gets its owner and due date too.
			if next := s.closeFor(ctx, c.Name, nextPeriod(period)); next != "" {
				s.db.ExecContext(ctx, `UPDATE demonstration_seeds SET close_ids=array_append(close_ids,$2::bigint) WHERE organization_id=$1 AND NOT $2::bigint=ANY(close_ids)`, s.o.Organization, next)
				s.plan(ctx, c, next, nextPeriod(period), n+1, false)
			}
		}
		if signed && c.Publish {
			s.publish(ctx, closeID, c, period)
		}
		if final && c.Scenario == seed.Reopened && signed {
			s.reopen(ctx, c, closeID, period, work)
		}
		earlier = append(earlier, b)
	}
	if c.Budget {
		s.budget(ctx, c, books)
	}
	if c.CashPlan {
		s.cashPlan(ctx, c)
	}
	return nil
}

// leave marks a client inactive on the roster, so no further month is owed or opened.
func (s *seeder) leave(ctx context.Context, c *seed.Client) {
	_, out := s.call(ctx, "GET", "/v1/closes/roster", nil)
	var roster struct {
		Clients []struct {
			Entity string `json:"entity"`
			Rev    string `json:"rev"`
		} `json:"clients"`
	}
	json.Unmarshal(out, &roster)
	for _, r := range roster.Clients {
		if r.Entity == c.Name {
			if _, ok := s.must(ctx, "roster leave", "POST", "/v1/closes/roster", map[string]any{"entity": c.Name, "active": false, "since": c.Since, "reason": "Hired an in-house bookkeeper; July was the last month we closed", "rev": r.Rev}); ok {
				s.recordNewest(ctx, "client_roster", c.Name)
			}
		}
	}
}

func nextPeriod(period string) string {
	t, _ := time.Parse("2006-01", period)
	return t.AddDate(0, 1, 0).Format("2006-01")
}

func previous(period string) string {
	t, _ := time.Parse("2006-01", period)
	return t.AddDate(0, -1, 0).Format("2006-01")
}

// openClose opens the client's month, or finds the one carry-forward opened.
func (s *seeder) openClose(ctx context.Context, c *seed.Client, period string) (string, error) {
	out, ok := s.must(ctx, "open "+period, "POST", "/v1/closes", map[string]any{"entity": c.Name, "period": period})
	if !ok {
		return "", fmt.Errorf("could not open %s", period)
	}
	id := str(field(out, "id"))
	if id == "" {
		return "", fmt.Errorf("no close ID for %s", period)
	}
	// Recorded as soon as it exists, so a run that stops here can still be removed.
	s.db.ExecContext(ctx, `UPDATE demonstration_seeds SET close_ids=array_append(close_ids,$2::bigint) WHERE organization_id=$1 AND NOT $2::bigint=ANY(close_ids)`, s.o.Organization, id)
	if period == seed.Months[0] {
		s.clientProcedures(ctx, c)
		// A named teammate who will own the client is assigned it, so its work is theirs to open.
		if owner := s.owner(c); owner != s.o.Subject {
			if _, ok := s.must(ctx, "assign "+c.Name, "POST", "/v1/closes/access", map[string]any{"subject": owner, "entity": c.Name, "granted": true, "reason": "Assigned " + c.Name + " at onboarding", "confirmed": true}); ok {
				s.recordNewest(ctx, "client_access", c.Name)
			}
		}
	}
	// The client's approved prepaid procedure is applied to the close as evidence, so the agent can prepare
	// straight through.
	if len(c.Prepaids) > 0 {
		tx, err := s.db.BeginTx(ctx, nil)
		if err == nil {
			procedure, err := preparation.CurrentProcedure(ctx, tx, id, "prepaid")
			if err == nil {
				_, err = preparation.AttachProcedure(ctx, tx, id, procedure, false, s.o.Subject)
			}
			if err == nil {
				err = tx.Commit()
			}
			if err != nil {
				tx.Rollback()
				s.problem("%s %s: apply prepaid procedure: %v", c.Name, period, err)
			}
		}
	}
	return id, nil
}

// plan gives the close its due date and owner: the firm's template unless the client's roster entry says otherwise,
// and the owner the roster names. A September close that is overdue was due earlier than its template says.
func (s *seeder) plan(ctx context.Context, c *seed.Client, closeID, period string, n int, final bool) {
	owner := s.owner(c)
	priority := "normal"
	if final && (c.Scenario == seed.Overdue || c.Scenario == seed.WaitingClient) {
		priority = "high"
	}
	// Every month is due the template's days after it ends, or the client's own; an overdue September was due days ago.
	days := s.firm.Template.DueDays
	if c.DueDays > 0 {
		days = c.DueDays
	}
	due := monthEndTime(period).AddDate(0, 0, days).Format("2006-01-02")
	if final && c.Scenario == seed.Overdue {
		due = s.today.AddDate(0, 0, -4).Format("2006-01-02")
	}
	body := map[string]any{"priority": priority, "assignment": "keep", "due_on": due}
	if owner == s.o.Subject {
		body["assignment"] = "take"
		body["note"] = "Taking " + c.Name + " for " + period
	}
	s.must(ctx, "plan "+period, "POST", "/v1/closes/"+closeID+"/planning", body)
	if owner != s.o.Subject {
		// A teammate's close: handed over as the assign action records it (the lookup of teammates needs WorkOS).
		s.db.ExecContext(ctx, `UPDATE client_closes SET owner=$2 WHERE id::text=$1`, closeID, owner)
		s.db.ExecContext(ctx, `INSERT INTO close_owner_changes(close_id,from_owner,to_owner,actor,reason) SELECT id,'',$2,$3,$4 FROM client_closes WHERE id::text=$1`, closeID, owner, s.o.Subject, "Handed to the client's usual accountant")
	}
	if priority == "high" {
		s.db.ExecContext(ctx, `UPDATE client_closes SET priority='high' WHERE id::text=$1`, closeID)
	}
}

// capture gives the close its month-end books, as a QuickBooks capture leaves them: the connection (once per
// client), the month-end snapshot with its evidence, and the transaction capture with its rows, open items and
// exceptions.
func (s *seeder) capture(ctx context.Context, c *seed.Client, closeID, connection string, b seed.Books, earlier []seed.Books, n int) (string, error) {
	// The connection is labeled as the demonstration's: its company name says so, its realm is no QuickBooks realm, and
	// it is a sandbox connection, so nothing it holds counts as a live provider read.
	realm := fmt.Sprintf("demonstration-%07d", 1000000+seedIndex(c.Name))
	// The month-end capture is taken as the month turns, so its balances and its as-of time agree.
	captured := monthEndTime(b.Period).Add(time.Duration(30+n) * time.Minute)
	if captured.After(s.today) {
		captured = s.today.Add(-2 * time.Hour)
	}
	if connection == "" {
		err := s.db.QueryRowContext(ctx, `INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,company_name,home_currency,fiscal_year_start,environment,confirmed_by,confirmed_at,created_at,updated_at,access_expires_at)
 VALUES($1,$2,$3,$4,'active',$5,$6,$7,$3||' (demonstration books)','USD',1,'sandbox',$4,$6,$6,$6,$6) RETURNING id::text`, closeID, s.o.Organization, c.Name, s.o.Subject, rand.Text(), monthEndTime(seed.Months[0]).AddDate(0, 0, -20), realm).Scan(&connection)
		if err != nil {
			return "", err
		}
		s.recordRow(ctx, "quickbooks_connections", connection)
	}
	evidence := func(name, body string, at time.Time) (string, error) {
		var id string
		err := s.db.QueryRowContext(ctx, `INSERT INTO evidence(close_id,name,digest,content,created_by,extraction_state,created_at) VALUES($1,$2,$3,$4,'quickbooks-sync','ready',$5) RETURNING id::text`, closeID, name, preparation.Digest([]byte(body)), body, at).Scan(&id)
		return id, err
	}
	// The transaction capture.
	records := map[string][]json.RawMessage{}
	var all []string
	for _, t := range b.Txns {
		raw := b.Record(t)
		records[t.Kind] = append(records[t.Kind], raw)
		all = append(all, string(raw))
	}
	allRaw, _ := json.Marshal(all)
	open := b.OpenItems(earlier)
	index, _ := json.Marshal(map[string]any{"source": "quickbooks_transactions", "period": b.Period, "realm": realm, "counts": b.Counts(), "capture_digest": preparation.Digest(allRaw)})
	indexID, err := evidence("QuickBooks transactions "+b.Period, string(index), captured)
	if err != nil {
		return "", err
	}
	var rows []quickbooks.TransactionRow
	for _, t := range b.Txns {
		rows = append(rows, quickbooks.TransactionRow{Kind: t.Kind, ProviderID: t.ID, Date: t.Date, Currency: "USD", Amount: t.Amount.FloatString(2), Record: b.Record(t)})
	}
	exceptions := append(quickbooks.FindExceptions(rows), quickbooks.OpenItemExceptions(open, monthEndTime(b.Period))...)
	if exceptions == nil {
		exceptions = []quickbooks.Exception{}
	}
	exceptionsRaw, _ := json.Marshal(exceptions)
	summary, _ := json.Marshal(map[string]any{"counts": b.Counts(), "started_at": captured.Add(-3 * time.Minute), "finished_at": captured, "completeness": "All supported types were paged and provider counts agreed before and after each type. Reads are sequential, not an atomic ledger snapshot; later edits require refresh.", "open_items": open})
	var syncID string
	if err = s.db.QueryRowContext(ctx, `INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,evidence_id,started_at,finished_at,payload,digest,exceptions,created_at) VALUES($1,$2,$3,'scheduled-quickbooks-refresh','completed',$4,$5,$6,$7,$8,$9,$5) RETURNING id::text`,
		closeID, connection, b.Period, indexID, captured.Add(-3*time.Minute), captured, string(summary), preparation.Digest(allRaw), string(exceptionsRaw)).Scan(&syncID); err != nil {
		return "", err
	}
	for _, t := range b.Txns {
		if _, err = s.db.ExecContext(ctx, `INSERT INTO quickbooks_transaction_rows(sync_id,kind,provider_id,revision,txn_date,provider_updated_at,currency,amount,record) VALUES($1,$2,$3,'0',$4,$5,'USD',$6,$7)`,
			syncID, t.Kind, t.ID, t.Date, t.Date+"T14:00:00Z", t.Amount.FloatString(2), string(b.Record(t))); err != nil {
			return "", err
		}
	}
	// The month-end snapshot, assembled by the code a real capture uses.
	snapshot, body, err := quickbooks.PeriodSnapshot("sandbox", realm, c.Name+" (demonstration books)", b.Period, b.Directory(), b.Reports(), captured.Add(-2*time.Minute))
	if err != nil {
		return "", err
	}
	sourceID, err := evidence("QuickBooks "+b.Period+" accrual period evidence.txt", string(body), captured)
	if err != nil {
		return "", err
	}
	snapshot["source_id"] = sourceID
	payload, _ := json.Marshal(snapshot)
	_, err = s.db.ExecContext(ctx, `INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,captured_at,actor) VALUES($1,$2,$3,$4,$5,$6,$7,'quickbooks-sync')`, closeID, connection, realm, preparation.Digest(payload), string(payload), captured.Add(-2*time.Minute), captured)
	s.captures.Store(closeID, [2]string{sourceID, indexID})
	return connection, err
}

func seedIndex(name string) int {
	n := 0
	for _, r := range name {
		n = (n*31 + int(r)) % 9000000
	}
	return n
}

func monthEndTime(period string) time.Time {
	t, _ := time.Parse("2006-01", period)
	return t.AddDate(0, 1, 0).Add(-time.Second)
}

// takeover confirms the client's first month: the takeover setup and the opening baseline the tie-out needs.
func (s *seeder) takeover(ctx context.Context, closeID string) {
	if out, ok := s.must(ctx, "takeover setup", "GET", "/v1/closes/"+closeID+"/takeover-setup", nil); ok {
		s.must(ctx, "takeover confirm", "POST", "/v1/closes/"+closeID+"/takeover-setup", map[string]any{"digest": str(field(out, "digest")), "timezone": "America/New_York", "confirmed": true})
	}
	out, ok := s.must(ctx, "opening baseline", "GET", "/v1/closes/"+closeID+"/opening-baseline", nil)
	if !ok {
		return
	}
	notes := map[string]string{}
	var items []struct{ Key, Label string }
	if raw, _ := json.Marshal(field(out, "items")); raw != nil {
		json.Unmarshal(raw, &items)
	}
	for _, item := range items {
		notes[item.Key] = "Taken over as the prior bookkeeper's closing balance; to be agreed to the bank statement and the vendor statements in the first month."
	}
	if _, ok = s.must(ctx, "opening baseline confirm", "POST", "/v1/closes/"+closeID+"/opening-baseline", map[string]any{"digest": str(field(out, "digest")), "notes": notes, "confirmed": true}); !ok {
		return
	}
	// The takeover cleanup was done in the first month: each inherited balance was agreed to its statement.
	var baselineID string
	s.db.QueryRowContext(ctx, `SELECT id::text FROM opening_baselines WHERE close_id::text=$1 ORDER BY id DESC LIMIT 1`, closeID).Scan(&baselineID)
	for _, item := range items {
		s.must(ctx, "resolve "+item.Label, "POST", "/v1/closes/"+closeID+"/opening-baseline/resolve", map[string]any{"baseline_id": baselineID, "item": item.Key, "reference": "Agreed to the June bank and vendor statements; no correcting entry needed."})
	}
}

// documents adds the month's documents as one upload would and records each one's answer for the reading. It returns
// each document's job ID by file name.
func (s *seeder) documents(ctx context.Context, closeID string, work []seed.Work) (map[string]string, map[string]string) {
	jobs, evidenceIDs := map[string]string{}, map[string]string{}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.problem("documents: %v", err)
		return jobs, evidenceIDs
	}
	defer tx.Rollback()
	actor := s.actor(ctx, s.o.Subject)
	if _, err = tx.ExecContext(ctx, `SELECT id FROM client_closes WHERE id::text=$1 FOR UPDATE`, closeID); err != nil {
		s.problem("documents: %v", err)
		return jobs, evidenceIDs
	}
	for _, w := range work {
		id, _, jobID, refused := addEvidence(actor, tx, closeID, w.File, []byte(w.Text), false, "work")
		if refused != nil {
			s.problem("upload %s: %s", w.File, refused.message)
			continue
		}
		if !w.NoAnswer {
			s.answers.Store(id, answerFor(w))
		}
		s.closeOf.Store(id, closeID)
		jobs[w.File], evidenceIDs[w.File] = jobID, id
	}
	if err = tx.Commit(); err != nil {
		s.problem("documents: %v", err)
	}
	files := map[string]string{}
	if f, ok := s.filesOf.Load(closeID); ok {
		files = f.(map[string]string)
	}
	for file, id := range evidenceIDs {
		files[file] = id
	}
	s.filesOf.Store(closeID, files)
	return jobs, evidenceIDs
}

// jobState reads a job's state, revision and proposal.
func (s *seeder) jobState(ctx context.Context, jobID string) (state string, revision int, proposal preparation.Proposal) {
	var raw []byte
	s.db.QueryRowContext(ctx, `SELECT j.state,i.revision,i.proposal FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id WHERE j.id::text=$1`, jobID).Scan(&state, &revision, &raw)
	json.Unmarshal(raw, &proposal)
	return
}

// decide does the accountant's part with each document once the agent is done: confirm an accrual, prepare a loan,
// payroll, owner item or reconciliation by hand from the document, and leave what the scenario leaves open.
func (s *seeder) decide(ctx context.Context, c *seed.Client, closeID, period string, work []seed.Work, jobs, evidenceIDs map[string]string, final bool) {
	capture, _ := s.captures.Load(closeID)
	ledgerID, indexID := "", ""
	if capture != nil {
		ledgerID, indexID = capture.([2]string)[0], capture.([2]string)[1]
	}
	for _, w := range work {
		jobID := jobs[w.File]
		if jobID == "" || w.NoAnswer || w.Kind == "unsupported" {
			continue
		}
		state, revision, proposal := s.jobState(ctx, jobID)
		switch w.Kind {
		case "register":
			// The register export is the reconciliation's ledger side; its own reading is set aside.
			if state == "needs_confirmation" {
				s.must(ctx, "set aside the register's reading", "POST", "/v1/closes/"+closeID+"/work/"+jobID+"/cancel", map[string]any{"revision": revision})
			}
			continue
		case "prepaid":
			// An invoice the procedure's accounts do not cover (a second kind of prepaid) is confirmed by the accountant.
			if state == "needs_confirmation" && len(proposal.Questions) == 0 {
				s.must(ctx, "confirm prepaid "+w.File, "POST", "/v1/closes/"+closeID+"/work/"+jobID+"/confirm", map[string]any{"revision": revision, "facts_confirmed": true, "inventory_confirmed": true, "input": w.Confirm})
			} else if state != "completed" {
				s.problem("%s %s: prepaid %s is %s (%s): %s", c.Name, period, w.File, state, proposal.Engine, strings.Join(proposal.Questions, "; "))
			}
		case "accrual":
			if state != "needs_confirmation" {
				s.problem("%s %s: accrual %s is %s: %s", c.Name, period, w.File, state, strings.Join(proposal.Questions, "; "))
				continue
			}
			if final && c.Scenario == seed.Overdue {
				continue // the accrual waits with the open question; the month is overdue
			}
			s.must(ctx, "confirm accrual "+w.File, "POST", "/v1/closes/"+closeID+"/work/"+jobID+"/confirm", map[string]any{"revision": revision, "facts_confirmed": true, "inventory_confirmed": true, "input": w.Confirm})
		case "loan", "payroll", "owner", "reconciliation":
			if final && w.Request != nil {
				continue
			}
			if ledgerID == "" && w.Kind != "reconciliation" {
				continue // no books captured: the draft waits for the capture
			}
			input := map[string]any{"entity": c.Name, "close_period": period}
			for k, v := range w.Confirm {
				input[k] = v
			}
			raw, _ := json.Marshal(input)
			primary := evidenceIDs[w.File]
			sources := map[string][]map[string]string{
				"loan":           {{"id": primary, "role": "statement", "location": "Whole statement"}, {"id": ledgerID, "role": "ledger", "location": "Equipment Loan and Interest Expense"}, {"id": indexID, "role": "support", "location": "Loan payment purchase"}},
				"payroll":        {{"id": primary, "role": "support", "location": "Whole summary"}, {"id": ledgerID, "role": "ledger", "location": "Payroll Clearing"}},
				"owner":          {{"id": primary, "role": "support", "location": "Owner's reply"}, {"id": ledgerID, "role": "ledger", "location": "Meals and Entertainment"}},
				"reconciliation": {{"id": primary, "role": "statement", "location": "Whole export"}, {"id": evidenceIDs[w.Register], "role": "ledger", "location": "Whole export"}},
			}[w.Kind]
			plan := map[string]any{"kind": w.Kind, "input": json.RawMessage(raw), "sources": sources}
			if state == "needs_confirmation" && len(proposal.Questions) == 0 {
				plan["work_id"], plan["work_revision"] = jobID, revision
			}
			if out, ok := s.must(ctx, "prepare "+w.Kind+" from "+w.File, "POST", "/v1/closes/"+closeID+"/preparations", plan); ok {
				if _, linked := plan["work_id"]; !linked {
					// A plan saved without the agent's work is run as its own job.
					s.must(ctx, "queue "+w.Kind, "POST", "/v1/closes/"+closeID+"/jobs", map[string]any{"plans": []string{str(field(out, "id"))}})
				}
				if _, linked := plan["work_id"]; !linked && state == "needs_confirmation" {
					_, revision, _ = s.jobState(ctx, jobID)
					s.must(ctx, "cancel agent's "+w.Kind+" intake", "POST", "/v1/closes/"+closeID+"/work/"+jobID+"/cancel", map[string]any{"revision": revision})
				}
			} else if state == "needs_confirmation" {
				s.problem("%s %s: %s proposal: %s", c.Name, period, w.Kind, strings.Join(proposal.Questions, "; "))
			}
		}
	}
	if err := s.settle(ctx); err != nil {
		s.problem("%s %s: %v", c.Name, period, err)
	}
}

// recurring refreshes each carried schedule against the month's capture with its booked balance, as the Recurring page
// does, so the month's entry is proposed from the schedule.
func (s *seeder) recurring(ctx context.Context, c *seed.Client, closeID, period string) {
	out, ok := s.must(ctx, "recurring", "GET", "/v1/closes/"+closeID+"/recurring", nil)
	if !ok {
		return
	}
	var view struct {
		Snapshot struct {
			ID string `json:"id"`
		} `json:"snapshot"`
		Items []struct {
			ID      string `json:"id"`
			Digest  string `json:"digest"`
			Blocker string `json:"blocker"`
			JobID   string `json:"job_id"`
		} `json:"items"`
	}
	json.Unmarshal(out, &view)
	queued := false
	for _, item := range view.Items {
		if item.Blocker != "" || item.JobID != "" || view.Snapshot.ID == "" {
			continue
		}
		var booked string
		s.db.QueryRowContext(ctx, `SELECT COALESCE(paper->'carry_forward'->>'opening_balance',paper->>'prepaid_balance','0.00') FROM workpapers WHERE id::text=$1`, item.ID).Scan(&booked)
		if _, ok := s.must(ctx, "recurring "+item.ID, "POST", "/v1/closes/"+closeID+"/recurring", map[string]any{"paper_id": item.ID, "digest": item.Digest, "snapshot_id": view.Snapshot.ID, "booked_balance": booked, "confirmed": true}); ok {
			queued = true
		}
	}
	if queued {
		if err := s.settle(ctx); err != nil {
			s.problem("%s %s: %v", c.Name, period, err)
		}
	}
}

// effort records the people's time on the close, as the Measure page counts it.
func (s *seeder) effort(ctx context.Context, c *seed.Client, closeID, period string, final bool) {
	owner := s.owner(c)
	entries := []struct {
		activity string
		minutes  int
		note     string
	}{
		{"evidence", 20 + seedIndex(c.Name+period)%25, "Collected the month's documents from " + c.Contact + " and checked the capture"},
		{"review", 25 + seedIndex(period+c.Name)%40, "Reviewed the agent's drafts against the invoices and the Trial Balance"},
	}
	if !final {
		entries = append(entries, struct {
			activity string
			minutes  int
			note     string
		}{"signoff", 10 + seedIndex(c.Name)%10, "Tie-out, coverage decisions and sign-off"})
	}
	for _, e := range entries {
		if owner == s.o.Subject {
			s.must(ctx, "time entry", "POST", "/v1/closes/"+closeID+"/effort/entries", map[string]any{"activity": e.activity, "minutes": e.minutes, "note": e.note})
		} else {
			// A teammate's time is recorded as theirs; the seed never acts with their authority.
			s.db.ExecContext(ctx, `INSERT INTO close_effort_entries(close_id,actor,activity,minutes,note) SELECT id,$2,$3,$4,$5 FROM client_closes WHERE id::text=$1`, closeID, owner, e.activity, e.minutes, e.note)
		}
	}
	// A few minutes of tracked activity on the page, as the workspace records it.
	for i, activity := range []string{"review", "evidence", "review"} {
		s.db.ExecContext(ctx, `INSERT INTO close_effort(close_id,actor,minute,activity,seconds) SELECT id,$2,$3,$4,$5 FROM client_closes WHERE id::text=$1 ON CONFLICT DO NOTHING`, closeID, owner, monthEndTime(period).Add(time.Duration(30+i)*time.Hour).Truncate(time.Minute), activity, 20+i*5)
	}
}

// papers lists the close's current workpapers: every draft no newer draft revises.
func (s *seeder) papers(ctx context.Context, closeID string) []map[string]any {
	out, ok := s.must(ctx, "close", "GET", "/v1/closes/"+closeID, nil)
	if !ok {
		return nil
	}
	var view struct {
		Workpapers []map[string]any `json:"workpapers"`
	}
	json.Unmarshal(out, &view)
	revised := map[string]bool{}
	for _, p := range view.Workpapers {
		if paper, ok := p["paper"].(map[string]any); ok {
			if old := str(paper["revises"]); old != "" {
				revised[old] = true
			}
		}
	}
	current := []map[string]any{}
	for _, p := range view.Workpapers {
		if !revised[str(p["id"])] {
			current = append(current, p)
		}
	}
	return current
}

// review approves every current draft as the administrator: every decision the seed records is theirs, since it
// never acts with another member's authority.
func (s *seeder) review(ctx context.Context, closeID string, c *seed.Client, period string, final bool) {
	owner := s.o.Subject
	for _, p := range s.papers(ctx, closeID) {
		if str(p["review_state"]) == "approved" {
			continue
		}
		note := "Agrees to the source document and the Trial Balance."
		if str(p["kind"]) == "reconciliation" {
			note = "Statement and register agree; no outstanding items."
		}
		code, out := s.callAs(ctx, owner, "POST", "/v1/closes/"+closeID+"/workpapers/"+str(p["id"])+"/reviews", map[string]any{"decision": "approved", "note": note, "digest": str(p["digest"]), "expected_review_id": str(p["review_id"])})
		if code != 200 {
			s.problem("%s %s: approve draft %s: %d %s", c.Name, period, str(p["id"]), code, out)
		}
	}
}

// cover decides the capture's coverage: a prepaid or accrual account is bound to the work that supports it, a bank
// account to its reconciliation, and every other account is reviewed from the Trial Balance with its reason.
func (s *seeder) cover(ctx context.Context, closeID string, b seed.Books) {
	out, ok := s.must(ctx, "coverage", "GET", "/v1/closes/"+closeID+"/coverage", nil)
	if !ok {
		return
	}
	var reply struct {
		Coverage struct {
			Digest string `json:"digest"`
			Items  []struct {
				AccountID       string   `json:"account_id"`
				Name            string   `json:"name"`
				Kind            string   `json:"kind"`
				State           string   `json:"state"`
				SuggestedJobIDs []string `json:"suggested_job_ids"`
			} `json:"items"`
			Jobs []struct {
				ID    string `json:"id"`
				State string `json:"state"`
				Kind  string `json:"kind"`
				Facts []struct {
					Field string `json:"field"`
					Value string `json:"value"`
				} `json:"facts"`
			} `json:"jobs"`
		} `json:"coverage"`
	}
	json.Unmarshal(out, &reply)
	view := reply.Coverage
	for _, item := range view.Items {
		if item.State == "bound" || item.State == "excluded" || item.State == "covered" {
			continue
		}
		body := map[string]any{"action": "bind", "digest": view.Digest, "account_id": item.AccountID, "confirmed": true}
		jobs := []string{}
		for _, j := range view.Jobs {
			if j.State != "completed" || j.Kind != item.Kind {
				continue
			}
			// A reconciliation supports the one account it reconciles, named in its reading.
			if item.Kind == "reconciliation" {
				reconciles := false
				for _, f := range j.Facts {
					reconciles = reconciles || (f.Field == "account" && strings.EqualFold(f.Value, item.Name))
				}
				if !reconciles {
					continue
				}
			}
			jobs = append(jobs, j.ID)
		}
		if len(item.SuggestedJobIDs) > 0 {
			jobs = item.SuggestedJobIDs
		}
		switch {
		case item.Kind == "prepaid" && len(jobs) > 0:
			body["jobs"], body["reason"] = jobs, "Supported by the prepaid schedules prepared from the invoices."
		case item.Kind == "accrual" && len(jobs) > 0:
			body["jobs"], body["reason"] = jobs, "Supported by the accrual prepared from the vendor's estimate."
		case item.Kind == "reconciliation" && len(jobs) > 0:
			body["jobs"], body["reason"] = jobs, "Supported by the reconciliation of the account to the bank's export."
		case item.Kind == "reconciliation":
			body["excluded"], body["reason"] = true, "Not reconciled this month: the card is paid in full and reviewed from its statement in the aging."
		default:
			body["excluded"], body["reason"] = true, coverageReason(item.Name)
		}
		code, result := s.call(ctx, "POST", "/v1/closes/"+closeID+"/coverage", body)
		if code != 200 {
			s.problem("coverage %s (%s): %d %s", item.Name, item.Kind, code, result)
		}
		// The digest moves with each decision.
		if fresh, ok := s.must(ctx, "coverage", "GET", "/v1/closes/"+closeID+"/coverage", nil); ok {
			view.Digest = str(field(fresh, "coverage", "digest"))
		}
	}
}

func coverageReason(account string) string {
	switch {
	case strings.Contains(account, "Receivable"):
		return "Reviewed in the aging; collections are followed up as open items."
	case strings.Contains(account, "Payable"):
		return "Reviewed in the payables aging against the vendor statements."
	case strings.Contains(account, "Equipment") || strings.Contains(account, "Loan"):
		return "Agrees to the lender's statement and the fixed asset register; no additions this month."
	case strings.Contains(account, "Equity") || strings.Contains(account, "Retained") || strings.Contains(account, "Owner"):
		return "Equity moves only through owner items reviewed in the close."
	case strings.Contains(account, "Payroll"):
		return "Agrees to the payroll provider's summary; cleared by the payroll entry."
	case strings.Contains(account, "Accrued"):
		return "Reviewed against the accrual drafts; no other accrued items this month."
	case strings.Contains(account, "Ask My Accountant"):
		return "Nothing posted here this month; any unknown charge is resolved before sign-off."
	default:
		return "Period activity reviewed from the Trial Balance and the General Ledger; no adjustment needed."
	}
}

// scope defines what sign-off covers: every current draft.
func (s *seeder) scope(ctx context.Context, closeID string, c *seed.Client, period string) {
	out, ok := s.must(ctx, "readiness", "GET", "/v1/closes/"+closeID+"/readiness", nil)
	if !ok {
		return
	}
	papers := []string{}
	for _, p := range s.papers(ctx, closeID) {
		papers = append(papers, str(p["id"]))
	}
	steps := []map[string]any{}
	if len(papers) == 0 {
		steps = append(steps, map[string]any{"name": "Month reviewed from the capture", "complete": true, "reference": "Trial Balance and General Ledger reviewed line by line; no adjustment needed this month."})
	}
	s.must(ctx, "scope", "POST", "/v1/closes/"+closeID+"/scope", map[string]any{"digest": str(field(out, "digest")), "expected_decision_id": str(field(out, "decision_id")),
		"scope": map[string]any{"description": fmt.Sprintf("%s %s: prepaids, accruals, payroll, loans and owner items agreed to source documents; bank reconciled; coverage decided for every account.", c.Name, period), "required_papers": papers, "external_steps": steps}})
}

// signOff signs the month off when nothing blocks it; a blocked month is reported with its blockers.
func (s *seeder) signOff(ctx context.Context, closeID string, c *seed.Client, period string) bool {
	out, ok := s.must(ctx, "readiness", "GET", "/v1/closes/"+closeID+"/readiness", nil)
	if !ok {
		return false
	}
	var ready struct {
		Digest     string   `json:"digest"`
		DecisionID string   `json:"decision_id"`
		State      string   `json:"state"`
		Blockers   []string `json:"blockers"`
	}
	json.Unmarshal(out, &ready)
	if len(ready.Blockers) > 0 || ready.State != "open" {
		s.problem("%s %s not signed off (%s): %s", c.Name, period, ready.State, strings.Join(ready.Blockers, " | "))
		return false
	}
	code, result := s.call(ctx, "POST", "/v1/closes/"+closeID+"/signoff", map[string]any{"action": "sign", "note": "Reviewed every draft against its source and the Trial Balance; coverage decided; ready for the client's reports.", "confirmed": true, "digest": ready.Digest, "expected_decision_id": ready.DecisionID})
	if code != 200 {
		s.problem("%s %s sign-off: %d %s", c.Name, period, code, result)
		return false
	}
	s.log("  %s signed off", period)
	return true
}

// publish publishes the signed month's management pack for the client's executive.
func (s *seeder) publish(ctx context.Context, closeID string, c *seed.Client, period string) {
	out, ok := s.must(ctx, "management pack", "GET", "/v1/closes/"+closeID+"/management-pack", nil)
	if !ok {
		return
	}
	commentary := fmt.Sprintf("%s: revenue held near plan with membership dues steady and corporate wellness billing on schedule. Payroll ran at two pay dates as usual. Utilities are accrued from Metro's estimate and reversed when the bill arrives. Cash ended the month covering about six weeks of operating payments.", monthName(period))
	s.must(ctx, "publish pack", "POST", "/v1/closes/"+closeID+"/management-pack/publications", map[string]any{"action": "publish", "digest": str(field(out, "digest")), "commentary": commentary, "reason": "Monthly pack for the owner's board call", "confirmed": true})
}

func monthName(period string) string {
	t, _ := time.Parse("2006-01", period)
	return t.Format("January 2006")
}

// reopen replaces a document after sign-off, so the recorded sign-off no longer stands and the close asks to be
// reopened: the client sent a corrected payroll summary.
func (s *seeder) reopen(ctx context.Context, c *seed.Client, closeID, period string, work []seed.Work) {
	for _, w := range work {
		if w.Kind != "payroll" {
			continue
		}
		var evidenceID string
		s.db.QueryRowContext(ctx, `SELECT id::text FROM evidence WHERE close_id::text=$1 AND name=$2 ORDER BY id LIMIT 1`, closeID, w.File).Scan(&evidenceID)
		if evidenceID == "" {
			return
		}
		// The reissued summary corrects the employer taxes, so the reading and the draft it would make differ.
		reissued := *c
		payroll := *c.Payroll
		payroll.EmployerTaxes = fmt.Sprintf("%.2f", mustFloat(payroll.EmployerTaxes)+56.25)
		reissued.Payroll = &payroll
		reissued.Reconcile = false // only the payroll summary is reissued
		var corrected seed.Work
		for _, again := range reissued.WorkFor(period, seed.Books{}) {
			if again.Kind == "payroll" {
				corrected = again
			}
		}
		if corrected.Text == "" {
			return
		}
		corrected.Text = strings.Replace(corrected.Text, "Payroll summary", "Payroll summary (reissued)", 1)
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return
		}
		tx.ExecContext(ctx, `SELECT id FROM client_closes WHERE id::text=$1 FOR UPDATE`, closeID)
		id, _, _, refused := addEvidence(s.actor(ctx, s.o.Subject), tx, closeID, strings.Replace(w.File, ".txt", "-reissued.txt", 1), []byte(corrected.Text), false, "work")
		if refused != nil || tx.Commit() != nil {
			tx.Rollback()
			s.problem("reopen: reissued payroll summary refused")
			return
		}
		s.answers.Store(id, answerFor(corrected))
		s.closeOf.Store(id, closeID)
		s.must(ctx, "replace evidence", "POST", "/v1/closes/"+closeID+"/evidence/"+evidenceID+"/replace", map[string]any{"replacement_id": id, "reason": "ADP reissued the September summary with a corrected employer tax figure (up $56.25)"})
		if err := s.settle(ctx); err != nil {
			s.problem("reopen: %v", err)
		}
		return
	}
}

// september leaves the last month where the scenario says: drafts waiting for approval, a request out to the client,
// an overdue month with an open question, or documents in with no books captured.
func (s *seeder) september(ctx context.Context, c *seed.Client, closeID string, work []seed.Work, jobs map[string]string) {
	switch c.Scenario {
	case seed.WaitingClient:
		for _, w := range work {
			if w.Request == nil {
				continue
			}
			jobID := jobs[w.File]
			state, revision, _ := s.jobState(ctx, jobID)
			if state != "needs_confirmation" {
				s.problem("%s: %s is %s, so no request can go out", c.Name, w.File, state)
				continue
			}
			out, ok := s.must(ctx, "request", "POST", "/v1/closes/"+closeID+"/work/"+jobID+"/request", map[string]any{"revision": revision, "recipient": w.Request.Recipient, "body": w.Request.Body})
			if !ok {
				continue
			}
			requestID := str(field(out, "request_id"))
			if requestID == "" {
				s.db.QueryRowContext(ctx, `SELECT max(id)::text FROM work_requests WHERE job_id::text=$1`, jobID).Scan(&requestID)
			}
			_, revision, _ = s.jobState(ctx, jobID)
			if w.Request.Delivered != "" {
				s.must(ctx, "delivered", "POST", "/v1/closes/"+closeID+"/work/"+jobID+"/delivered", map[string]any{"revision": revision, "request_id": requestID, "delivery_reference": w.Request.Delivered})
				_, revision, _ = s.jobState(ctx, jobID)
				s.must(ctx, "link", "POST", "/v1/closes/"+closeID+"/work/"+jobID+"/link", map[string]any{"revision": revision, "request_id": requestID})
			}
		}
	case seed.Review, seed.Overdue, seed.Uncaptured:
		// Drafts stay unapproved; coverage and scope stay undecided.
	}
	if c.Goal {
		s.must(ctx, "goal", "POST", "/v1/closes/"+closeID+"/goal", map[string]any{"confirmed": true, "due_on": s.today.AddDate(0, 0, 6).Format("2006-01-02"), "finish": "review"})
	}
	if c.Routine {
		if _, ok := s.must(ctx, "routine", "POST", "/v1/closes/"+closeID+"/goal", map[string]any{"confirmed": true, "due_on": s.today.AddDate(0, 0, 9).Format("2006-01-02"), "finish": "signoff", "every_month": true}); ok {
			s.recordNewest(ctx, "close_routines", c.Name)
		}
	}
}

// budget imports the client's budget for the year and maps it to the ledger's accounts.
func (s *seeder) budget(ctx context.Context, c *seed.Client, books []seed.Books) {
	closeID := s.closeFor(ctx, c.Name, seed.Months[len(seed.Months)-1])
	if closeID == "" {
		return
	}
	months := []string{}
	for m := 1; m <= 12; m++ {
		months = append(months, fmt.Sprintf("2026-%02d", m))
	}
	var lines []string
	lines = append(lines, "Account,"+strings.Join(months, ","))
	activity := books[len(books)-1].Activity()
	for _, a := range c.Accounts {
		if a.Type != "Income" && a.Type != "Expense" && a.Type != "Cost of Goods Sold" {
			continue
		}
		v := activity[a.ID]
		if v == nil || v.Sign() == 0 || a.Name == "Ask My Accountant" {
			continue
		}
		base, _ := v.Float64()
		if base < 0 {
			base = -base
		}
		cells := []string{a.Name}
		for m := range months {
			cells = append(cells, fmt.Sprintf("%.2f", base*(0.92+0.015*float64(m))))
		}
		lines = append(lines, strings.Join(cells, ","))
	}
	out, ok := s.must(ctx, "budget", "POST", "/v1/closes/"+closeID+"/budget", map[string]any{"action": "import", "name": "FY2026 operating budget", "csv": strings.Join(lines, "\n") + "\n", "source": "Owner's budget workbook, approved January 2026"})
	if !ok {
		return
	}
	// The comparison reads the approved version; its lines map by name onto the chart they were taken from.
	if problems, _ := field(out, "problems").([]any); len(problems) > 0 {
		s.problem("%s budget: %v", c.Name, problems)
		return
	}
	s.recordRow(ctx, "budget_versions", str(field(out, "id")))
	s.must(ctx, "approve budget", "POST", "/v1/closes/"+closeID+"/budget", map[string]any{"action": "approve", "id": str(field(out, "id"))})
}

func (s *seeder) closeFor(ctx context.Context, entity, period string) string {
	var id string
	s.db.QueryRowContext(ctx, `SELECT id::text FROM client_closes WHERE organization_id=$1 AND lower(entity)=lower($2) AND period=$3`, s.o.Organization, entity, period).Scan(&id)
	return id
}

// cashPlan records the client's standing obligations, a scenario and a reviewed plan on the latest month.
func (s *seeder) cashPlan(ctx context.Context, c *seed.Client) {
	closeID := s.closeFor(ctx, c.Name, seed.Months[len(seed.Months)-1])
	if closeID == "" {
		return
	}
	path := "/v1/closes/" + closeID
	// Amounts are decimal, as the route takes them; each is approved so the plan counts it.
	obligations := []map[string]any{
		{"action": "add", "name": "Rent, " + c.Name, "category": "rent", "direction": "out", "amount": c.Rent(), "first_on": "2026-10-01", "every": "month", "vendor": "Lakeside Property Management", "basis": "Lease dated 2024-03-01, section 4: due on the first of each month.", "reason": "Standing obligation"},
	}
	if p := c.Payroll; p != nil {
		// Each pay date's debit as the books show it: net pay and taxes together, split across the month's pay dates.
		total := mustFloat(p.Gross) + mustFloat(p.EmployerTaxes)
		each := float64(int64(total*100/float64(len(p.PayDays)))) / 100
		first := total - each*float64(len(p.PayDays)-1)
		for i, d := range p.PayDays {
			amount := each
			if i == 0 {
				amount = first
			}
			obligations = append(obligations, map[string]any{"action": "add", "name": fmt.Sprintf("%s payroll, the %dth", p.Provider, d), "category": "payroll", "direction": "out", "amount": fmt.Sprintf("%.2f", amount), "first_on": fmt.Sprintf("2026-10-%02d", d), "every": "month", "vendor": p.Provider, "basis": fmt.Sprintf("Payroll is paid on the %dth; the provider debits net pay and taxes together.", d), "reason": "Standing obligation"})
		}
	}
	if c.Loan != nil {
		obligations = append(obligations, map[string]any{"action": "add", "name": c.Loan.Lender + " note payment", "category": "debt", "direction": "out", "amount": fmt.Sprintf("%.2f", mustFloat(c.Loan.Principal)+mustFloat(c.Loan.Interest)), "first_on": fmt.Sprintf("2026-10-%02d", c.Loan.PayDay), "every": "month", "vendor": c.Loan.Lender, "basis": c.Loan.Agreement + ": fixed monthly payment on the " + fmt.Sprint(c.Loan.PayDay) + "th.", "reason": "Standing obligation"})
	}
	obligations = append(obligations, map[string]any{"action": "add", "name": "Quarterly estimated tax", "category": "tax", "direction": "out", "amount": "11500.00", "first_on": "2027-01-15", "every": "once", "vendor": "", "basis": "Safe-harbor estimate from the 2025 return, per the owner's tax preparer.", "reason": "Standing obligation"})
	for _, o := range obligations {
		if _, ok := s.must(ctx, "obligation "+str(o["name"]), "POST", path+"/cash-obligations", o); ok {
			var id string
			s.db.QueryRowContext(ctx, `SELECT id::text FROM cash_obligations WHERE organization_id=$1 AND lower(client_entity)=lower($2) AND name=$3 AND approved_at IS NULL ORDER BY id DESC LIMIT 1`, s.o.Organization, c.Name, str(o["name"])).Scan(&id)
			s.recordRow(ctx, "cash_obligations", id)
			s.must(ctx, "approve obligation "+str(o["name"]), "POST", path+"/cash-obligations", map[string]any{"action": "approve", "id": id})
		}
	}
	// A scenario: the landscaping client's slowest customer pays a month late (its open invoice moved); the studio
	// plans an equipment purchase.
	if out, ok := s.must(ctx, "cash plan", "GET", path+"/cash-plan", nil); ok {
		var plan struct {
			Weeks []struct {
				Items []struct {
					Key, On, Label string
				} `json:"items"`
			} `json:"weeks"`
		}
		json.Unmarshal(out, &plan)
		var changes []map[string]any
		for _, w := range plan.Weeks {
			for _, item := range w.Items {
				if strings.HasPrefix(item.Key, "invoice:") && len(changes) == 0 {
					on, _ := time.Parse("2006-01-02", item.On)
					changes = append(changes, map[string]any{"kind": "move", "item": item.Key, "on": on.AddDate(0, 0, 30).Format("2006-01-02")})
				}
			}
		}
		name := "Slowest customer pays a month late"
		if c.Loan == nil {
			name, changes = "Replace the cardio equipment in November", []map[string]any{{"kind": "add", "label": "Treadmills and rowers, Rogue Fitness quote", "direction": "out", "amount": "18400.00", "first_on": "2026-11-12", "every": "once"}}
		}
		if len(changes) > 0 {
			if _, ok := s.must(ctx, "scenario", "POST", path+"/cash-scenarios", map[string]any{"action": "add", "name": name, "changes": changes}); ok {
				s.recordNewest(ctx, "cash_scenarios", c.Name)
			}
		}
	}
	if out, ok := s.must(ctx, "cash plan", "GET", path+"/cash-plan", nil); ok {
		s.must(ctx, "cash plan review", "POST", path+"/cash-plan/reviews", map[string]any{"scenario": "", "digest": str(field(out, "digest")), "assumptions": "Open invoices collected on their due dates except Fairview Parks (net 45, historically 10 days late); bills paid on due dates; payroll and the note as scheduled.", "confirmed": true})
	}
}

func mustFloat(s string) float64 { f, _ := strconv.ParseFloat(s, 64); return f }

// afterwards adds what spans clients: the September decisions already looked at, open-item follow-ups, daily
// book reviews, chats with the agent and a few rows the pages list.
func (s *seeder) afterwards(ctx context.Context) {
	// Collections and open items for the landscaping client, whose customers pay late.
	if closeID := s.closeFor(ctx, "Larkspur Landscaping Inc.", "2026-09"); closeID != "" {
		s.must(ctx, "open items", "GET", "/v1/closes/"+closeID+"/open-items", nil)
		if out, ok := s.must(ctx, "aging", "GET", "/v1/closes/"+closeID+"/aging", nil); ok {
			captureID := str(field(out, "capture_id"))
			var receivables []struct {
				Name  string
				Lines []struct{ ID, Kind, Number string }
			}
			if raw, _ := json.Marshal(field(out, "receivables")); raw != nil {
				json.Unmarshal(raw, &receivables)
			}
			for i, party := range receivables {
				for _, line := range party.Lines {
					if line.Kind != "Invoice" {
						continue
					}
					status, note, expected := "following_up", "Statement sent to "+party.Name+"'s AP on "+s.today.AddDate(0, 0, -3).Format("Jan 2")+".", ""
					if i == 0 {
						status, note, expected = "promised", party.Name+" promised payment with their next check run.", s.today.AddDate(0, 0, 9).Format("2006-01-02")
					}
					body := map[string]any{"kind": "Invoice", "item_id": line.ID, "capture_id": captureID, "status": status, "note": note, "assign": "take"}
					if expected != "" {
						body["expected_on"] = expected
					}
					if code, _ := s.call(ctx, "POST", "/v1/closes/"+closeID+"/open-items", body); code == 200 {
						s.recordNewest(ctx, "open_item_actions", "Larkspur Landscaping Inc.")
					}
					break
				}
			}
		}
		if _, ok := s.must(ctx, "contact preference", "POST", "/v1/closes/"+closeID+"/collections", map[string]any{"action": "preference", "recipient": "ap@fairview.example", "do_not_contact": true, "reason": "Tom handles the city himself; the contract forbids vendor contact with Parks staff about invoices.", "confirmed": true}); ok {
			s.recordNewest(ctx, "collection_contact_preferences", "Larkspur Landscaping Inc.")
		}
	}
	// Daily book reviews: the September captures with exceptions were looked at.
	for _, name := range []string{"Harbor Fixture LLC", "Cedar & Pine Coffee Co.", "Summit Peak Fitness LLC"} {
		closeID := s.closeFor(ctx, name, "2026-09")
		var syncID string
		s.db.QueryRowContext(ctx, `SELECT id::text FROM quickbooks_entity_syncs WHERE close_id::text=$1 ORDER BY id DESC LIMIT 1`, closeID).Scan(&syncID)
		if syncID != "" {
			s.call(ctx, "POST", "/v1/closes/"+closeID+"/daily-review", map[string]any{"sync_id": syncID, "source": "quickbooks"})
		}
	}
	// The goals stay active for the worker, which looks at them as it does any hand-over. The firm's review rules are
	// never changed: what it requires of real work it requires of the demonstration too.
	s.chats(ctx)
	// Some of the inbox has been read already; one item is snoozed to next week.
	if out, ok := s.must(ctx, "attention", "GET", "/v1/closes/attention", nil); ok {
		var inbox struct {
			Groups []struct {
				CloseID string `json:"close_id"`
				Events  []struct {
					Key string `json:"key"`
				} `json:"events"`
			} `json:"groups"`
		}
		json.Unmarshal(out, &inbox)
		// Only the demonstration's own events are marked; a real client's decisions stay as they are.
		mine := map[string]bool{}
		if rows, err := s.db.QueryContext(ctx, `SELECT id::text FROM client_closes WHERE organization_id=$1 AND lower(entity)=ANY($2)`, s.o.Organization, s.clientNames()); err == nil {
			for rows.Next() {
				var id string
				rows.Scan(&id)
				mine[id] = true
			}
			rows.Close()
		}
		var keys []string
		for _, g := range inbox.Groups {
			if !mine[g.CloseID] {
				continue
			}
			for _, item := range g.Events {
				keys = append(keys, item.Key)
			}
		}
		if len(keys) > 3 {
			if code, _ := s.call(ctx, "POST", "/v1/closes/attention", map[string]any{"action": "read", "keys": keys[len(keys)/2:]}); code == 200 {
				for _, key := range keys[len(keys)/2:] {
					s.record(ctx, "attention_keys", key)
				}
			}
			if code, _ := s.call(ctx, "POST", "/v1/closes/attention", map[string]any{"action": "snooze", "keys": keys[:1], "until": s.today.AddDate(0, 0, 5).Format(time.RFC3339)}); code == 200 {
				s.record(ctx, "attention_keys", keys[0])
			}
		}
	}
	// Model usage for the month so far, as the operations view counts it.
}

// overviewAnswer describes each September close from what the database holds for it, so a step the run could not
// take is not claimed.
func (s *seeder) overviewAnswer(ctx context.Context) string {
	rows, err := s.db.QueryContext(ctx, `SELECT c.entity,
 COALESCE((SELECT decision FROM close_signoffs WHERE close_id=c.id ORDER BY id DESC LIMIT 1),''),
 (SELECT count(*) FROM workpaper_review_state w WHERE w.close_id=c.id AND w.review_state='pending'),
 (SELECT count(*) FROM preparation_jobs j WHERE j.close_id=c.id AND j.state='needs_confirmation'),
 (SELECT count(*) FROM preparation_jobs j WHERE j.close_id=c.id AND j.state='waiting_client'),
 (SELECT count(*) FROM preparation_jobs j WHERE j.close_id=c.id AND j.state='failed'),
 EXISTS(SELECT 1 FROM evidence e WHERE e.close_id=c.id AND e.superseded_by IS NOT NULL AND e.replaced_at>COALESCE((SELECT created_at FROM close_signoffs WHERE close_id=c.id ORDER BY id DESC LIMIT 1),'-infinity')),
 EXISTS(SELECT 1 FROM quickbooks_snapshots q WHERE q.close_id=c.id),
 COALESCE(c.due_on<CURRENT_DATE,false)
 FROM client_closes c WHERE c.organization_id=$1 AND c.period='2026-09' AND lower(c.entity)=ANY($2) ORDER BY c.entity`, s.o.Organization, s.clientNames())
	if err != nil {
		return "I could not read the September closes."
	}
	defer rows.Close()
	var lines, signed []string
	open := 0
	for rows.Next() {
		var entity, decision string
		var pending, confirm, waiting, failed int
		var replaced, captured, overdue bool
		if rows.Scan(&entity, &decision, &pending, &confirm, &waiting, &failed, &replaced, &captured, &overdue) != nil {
			continue
		}
		short := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(entity, " LLC"), " PLLC"), " Inc."), " PC")
		if decision == "signed_off" && !replaced {
			signed = append(signed, short)
			continue
		}
		open++
		var parts []string
		if decision == "signed_off" && replaced {
			parts = append(parts, "was signed off, but a document was replaced since, so it needs reopening")
		}
		if overdue {
			parts = append(parts, "is past its due date")
		}
		if !captured {
			parts = append(parts, "has no books captured yet")
		}
		if pending > 0 {
			parts = append(parts, fmt.Sprintf("has %d draft(s) waiting for your approval", pending))
		}
		if confirm > 0 {
			parts = append(parts, fmt.Sprintf("has %d reading(s) waiting for you to confirm", confirm))
		}
		if waiting > 0 {
			parts = append(parts, "has a request to the client approved and waiting to be sent")
		}
		if failed > 0 {
			parts = append(parts, fmt.Sprintf("has %d document(s) I could not read", failed))
		}
		if len(parts) == 0 {
			parts = append(parts, "is open with nothing waiting")
		}
		lines = append(lines, short+" "+strings.Join(parts, ", ")+".")
	}
	answer := fmt.Sprintf("%d September closes are open. %s", open, strings.Join(lines, " "))
	if len(signed) > 0 {
		answer += " " + strings.Join(signed, " and ") + " are signed off."
	}
	return answer
}

// openCount says how many of the demonstration's September closes are open, as the closes stand.
func (s *seeder) openCount(ctx context.Context) string {
	var open int
	s.db.QueryRowContext(ctx, `SELECT count(*) FROM client_closes c WHERE organization_id=$1 AND period='2026-09' AND lower(entity)=ANY($2) AND COALESCE((SELECT decision FROM close_signoffs WHERE close_id=c.id ORDER BY id DESC LIMIT 1),'')<>'signed_off'`, s.o.Organization, s.clientNames()).Scan(&open)
	return fmt.Sprintf("%d September closes are open.", open)
}

// ownershipAnswer says, from the open closes as they are owned, which September closes are the administrator's.
func (s *seeder) ownershipAnswer(ctx context.Context) string {
	var mine, others []string
	rows, err := s.db.QueryContext(ctx, `SELECT entity,owner=$2 FROM client_closes c WHERE organization_id=$1 AND period='2026-09' AND lower(entity)=ANY($3) AND COALESCE((SELECT decision FROM close_signoffs WHERE close_id=c.id ORDER BY id DESC LIMIT 1),'')<>'signed_off' ORDER BY entity`, s.o.Organization, s.o.Subject, s.clientNames())
	if err == nil {
		for rows.Next() {
			var entity string
			var yours bool
			if rows.Scan(&entity, &yours) == nil {
				if yours {
					mine = append(mine, entity)
				} else {
					others = append(others, entity)
				}
			}
		}
		rows.Close()
	}
	switch {
	case len(others) == 0:
		return "All of them are yours: every open September close is assigned to you."
	case len(mine) == 0:
		return "None of them: every open September close is with a teammate."
	default:
		return strings.Join(mine, ", ") + " are yours. " + strings.Join(others, ", ") + " are with a teammate."
	}
}

// chats are a few conversations with the agent as the sidebar lists them: firm-wide and about one close.
func (s *seeder) chats(ctx context.Context) {
	harbor := s.closeFor(ctx, "Harbor Fixture LLC", "2026-09")
	larkspur := s.closeFor(ctx, "Larkspur Landscaping Inc.", "2026-09")
	type turn struct{ role, body string }
	sessions := []struct {
		title   string
		closeID string
		at      time.Time
		turns   []turn
	}{
		{"What is still open for September", "", s.today.Add(-26 * time.Hour), []turn{
			{"user", "What is still open for September across the firm?"},
			{"assistant", s.overviewAnswer(ctx)},
			{"user", "Which of those is mine?"},
			{"assistant", s.ownershipAnswer(ctx)},
		}},
		{"Harbor Fixture insurance schedule", harbor, s.today.Add(-5 * time.Hour), []turn{
			{"user", "Why is the insurance expense lower than last month?"},
			{"assistant", "September's insurance expense is the schedule's monthly share of the Northstar Mutual policy, $400.00, as it was in August. The July figure was higher because the annual premium of $4,800.00 was posted to Insurance when the bill arrived, and the July draft moved $4,400.00 to Prepaid Expenses."},
		}},
		{"Fairview retainage", larkspur, s.today.Add(-50 * time.Hour), []turn{
			{"user", "How should we treat the 5% retainage in the Fairview amendment?"},
			{"assistant", "I have not prepared anything for it: retainage is a treatment the firm decides. The amendment holds back 5% of each invoice until the trail planting is accepted, expected 2026-11-30. If the September invoice was billed gross, the retained part is a receivable that will not be collected before acceptance; if it was billed net, there is unbilled revenue to consider. I left the question on the September work so you can answer it with Tom."},
		}},
	}
	for _, session := range sessions {
		var closeID any
		if session.closeID != "" {
			closeID = session.closeID
		}
		var sessionID string
		if err := s.db.QueryRowContext(ctx, `INSERT INTO chat_sessions(organization_id,author,close_id,executive,title,renamed,created_at,last_at) VALUES($1,$2,NULLIF($3,'')::bigint,false,$4,false,$5,$6) RETURNING id::text`, s.o.Organization, s.o.Subject, str(closeID), session.title, session.at, session.at.Add(time.Duration(len(session.turns))*time.Minute)).Scan(&sessionID); err != nil {
			s.problem("chat session: %v", err)
			continue
		}
		if id, err := strconv.ParseInt(sessionID, 10, 64); err == nil {
			s.record(ctx, "chat_session_ids", id)
		}
		for i, t := range session.turns {
			citations := "[]"
			if t.role == "assistant" && session.closeID != "" {
				citations = fmt.Sprintf(`[{"label":"Close overview","href":"/dashboard/closes/%s"}]`, session.closeID)
			}
			var messageID string
			if err := s.db.QueryRowContext(ctx, `INSERT INTO chat_messages(organization_id,author,close_id,role,body,citations,every_month,closes,replayable,executive,session_id,created_at) VALUES($1,$2,NULLIF($3,'')::bigint,$4,$5,$6::jsonb,false,'{}',false,false,$7::bigint,$8) RETURNING id::text`,
				s.o.Organization, s.o.Subject, str(closeID), t.role, t.body, citations, sessionID, session.at.Add(time.Duration(i)*time.Minute)).Scan(&messageID); err == nil {
				s.recordRow(ctx, "chat_messages", messageID)
			}
		}
	}
}

type seedStatement struct {
	sql  string
	args []any
}

// RemoveSeed takes the demonstration out of the firm by its record: the closes it made with what hangs off them, the
// demonstration clients' roster entries, aliases, procedures, plans and decisions, the chats, marks and rows it
// added, and the firm's policy as it was. The firm and its members stay.
func RemoveSeed(ctx context.Context, db *sql.DB, firm seed.Firm, o SeedOptions) error {
	var subject string
	var members []string
	var createdFirm bool
	var policy []byte
	var closes, chats, invitations, roleChanges, agentAccess, procedures []int64
	var keys []string
	err := db.QueryRowContext(ctx, `SELECT subject,members,created_firm,policy,close_ids,chat_session_ids,invitation_ids,role_change_ids,agent_access_ids,procedure_ids,attention_keys FROM demonstration_seeds WHERE organization_id=$1`, o.Organization).Scan(&subject, &members, &createdFirm, &policy, &closes, &chats, &invitations, &roleChanges, &agentAccess, &procedures, &keys)
	if err == sql.ErrNoRows {
		return errors.New("this firm holds no demonstration to remove")
	}
	if err != nil {
		return fmt.Errorf("the seed's record could not be read: %w", err)
	}
	names := []string{}
	for _, c := range firm.Clients {
		names = append(names, strings.ToLower(c.Name))
	}
	// Every close of the demonstration's clients is the seed's (the firm had none under those names), so a run that
	// stopped before it recorded them is still taken out whole. Newest first, so a connection's later months go before
	// the month that holds it.
	// Only the recorded closes: a close someone opened under a demonstration name, during the run or after, is theirs.
	rows, err := db.QueryContext(ctx, `SELECT id::text FROM client_closes WHERE organization_id=$1 AND id=ANY($2) ORDER BY period DESC,id DESC`, o.Organization, closes)
	if err != nil {
		return err
	}
	var ordered []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ordered = append(ordered, id)
	}
	rows.Close()
	// A legal hold or a retention date still to come protects a close, demonstration or not.
	var held []string
	rows, err = db.QueryContext(ctx, `SELECT entity||' '||period FROM client_closes WHERE organization_id=$1 AND id::text=ANY($2) AND (legal_hold OR retain_until>CURRENT_DATE) ORDER BY entity,period`, o.Organization, ordered)
	if err != nil {
		return fmt.Errorf("retention could not be read: %w", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		held = append(held, name)
	}
	rows.Close()
	if len(held) > 0 {
		return fmt.Errorf("a legal hold or retention date protects %s; lift it under the close's Settings before removing the demonstration", strings.Join(held, ", "))
	}
	// An email still on its way out (a request's or a collection message's) is kept until its outcome is known, as the
	// deletion route keeps it.
	var inFlight int
	db.QueryRowContext(ctx, `SELECT count(*) FROM email_outbox m WHERE m.state IN ('queued','sending','unknown') AND (m.request_id IN (SELECT q.id FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id::text=ANY($1)) OR m.collection_id IN (SELECT id FROM collection_messages WHERE close_id::text=ANY($1)))`, ordered).Scan(&inFlight)
	if inFlight > 0 {
		return fmt.Errorf("%d email(s) from the demonstration's closes are still being sent; wait for their outcome before removing it", inFlight)
	}
	// A posting or correction whose outcome the provider has not yet confirmed is kept until it is, as the deletion
	// route keeps it.
	var unresolved int
	db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM production_postings WHERE close_id::text=ANY($1) AND state IN ('executing','unknown'))
 +(SELECT count(*) FROM sandbox_postings WHERE close_id::text=ANY($1) AND state IN ('executing','unknown'))
 +(SELECT count(*) FROM xero_postings WHERE close_id::text=ANY($1) AND state IN ('executing','unknown'))
 +(SELECT count(*) FROM vendor_credit_postings WHERE close_id::text=ANY($1) AND state IN ('executing','unknown'))
 +(SELECT count(*) FROM bill_corrections WHERE close_id::text=ANY($1) AND state IN ('executing','unknown'))
 +(SELECT count(*) FROM credit_applications WHERE close_id::text=ANY($1) AND state IN ('executing','unknown'))
 +(SELECT count(*) FROM xero_corrections WHERE close_id::text=ANY($1) AND state IN ('executing','unknown'))`, ordered).Scan(&unresolved)
	if unresolved > 0 {
		return fmt.Errorf("%d posting(s) or correction(s) on the demonstration's closes have an outcome still to confirm; resolve them before removing it", unresolved)
	}
	// The client-scoped rows the run recorded as it made them; anything else under those names is someone else's.
	recorded := map[string][]string{}
	var before map[string]any
	json.Unmarshal(policy, &before)
	if rowsByTable, ok := before["rows"].(map[string]any); ok {
		for table, list := range rowsByTable {
			if ids, ok := list.([]any); ok {
				for _, id := range ids {
					recorded[table] = append(recorded[table], fmt.Sprint(id))
				}
			}
		}
	}
	// Work that depends on the demonstration's rows and is not the demonstration's keeps them: a budget based on a
	// seeded version, or a goal the monthly routine made on a retained close.
	var dependents int
	if err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM budget_versions WHERE organization_id=$1 AND based_on::text=ANY($2) AND NOT id::text=ANY($2))
 +(SELECT count(*) FROM close_goals g WHERE g.routine_id::text=ANY($3) AND NOT g.close_id::text=ANY($4))`, o.Organization, recorded["budget_versions"], recorded["close_routines"], ordered).Scan(&dependents); err != nil {
		return err
	}
	if dependents > 0 {
		return fmt.Errorf("%d budget(s) or goal(s) outside the demonstration build on its rows; remove those first", dependents)
	}
	// Everything goes in one transaction, so a refusal partway leaves the demonstration whole.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ordered {
		if err := removeClose(ctx, tx, id, chats, subject); err != nil {
			return fmt.Errorf("close %s: %w", id, err)
		}
	}
	// A demonstration client with a close of someone else's keeps its setup: roster entry, assignments, aliases,
	// routine and connection stay with that close.
	var retained []string
	if rows, err := tx.QueryContext(ctx, `SELECT DISTINCT lower(entity) FROM client_closes WHERE organization_id=$1 AND lower(entity)=ANY($2)`, o.Organization, names); err == nil {
		for rows.Next() {
			var name string
			rows.Scan(&name)
			retained = append(retained, name)
		}
		rows.Close()
	}
	statements := []seedStatement{}
	for _, table := range []string{"client_roster", "client_access", "budget_versions", "cash_obligations", "cash_scenarios", "open_item_actions", "collection_contact_preferences", "close_routines"} {
		if ids := recorded[table]; len(ids) > 0 {
			statements = append(statements, seedStatement{fmt.Sprintf(`DELETE FROM %s WHERE organization_id=$1 AND id::text=ANY($2) AND NOT lower(client_entity)=ANY($3)`, table), []any{o.Organization, ids, retained}})
		}
	}
	for _, pair := range recorded["client_aliases"] {
		if entity, alias, ok := strings.Cut(pair, " | "); ok && !slices.Contains(retained, strings.ToLower(entity)) {
			statements = append(statements, seedStatement{`DELETE FROM client_aliases WHERE organization_id=$1 AND client_entity=$2 AND alias=$3`, []any{o.Organization, entity, alias}})
		}
	}
	if ids := recorded["quickbooks_connections"]; len(ids) > 0 {
		statements = append(statements, seedStatement{`DELETE FROM quickbooks_connections WHERE organization_id=$1 AND id::text=ANY($2) AND NOT lower(client_entity)=ANY($3) AND NOT EXISTS(SELECT 1 FROM quickbooks_snapshots WHERE connection_id=quickbooks_connections.id) AND NOT EXISTS(SELECT 1 FROM quickbooks_entity_syncs WHERE connection_id=quickbooks_connections.id)`, []any{o.Organization, ids, retained}})
	}
	// The seed's own chat messages go; a session that still holds the administrator's later turns stays with them.
	statements = append(statements,
		seedStatement{`DELETE FROM chat_messages WHERE organization_id=$1 AND id::text=ANY($2)`, []any{o.Organization, recorded["chat_messages"]}},
		seedStatement{`DELETE FROM chat_sessions WHERE organization_id=$1 AND id=ANY($2) AND NOT EXISTS(SELECT 1 FROM chat_messages m WHERE m.session_id=chat_sessions.id)`, []any{o.Organization, chats}},
	)
	statements = append(statements,
		seedStatement{`DELETE FROM procedure_versions WHERE organization_id=$1 AND id=ANY($2) AND NOT EXISTS(SELECT 1 FROM evidence e WHERE e.procedure_id=procedure_versions.id)`, []any{o.Organization, procedures}},
		seedStatement{`DELETE FROM attention_marks WHERE organization_id=$1 AND subject=$2 AND key=ANY($3)`, []any{o.Organization, subject, keys}},
		seedStatement{`DELETE FROM member_invitations WHERE organization_id=$1 AND id=ANY($2)`, []any{o.Organization, invitations}},
		seedStatement{`DELETE FROM member_role_changes WHERE organization_id=$1 AND id=ANY($2)`, []any{o.Organization, roleChanges}},
		seedStatement{`DELETE FROM agent_access WHERE organization_id=$1 AND id=ANY($2)`, []any{o.Organization, agentAccess}},
	)
	for _, st := range statements {
		if _, err := tx.ExecContext(ctx, st.sql, st.args...); err != nil {
			return fmt.Errorf("%s: %w", st.sql[:40], err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM demonstration_seeds WHERE organization_id=$1`, o.Organization); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// The firm's policy as the seed found it, recorded as a change by the person who ran the seed; the review rules
	// were never changed, and the local firm cannot take them.
	if before != nil {
		s := &seeder{db: db, mux: savedCloses(db, strings.Repeat("s", 32)), o: SeedOptions{Organization: o.Organization, Subject: subject}}
		// Only what the seed set, and only where the firm has left it as the seed set it: a later decision stands.
		var now map[string]any
		var raw []byte
		if db.QueryRowContext(ctx, `SELECT to_jsonb(o) FROM (SELECT require_link_passcode,COALESCE(materiality_amount::text,'') AS materiality_amount,reminder_after_days,reminder_max,agent_chat FROM organizations WHERE id=$1) o`, o.Organization).Scan(&raw) == nil {
			json.Unmarshal(raw, &now)
		}
		restore := map[string]any{"reason": "The demonstration was removed; the firm's policy is as it was before it"}
		changed := map[string]bool{}
		if list, ok := before["changed"].([]any); ok {
			for _, field := range list {
				changed[fmt.Sprint(field)] = true
			}
		}
		for field, set := range seedPolicy {
			if changed[field] && fmt.Sprint(now[field]) == fmt.Sprint(set) && fmt.Sprint(before[field]) != fmt.Sprint(set) {
				restore[policyInputs[field]] = before[field]
			}
		}
		if len(restore) == 1 {
			restore = nil
		}
		if restore != nil {
			s.must(ctx, "restore policy", "POST", "/v1/closes/organization/policy", restore)
		}
		if stopped, ok := before["agent_chat_stopped"].(bool); ok && changed["agent_chat"] && fmt.Sprint(now["agent_chat"]) == fmt.Sprint(seedPolicy["agent_chat"]) {
			// The notice that the agent was stopped by a migration is not a policy the route sets; it is put back as found,
			// unless the firm has since chosen an agent level of its own.
			db.ExecContext(ctx, `UPDATE organizations SET agent_chat_stopped=$2 WHERE id=$1`, o.Organization, stopped)
		}
		// A monthly template, once set, has a due date of 1–60 days: a firm that had none keeps the demonstration's
		// 15 days until it chooses its own (docs/seed-data.md says so).
		// The settings the seed made for the administrator go only while they still hold the seed's values.
		if set, _ := before["set_time_zone"].(bool); set {
			db.ExecContext(ctx, `DELETE FROM member_settings WHERE organization_id=$1 AND subject=$2 AND time_zone='America/New_York'`, o.Organization, subject)
		}
		if set, _ := before["set_preferences"].(bool); set {
			db.ExecContext(ctx, `DELETE FROM attention_preferences WHERE organization_id=$1 AND subject=$2 AND NOT browser AND kinds <@ $3::text[] AND kinds @> $3::text[]`, o.Organization, subject, seedKinds)
		}
		if len(s.problems) > 0 {
			return errors.New(s.problems[0])
		}
	}
	return nil
}

// removeClose deletes one close and everything that hangs off it, in the order the deletion route uses.
func removeClose(ctx context.Context, tx *sql.Tx, closeID string, seedSessions []int64, actor string) error {
	// The close is locked and its hold checked again here, under the deletion's own transaction, before anything goes.
	var org, entity, period, client, plainClient string
	var postings int
	var held bool
	if err := tx.QueryRowContext(ctx, `SELECT organization_id,entity,period,client_identity(organization_id,entity),trim(regexp_replace(client_identity(organization_id,entity),'[^[:alnum:]]+',' ','g')),(SELECT count(*) FROM production_postings WHERE close_id=client_closes.id),legal_hold OR COALESCE(retain_until>CURRENT_DATE,false) FROM client_closes WHERE id::text=$1 FOR UPDATE`, closeID).Scan(&org, &entity, &period, &client, &plainClient, &postings, &held); err != nil {
		return fmt.Errorf("read the close: %w", err)
	}
	if held {
		return fmt.Errorf("a legal hold or retention date now protects %s %s", entity, period)
	}
	// The deletion is receipted as the deletion route receipts one: the demonstration's relationship with the client ends.
	if _, err := tx.ExecContext(ctx, `INSERT INTO close_deletion_receipts(organization_id,close_id,entity_digest,period,production_postings,actor,reason_code,client_digest,plain_digest) VALUES($1,$2::bigint,$3,$4,$5,$6,'contract_termination',$7,$8)`,
		org, closeID, preparation.Digest([]byte(org+"\x00"+strings.ToLower(entity)+"\x00"+period)), period, postings, actor, preparation.Digest([]byte(org+"\x00"+client+"\x00"+period)), preparation.Digest([]byte(org+"\x00"+plainClient+"\x00"+period))); err != nil {
		return fmt.Errorf("receipt: %w", err)
	}
	// A member's own conversation that mentions the close keeps its messages, detached from it; only the seed's chats
	// and the chats held inside the close go with it.
	// An action card the agent offered on the close cannot stand without it (chat_action_scope), so it goes with the close.
	if _, err := tx.ExecContext(ctx, `DELETE FROM chat_messages m USING chat_sessions s WHERE s.id=m.session_id AND s.close_id IS NULL AND NOT s.id=ANY($2::bigint[]) AND m.action IS NOT NULL AND (m.close_id=$1::bigint OR m.target_close_id=$1::bigint)`, closeID, seedSessions); err != nil {
		return fmt.Errorf("detach chat actions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE chat_messages m SET close_id=NULLIF(m.close_id,$1::bigint),target_close_id=NULLIF(m.target_close_id,$1::bigint),closes=array_remove(m.closes,$1::bigint) FROM chat_sessions s
 WHERE s.id=m.session_id AND s.close_id IS NULL AND NOT s.id=ANY($2::bigint[]) AND (m.close_id=$1::bigint OR m.target_close_id=$1::bigint OR $1::bigint=ANY(m.closes))`, closeID, seedSessions); err != nil {
		return fmt.Errorf("detach chats: %w", err)
	}
	for _, statement := range []string{
		`DELETE FROM sandbox_postings WHERE close_id=$1`,
		`DELETE FROM procedure_example_approvals WHERE workpaper_id IN (SELECT id FROM workpapers WHERE close_id=$1)`,
		`DELETE FROM approval_requests WHERE close_id=$1`,
		`DELETE FROM standing_approvals WHERE workpaper_id IN (SELECT id FROM workpapers WHERE close_id=$1)`,
		`DELETE FROM workpaper_comments WHERE workpaper_id IN (SELECT id FROM workpapers WHERE close_id=$1)`,
		`DELETE FROM management_acknowledgments WHERE publication_id IN (SELECT id FROM management_publications WHERE close_id=$1)`,
		`DELETE FROM management_publications WHERE close_id=$1`,
		`DELETE FROM cash_plan_reviews WHERE close_id=$1`,
		`DELETE FROM cash_flow_imports WHERE close_id=$1`,
		`DELETE FROM chat_messages WHERE close_id=$1 OR target_close_id=$1 OR $1=ANY(closes)`,
		`DELETE FROM chat_sessions WHERE close_id=$1`,
		`DELETE FROM portfolio_items WHERE close_id=$1`,
		`DELETE FROM statement_extractions WHERE close_id=$1`,
		`DELETE FROM goal_handoffs WHERE goal_id IN (SELECT id FROM close_goals WHERE close_id=$1)`,
		`DELETE FROM close_goals WHERE close_id=$1`,
		`DELETE FROM coverage_bindings WHERE close_id=$1`,
		`DELETE FROM collection_messages WHERE close_id=$1`,
		`DELETE FROM work_replies WHERE request_id IN (SELECT q.id FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id=$1)`,
		`DELETE FROM request_submissions WHERE request_id IN (SELECT q.id FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id=$1)`,
		`DELETE FROM client_request_links WHERE request_id IN (SELECT q.id FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id=$1)`,
		`DELETE FROM work_requests WHERE draft_id IN (SELECT id FROM request_drafts WHERE close_id=$1)`,
		`DELETE FROM request_drafts WHERE close_id=$1`,
		`DELETE FROM preparation_jobs WHERE close_id=$1`,
		`DELETE FROM production_postings WHERE close_id=$1`,
		`DELETE FROM workpapers WHERE close_id=$1`,
		`DELETE FROM daily_reviews WHERE close_id=$1`,
		`DELETE FROM bill_corrections WHERE close_id=$1`,
		`DELETE FROM credit_applications WHERE close_id=$1`,
		`DELETE FROM vendor_credit_postings WHERE close_id=$1`,
		`DELETE FROM xero_corrections WHERE close_id=$1`,
		`DELETE FROM xero_postings WHERE close_id=$1`,
		`DELETE FROM quickbooks_entity_syncs WHERE close_id=$1`,
		`DELETE FROM quickbooks_snapshots WHERE close_id=$1`,
		`DELETE FROM forwarded_files WHERE close_id=$1`,
		`DELETE FROM forwarding_links WHERE close_id=$1`,
		`DELETE FROM production_posting_grants WHERE connection_id IN (SELECT id FROM quickbooks_connections WHERE close_id=$1)`,
		`DELETE FROM quickbooks_connections WHERE close_id=$1 AND NOT EXISTS(SELECT 1 FROM quickbooks_snapshots s WHERE s.connection_id=quickbooks_connections.id) AND NOT EXISTS(SELECT 1 FROM quickbooks_entity_syncs s WHERE s.connection_id=quickbooks_connections.id)`,
		`UPDATE quickbooks_connections SET close_id=(SELECT min(s.close_id) FROM quickbooks_snapshots s WHERE s.connection_id=quickbooks_connections.id) WHERE close_id=$1`,
		`DELETE FROM xero_transaction_rows WHERE snapshot_id IN (SELECT id FROM xero_snapshots WHERE close_id=$1)`,
		`DELETE FROM xero_snapshots WHERE close_id=$1`,
		`DELETE FROM xero_posting_grants WHERE connection_id IN (SELECT id FROM xero_connections WHERE close_id=$1)`,
		`DELETE FROM xero_connections WHERE close_id=$1`,
		`DELETE FROM stripe_report_runs WHERE close_id=$1`,
		`DELETE FROM stripe_connections WHERE close_id=$1`,
		`DELETE FROM client_attachments WHERE close_id=$1`,
		`UPDATE procedure_drafts SET source_evidence_id=NULL,source_digest='',source_quote='' WHERE source_evidence_id IN (SELECT id FROM evidence WHERE close_id=$1)`,
		`DELETE FROM evidence WHERE close_id=$1`,
		`DELETE FROM preparation_plans WHERE close_id=$1`,
		`DELETE FROM close_preparations WHERE close_id=$1`,
		`DELETE FROM close_signoffs WHERE close_id=$1`,
		`DELETE FROM close_effort WHERE close_id=$1`,
		`DELETE FROM close_effort_entries WHERE close_id=$1`,
		`DELETE FROM close_scopes WHERE close_id=$1`,
		`DELETE FROM close_owner_changes WHERE close_id=$1`,
		`DELETE FROM opening_baselines WHERE close_id=$1`,
		`DELETE FROM takeover_setups WHERE close_id=$1`,
		`DELETE FROM close_export_labels WHERE close_id=$1`,
		`DELETE FROM operations_alert_deliveries WHERE close_id=$1`,
		`DELETE FROM client_closes WHERE id=$1`,
	} {
		if _, err := tx.ExecContext(ctx, statement, closeID); err != nil {
			return fmt.Errorf("%s: %w", statement, err)
		}
	}
	return nil
}
