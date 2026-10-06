// Command acceptance runs a corpus of labeled client-months through the real pipeline and scores the result
// (docs/acceptance-corpus.md). It only uses a local, migrated database holding no closes, so its queue steps take no one
// else's work and nothing hosted is touched. It exits 1 when the run finds a critical failure or leaves a document undecided.
//
//	acceptance -corpus DIR [-database URL] [-model recorded|live] [-report report.json]
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"regexp"
	"strings"

	"close.local/backend/internal/acceptance"
	"close.local/backend/internal/llm"
	"close.local/backend/internal/transport"
	"github.com/jackc/pgx/v5"
)

func main() {
	corpus := flag.String("corpus", "", "the corpus directory: one directory per case, each holding case.json")
	database := flag.String("database", os.Getenv("ACCEPTANCE_DATABASE_URL"), "a local, migrated PostgreSQL database holding no closes")
	model := flag.String("model", "recorded", "recorded: replay each document's recorded answer; live: the model configured in the environment")
	reportPath := flag.String("report", "", "write the full report, with every observation, as JSON here")
	organization := flag.String("organization", "", "run every case as a close of this firm, as the person -subject; for a live Context Machine run, the firm and person its access profile serves")
	subject := flag.String("subject", "", "the person the cases are worked as, with -organization")
	flag.Parse()
	if *corpus == "" || *database == "" || (*model != "recorded" && *model != "live") || (*organization == "") != (*subject == "") {
		flag.Usage()
		os.Exit(2)
	}
	if err := local(*database); err != nil {
		log.Fatal(err)
	}
	cases, err := acceptance.Load(*corpus)
	if err != nil {
		log.Fatal(err)
	}
	firm := transport.AcceptanceFirm{Organization: *organization, Subject: *subject}
	if err = oneFirm(firm, cases); err != nil {
		log.Fatal(err)
	}
	var live *llm.Client
	if *model == "live" {
		if live, err = llm.FromEnv(); err != nil {
			log.Fatal(err)
		}
	} else {
		for _, c := range cases {
			if err = c.RecordedComplete(); err != nil {
				log.Fatal(err)
			}
		}
	}
	db, err := transport.OpenTenantDB(*database, "*")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	var migrated bool
	var closes int
	if err = db.QueryRowContext(ctx, `SELECT to_regclass('preparation_intakes') IS NOT NULL AND to_regclass('workflow_runs') IS NOT NULL`).Scan(&migrated); err != nil || !migrated {
		log.Fatal("Migrate the database first (infra/postgres/migrate.sh).")
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM client_closes`).Scan(&closes); err != nil || closes > 0 {
		log.Fatal("Use a fresh, migrated database: this one holds closes, and the run's queue steps would take their work.")
	}
	observed := []acceptance.CaseObserved{}
	for _, c := range cases {
		o, err := transport.RunAcceptanceCase(ctx, db, c, firm, func(files map[string]string) (*llm.Client, error) {
			if live != nil {
				return live, nil
			}
			return c.Recorded(files)
		})
		if err != nil {
			log.Fatalf("case %s: %v", c.ID, err)
		}
		observed = append(observed, o)
	}
	report := acceptance.Score(cases, observed)
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

// oneFirm refuses a corpus that cannot run inside one named firm: there each case is a close of that firm, and a client
// has one close a month and one ledger connection, so no two cases may share a client.
func oneFirm(firm transport.AcceptanceFirm, cases []acceptance.Case) error {
	if firm.Organization == "" {
		return nil
	}
	if !regexp.MustCompile(`^org_[A-Za-z0-9]+$`).MatchString(firm.Organization) {
		return fmt.Errorf("-organization %q is not a firm ID (org_ and letters or digits)", firm.Organization)
	}
	clients := map[string]string{}
	for _, c := range cases {
		key := strings.ToLower(strings.TrimSpace(c.Client))
		if earlier, ok := clients[key]; ok {
			return fmt.Errorf("cases %s and %s share the client %q: in one firm, run them separately, each on a fresh database", earlier, c.ID, c.Client)
		}
		clients[key] = c.ID
	}
	return nil
}

// local refuses any database that is not on this machine: the run creates firms and works the whole queue. It checks
// every host the driver would connect to, as it resolves them, including PGHOST and the other PG settings.
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
			return fmt.Errorf("the acceptance run uses only a local database (localhost, a loopback address or a socket), not %q", host)
		}
	}
	return nil
}
