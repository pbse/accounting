// Command seed fills a firm with the demonstration data of internal/seed (docs/seed-data.md): ten clients, four
// months of books, documents, the agent's work and the firm's decisions, written through the same routes a person
// uses. It refuses a database that is not on this machine unless -hosted is given, and then asks for -yes.
//
//	seed -database URL (-organization ORG | -domain DOMAIN) (-subject SUBJECT | -email EMAIL) [-members a,b] [-hosted -yes] [-remove]
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"strings"

	"close.local/backend/internal/seed"
	"close.local/backend/internal/transport"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	database := flag.String("database", os.Getenv("SEED_DATABASE_URL"), "the database to fill (a migrated PostgreSQL)")
	organization := flag.String("organization", "", "the firm's organization ID")
	domain := flag.String("domain", "", "the firm's email domain, to find its organization")
	subject := flag.String("subject", "", "the administrator's subject (WorkOS user ID)")
	email := flag.String("email", "", "the administrator's email, to find their subject from their sign-ins")
	members := flag.String("members", "", "comma-separated subjects of other members who own some clients; default: every other current member")
	firmName := flag.String("firm-name", "Demonstration firm", "the firm's name when the seed has to create it")
	hosted := flag.Bool("hosted", false, "allow a database that is not on this machine")
	yes := flag.Bool("yes", false, "with -hosted: proceed without asking")
	remove := flag.Bool("remove", false, "take the demonstration clients out instead of adding them")
	flag.Parse()
	if *database == "" || (*organization == "" && *domain == "") || (!*remove && *subject == "" && *email == "") {
		flag.Usage()
		os.Exit(2)
	}
	if err := local(*database); err != nil && !*hosted {
		log.Fatalf("%v (pass -hosted to seed a hosted database)", err)
	}
	db, err := sql.Open("pgx", *database)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("database: %v", err)
	}
	org, name := *organization, ""
	if org == "" {
		if err := db.QueryRowContext(ctx, `SELECT id,name FROM organizations WHERE email_domain=$1`, strings.ToLower(*domain)).Scan(&org, &name); err != nil {
			log.Fatalf("no firm is registered under the domain %s: %v", *domain, err)
		}
	} else {
		db.QueryRowContext(ctx, `SELECT name FROM organizations WHERE id=$1`, org).Scan(&name)
	}
	sub := *subject
	if sub == "" && *email != "" {
		if err := db.QueryRowContext(ctx, `SELECT subject FROM auth_sessions WHERE organization_id=$1 AND lower(email)=lower($2) ORDER BY created_at DESC LIMIT 1`, org, *email).Scan(&sub); err != nil {
			log.Fatalf("%s has not signed in to this firm, so their subject is unknown: %v (pass -subject)", *email, err)
		}
	}
	var others []string
	if *members != "" {
		for _, m := range strings.Split(*members, ",") {
			if m = strings.TrimSpace(m); m != "" && m != sub {
				others = append(others, m)
			}
		}
	} else {
		rows, err := db.QueryContext(ctx, `SELECT subject FROM current_organization_members WHERE organization_id=$1 AND granted AND subject<>$2 ORDER BY subject`, org, sub)
		if err == nil {
			for rows.Next() {
				var m string
				if rows.Scan(&m) == nil {
					others = append(others, m)
				}
			}
			rows.Close()
		}
	}
	firm := seed.Build()
	options := transport.SeedOptions{Organization: org, Subject: sub, Members: others, FirmName: *firmName, Hosted: *hosted, Log: func(format string, args ...any) { fmt.Printf(format+"\n", args...) }}
	if *remove {
		fmt.Printf("Removing the demonstration from %s (%s)\n", org, name)
		if *hosted && !*yes {
			log.Fatal("pass -yes to change a hosted database")
		}
		if err := transport.RemoveSeed(ctx, db, firm, options); err != nil {
			log.Fatal(err)
		}
		fmt.Println("Removed.")
		return
	}
	fmt.Printf("Firm: %s (%s)\nAdministrator: %s\nOther members: %s\nClients: %d, months %s to %s\n", org, name, sub, strings.Join(others, ", "), len(firm.Clients), seed.Months[0], seed.Months[len(seed.Months)-1])
	if *hosted && !*yes {
		log.Fatal("pass -yes to change a hosted database")
	}
	problems, err := transport.SeedFirm(ctx, db, firm, options)
	for _, p := range problems {
		fmt.Println("problem:", p)
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Done with %d problems.\n", len(problems))
	if len(problems) > 0 {
		os.Exit(1)
	}
}

// local refuses any database that is not on this machine, unless -hosted says so. The connection string is read the
// way the driver reads it (a URL or key=value pairs), and anything that cannot be placed is refused.
func local(database string) error {
	config, err := pgx.ParseConfig(database)
	if err != nil {
		return fmt.Errorf("database connection string: %w", err)
	}
	host := config.Host
	if host == "localhost" || strings.HasPrefix(host, "/") {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("%q is not a local database", host)
}
