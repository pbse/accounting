// Command acceptance runs a corpus of labeled client-months through the real pipeline and scores the result
// (docs/acceptance-corpus.md). It only uses a local, migrated database holding no closes, so its queue steps take no one
// else's work and nothing hosted is touched; it exits 1 when the run finds a critical failure.
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
	"net/url"
	"os"

	"close.local/backend/internal/acceptance"
	"close.local/backend/internal/llm"
	"close.local/backend/internal/transport"
)

func main() {
	corpus := flag.String("corpus", "", "the corpus directory: one directory per case, each holding case.json")
	database := flag.String("database", os.Getenv("ACCEPTANCE_DATABASE_URL"), "a local, migrated PostgreSQL database holding no closes")
	model := flag.String("model", "recorded", "recorded: replay each document's recorded answer; live: the model configured in the environment")
	reportPath := flag.String("report", "", "write the full report, with every observation, as JSON here")
	flag.Parse()
	if *corpus == "" || *database == "" || (*model != "recorded" && *model != "live") {
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
	var live *llm.Client
	if *model == "live" {
		if live, err = llm.FromEnv(); err != nil {
			log.Fatal(err)
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
		o, err := transport.RunAcceptanceCase(ctx, db, c, func(files map[string]string) (*llm.Client, error) {
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
	if report.Critical() > 0 {
		os.Exit(1)
	}
}

// local refuses any database that is not on this machine: the run creates firms and works the whole queue.
func local(database string) error {
	u, err := url.Parse(database)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return fmt.Errorf("give the database as a postgres:// URL")
	}
	for _, host := range []string{u.Hostname(), u.Query().Get("host")} {
		ip := net.ParseIP(host)
		if host != "" && host != "localhost" && !(ip != nil && ip.IsLoopback()) && !(len(host) > 0 && host[0] == '/') {
			return fmt.Errorf("the acceptance run uses only a local database (localhost, a loopback address or a socket), not %s", host)
		}
	}
	return nil
}
