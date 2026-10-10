// Command agent-eval runs the agent chat's golden set (docs/agent-chat.md, "Evaluation and acceptance"). A recording
// run (-record) prepares each acceptance case the set asks about as a close in a firm of its own on a fresh local
// database and makes every expected read as the firm's member, saving what the routes answered under reads/. A replay
// run (the default) sends each question through the real loop to the configured provider, answers the loop's reads
// from the record, and scores tool choice, facts, citations, cards and refusals. It spends model calls, so it runs on
// demand and before a release, not in CI. It exits 1 when the run fails the release bar.
//
//	agent-eval -set DIR -database URL [-record -corpus DIR] [-only PREFIX] [-report report.json]
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"close.local/backend/internal/acceptance"
	"close.local/backend/internal/agenteval"
	"close.local/backend/internal/llm"
	"close.local/backend/internal/transport"
	"github.com/jackc/pgx/v5"
)

func main() {
	set := flag.String("set", "acceptance/agent", "the golden set directory: golden.json and reads/")
	corpus := flag.String("corpus", "acceptance/sample", "the acceptance corpus the set's cases come from (recording only)")
	database := flag.String("database", os.Getenv("ACCEPTANCE_DATABASE_URL"), "a local, migrated PostgreSQL database; a recording run needs one holding no closes")
	record := flag.Bool("record", false, "prepare the cases and record every expected read instead of replaying")
	only := flag.String("only", "", "replay only the questions whose id starts with this; a recording records every case one of them asks about, whole")
	reportPath := flag.String("report", "", "write the full report, with every observation, as JSON here")
	profile := flag.String("profile", "", "replay every recorded fixture as this single configured access_profile on a local context machine")
	flag.Parse()
	if *set == "" || *database == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := local(*database); err != nil {
		log.Fatal(err)
	}
	golden, records, err := agenteval.Load(*set)
	if err != nil {
		log.Fatal(err)
	}
	db, err := transport.OpenTenantDB(*database, "*")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	var migrated bool
	if err = db.QueryRowContext(ctx, `SELECT to_regclass('chat_messages') IS NOT NULL AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name='organizations' AND column_name='agent_chat')`).Scan(&migrated); err != nil || !migrated {
		log.Fatal("Migrate the database first (infra/postgres/migrate.sh).")
	}
	if *record {
		recordReads(ctx, db, golden, *corpus, *only)
		return
	}
	client, err := llm.FromEnv()
	if err != nil {
		log.Fatal(err)
	}
	if llm.Model(llm.WithClient(ctx, client), "chat") == "" {
		log.Fatal("a replay needs the chat model configured (LLM_PROVIDER with its OPENAI_* or CONTEXT_MACHINE_* settings)")
	}
	profiles := llm.CompletionProfiles(llm.WithClient(ctx, client))
	if len(profiles) > 0 {
		if err = localContextMachine(client.ContextMachineEndpoint()); err != nil {
			log.Fatal(err)
		}
	}
	var owner *llm.Scope
	if *profile != "" {
		for _, p := range profiles {
			if p.AccessProfile == *profile && p.Subject != llm.AnyMember {
				p := p
				owner = &p
			}
		}
		if owner == nil {
			log.Fatal("-profile must name an exact member profile in the local context-machine configuration")
		}
	}
	var observed []agenteval.Observed
	for _, q := range golden.Questions {
		if !strings.HasPrefix(q.ID, *only) {
			continue
		}
		rec, ok := records[q.Case]
		if !ok {
			log.Fatalf("question %s: case %s has no recorded reads; run with -record first", q.ID, q.Case)
		}
		if owner != nil {
			rec.Organization, rec.Subject = owner.Organization, owner.Subject
		}
		if len(profiles) > 0 && !llm.Allows(llm.WithIdentity(llm.WithClient(ctx, client), rec.Organization, rec.Subject), rec.Organization) {
			log.Fatal("fixture identity is not configured; use -profile with a single local evaluation owner")
		}
		if err = transport.EnsureAgentEvalFirm(ctx, db, rec); err != nil {
			log.Fatal(err)
		}
		began := time.Now()
		o := transport.RunAgentEvalQuestion(ctx, db, q, rec, client)
		observed = append(observed, o)
		log.Printf("%s: %s in %s, %d rounds, %d reads: %s", q.ID, o.Outcome, time.Since(began).Round(time.Second), o.Rounds, o.Reads, firstLine(o.Answer))
	}
	if *only != "" {
		kept := golden.Questions[:0]
		for _, q := range golden.Questions {
			if strings.HasPrefix(q.ID, *only) {
				kept = append(kept, q)
			}
		}
		golden.Questions = kept
	}
	report := agenteval.Score(golden, observed)
	fmt.Print(report.Markdown())
	if *reportPath != "" {
		raw, _ := json.MarshalIndent(report, "", " ")
		if err = os.WriteFile(*reportPath, raw, 0o600); err != nil {
			log.Fatal(err)
		}
	}
	if !report.Passed() {
		os.Exit(1)
	}
}

// recordReads prepares every case the set asks about in a firm of its own and records each question's expected
// reads; a question that adds a document is recorded after the case's other questions, so their reads are unchanged,
// and its own reads are kept for it alone.
func recordReads(ctx context.Context, db *sql.DB, golden agenteval.Set, corpus, only string) {
	var closes int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM client_closes`).Scan(&closes); err != nil || closes > 0 {
		log.Fatal("Use a fresh, migrated database for a recording: this one holds closes.")
	}
	cases, err := acceptance.Load(corpus)
	if err != nil {
		log.Fatal(err)
	}
	byID := map[string]acceptance.Case{}
	for _, c := range cases {
		byID[c.ID] = c
	}
	today := time.Now().UTC().Format("2006-01-02")
	for _, caseID := range golden.Cases() {
		// A case is recorded whole or not at all: its reads are shared by its questions, so a partial recording
		// would leave the others answering from nothing.
		selected := false
		for _, q := range golden.Questions {
			if q.Case == caseID && strings.HasPrefix(q.ID, only) {
				selected = true
			}
		}
		if !selected {
			continue
		}
		c, ok := byID[caseID]
		if !ok {
			log.Fatalf("the set asks about case %s, which the corpus does not hold", caseID)
		}
		if err = c.RecordedComplete(); err != nil {
			log.Fatal(err)
		}
		firm := transport.AcceptanceFirm{Organization: "org_AgentEval" + regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(caseID, ""), Subject: "user_agent_eval"}
		closeID, err := transport.PrepareAgentEvalCase(ctx, db, c, firm, func(files map[string]string) (*llm.Client, error) { return c.Recorded(files) })
		if err != nil {
			log.Fatalf("case %s: %v", caseID, err)
		}
		rec := agenteval.Record{Case: caseID, Organization: firm.Organization, Subject: firm.Subject, CloseID: closeID, Entity: c.Client, Period: c.Period, Today: today}
		var questions []agenteval.Question
		for _, q := range golden.Questions {
			if q.Case == caseID {
				questions = append(questions, q)
			}
		}
		sort.SliceStable(questions, func(i, j int) bool { return questions[i].Evidence == nil && questions[j].Evidence != nil })
		for _, q := range questions {
			if q.Evidence != nil {
				if err = transport.AddAgentEvalEvidence(ctx, db, firm, closeID, *q.Evidence); err != nil {
					log.Fatalf("question %s: %v", q.ID, err)
				}
			}
			reads, err := transport.RecordAgentEvalReads(ctx, db, firm, closeID, c.Client, today, q)
			if err != nil {
				log.Fatal(err)
			}
			for _, read := range reads {
				if q.Evidence != nil {
					// The document changes what the close's reads answer; these reads are this question's own.
					read.Question = q.ID
				}
				rec.Add(read)
			}
		}
		if err = rec.Save(golden.Dir); err != nil {
			log.Fatal(err)
		}
		log.Printf("case %s: close %s, %d reads recorded", caseID, closeID, len(rec.Reads))
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

// local refuses any database that is not on this machine: the run creates firms and works the queue.
func local(database string) error {
	config, err := pgx.ParseConfig(database)
	if err != nil {
		return fmt.Errorf("the database setting does not parse: %w", err)
	}
	hosts := []string{config.Host}
	for _, fallback := range config.Fallbacks {
		hosts = append(hosts, fallback.Host)
	}
	for _, host := range hosts {
		ip := net.ParseIP(host)
		if host != "localhost" && !(ip != nil && ip.IsLoopback()) && !strings.HasPrefix(host, "/") {
			return fmt.Errorf("the golden set uses only a local database (localhost, a loopback address or a socket), not %q", host)
		}
	}
	return nil
}

// Replay carries synthetic recorded data only, but it must never send it through
// a production personal profile or create fixtures in a hosted database.
func localContextMachine(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/v1/run" {
		return fmt.Errorf("context-machine evaluation requires a local HTTP /v1/run endpoint")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && !(ip != nil && ip.IsLoopback()) {
		return fmt.Errorf("context-machine evaluation uses a local service, never the hosted personal profile")
	}
	return nil
}
