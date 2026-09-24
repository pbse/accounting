package main

import (
	"context"
	"database/sql"
	"log"
	"os"
	"time"

	"close.local/backend/internal/secrets"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	if os.Getenv("DATABASE_URL") == "" || os.Getenv("SECRETS_KEY_PREVIOUS") == "" || os.Getenv("SECRETS_KEY") == os.Getenv("SECRETS_KEY_PREVIOUS") {
		log.Fatal("Set DATABASE_URL and distinct SECRETS_KEY (new) and SECRETS_KEY_PREVIOUS (old).")
	}
	db, err := sql.Open("pgx", os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `LOCK TABLE quickbooks_connections,xero_connections,email_connections,email_outbox IN ACCESS EXCLUSIVE MODE`); err != nil {
		log.Fatal("Could not isolate rotation; no changes committed: ", err)
	}
	queries := []struct{ table, column, scope, query string }{
		{"quickbooks_connections", "credential", "qbo", `SELECT id::text,organization_id,credential FROM quickbooks_connections WHERE credential IS NOT NULL FOR UPDATE`},
		{"xero_connections", "credential", "xero", `SELECT id::text,organization_id,credential FROM xero_connections WHERE credential IS NOT NULL FOR UPDATE`},
		{"email_connections", "credential", "resend", `SELECT id::text,organization_id,credential FROM email_connections FOR UPDATE`},
		{"email_connections", "webhook_secret", "resend-webhook", `SELECT id::text,organization_id,webhook_secret FROM email_connections WHERE webhook_secret IS NOT NULL FOR UPDATE`},
		{"email_outbox", "payload", "email", `SELECT m.id,o.organization_id,m.payload FROM email_outbox m JOIN email_connections o ON o.id=m.connection_id FOR UPDATE OF m`},
	}
	count := 0
	for _, q := range queries {
		rows, e := tx.QueryContext(ctx, q.query)
		if e != nil {
			log.Fatal(e)
		}
		type change struct {
			id        string
			encrypted []byte
		}
		changes := []change{}
		for rows.Next() {
			var id, org string
			var encrypted []byte
			if e = rows.Scan(&id, &org, &encrypted); e != nil {
				break
			}
			scope := org + "/" + q.scope
			if q.scope == "qbo" || q.scope == "xero" || q.scope == "resend-webhook" || q.scope == "email" {
				scope += "/" + id
			}
			plain, openErr := secrets.OpenPrevious(scope, encrypted)
			if openErr != nil {
				e = openErr
				break
			}
			rotated, sealErr := secrets.Seal(scope, plain)
			if sealErr != nil {
				e = sealErr
				break
			}
			check, checkErr := secrets.Open(scope, rotated)
			if checkErr != nil || string(check) != string(plain) {
				e = sql.ErrNoRows
				break
			}
			changes = append(changes, change{id, rotated})
		}
		if closeErr := rows.Close(); e == nil {
			e = closeErr
		}
		if e == nil {
			e = rows.Err()
		}
		if e != nil {
			log.Fatal("Rotation aborted; no changes committed: ", e)
		}
		for _, item := range changes {
			if _, e = tx.ExecContext(ctx, `UPDATE `+q.table+` SET `+q.column+`=$1 WHERE id::text=$2`, item.encrypted, item.id); e != nil {
				log.Fatal("Rotation aborted; no changes committed: ", e)
			}
			count++
		}
	}
	if err = tx.Commit(); err != nil {
		log.Fatal("Rotation commit unknown; inspect ciphertext before retrying: ", err)
	}
	log.Printf("Re-encrypted and checked %d records. Verify provider readback before removing SECRETS_KEY_PREVIOUS.", count)
}
