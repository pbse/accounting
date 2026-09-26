package transport

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/secrets"
	"close.local/backend/internal/stripe"
)

// stripeKey opens the restricted key of the client's active connection. Only a missing or inactive connection is a
// refusal; a database or server-key failure is temporary.
func stripeKey(ctx context.Context, db *sql.DB, org, entity, id string) (string, string, error) {
	var state, account string
	var encrypted []byte
	err := db.QueryRowContext(ctx, `SELECT state,account_id,COALESCE(credential,''::bytea) FROM stripe_connections WHERE id::text=$1 AND organization_id=$2 AND lower(client_entity)=lower($3)`, id, org, entity).Scan(&state, &account, &encrypted)
	if err == sql.ErrNoRows || err == nil && state != "active" {
		return "", "", refusal{409, "Connect and confirm the client's Stripe account first."}
	}
	if err != nil {
		return "", "", err
	}
	key, err := secrets.Open(org+"/stripe/"+id, encrypted)
	if err != nil {
		return "", "", errors.New("Encrypted Stripe key unavailable; check the server key")
	}
	return string(key), account, nil
}

// stripeReport checks a downloaded report fits the evidence and settlement limits and carries its parser's columns.
func stripeReport(body []byte, columns []string) error {
	if len(body) > 512*1024 || !utf8.Valid(body) {
		return errors.New("The report is larger than the 512 KiB evidence limit or is not text. Download it from Stripe in parts and upload them.")
	}
	reader := csv.NewReader(bytes.NewReader(body))
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if err != nil {
		return errors.New("The report is not a readable CSV.")
	}
	for i := range header {
		header[i] = strings.TrimSpace(strings.TrimPrefix(header[i], "\ufeff"))
	}
	for _, column := range columns {
		if !slices.Contains(header, column) {
			return errors.New("The report lacks the column " + column + ".")
		}
	}
	rows := 0
	for {
		if _, err = reader.Read(); err == io.EOF {
			break
		} else if err != nil {
			return errors.New("The report is not a readable CSV.")
		}
		if rows++; rows > 5000 {
			return errors.New("The report has more than 5,000 transactions, the settlement limit. Download it from Stripe in parts and upload them.")
		}
	}
	return nil
}

// sweepStripeReports moves up to ten report runs forward: a run whose request answer was lost is asked again with
// the same idempotency key, and a finished report is saved as evidence with the same intake job as an upload. A
// claim lasts five minutes, so a crashed worker's run is picked up again.
func sweepStripeReports(ctx context.Context, db *sql.DB) error {
	for range 10 {
		var id, closeID, connection, period, key, org, entity, actor, state, report string
		var runID sql.NullString
		err := db.QueryRowContext(ctx, `UPDATE stripe_report_runs r SET claimed_at=now() FROM client_closes c WHERE c.id=r.close_id AND r.id=(SELECT id FROM stripe_report_runs WHERE (state='pending' OR (state='requesting' AND created_at<now()-interval '1 minute')) AND (claimed_at IS NULL OR claimed_at<now()-interval '5 minutes') ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1)
 RETURNING r.id::text,r.close_id::text,r.connection_id::text,r.period,r.idempotency_key::text,r.run_id,r.state,c.organization_id,c.entity,r.actor,r.report`).Scan(&id, &closeID, &connection, &period, &key, &runID, &state, &org, &entity, &actor, &report)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		fail := func(message string) error {
			_, err := db.ExecContext(ctx, `UPDATE stripe_report_runs SET state='failed',error=$2,finished_at=now() WHERE id::text=$1 AND state IN ('requesting','pending')`, id, message)
			return err
		}
		// Checked again a minute later.
		later := func() error {
			_, err := db.ExecContext(ctx, `UPDATE stripe_report_runs SET claimed_at=now()-interval '4 minutes' WHERE id::text=$1`, id)
			return err
		}
		apiKey, _, err := stripeKey(ctx, db, org, entity, connection)
		var inactive refusal
		if errors.As(err, &inactive) {
			err = fail("Stripe was disconnected before the report was saved.")
		} else if err != nil {
			log.Print("Stripe key unavailable; the report waits: ", err)
			err = later()
		}
		if apiKey == "" {
			if err != nil {
				return err
			}
			continue
		}
		var provider stripe.ProviderError
		if state == "requesting" {
			month, _ := time.Parse("2006-01", period)
			created, err := stripe.CreateRun(ctx, apiKey, key, report, month, month.AddDate(0, 1, 0))
			switch {
			case errors.As(err, &provider):
				err = fail(provider.Message)
			case err != nil:
				err = later()
			default:
				_, err = db.ExecContext(ctx, `UPDATE stripe_report_runs SET run_id=$2,state='pending',claimed_at=now()-interval '4 minutes' WHERE id::text=$1 AND state='requesting'`, id, created)
			}
			if err != nil {
				return err
			}
			continue
		}
		status, file, message, err := stripe.Run(ctx, apiKey, runID.String)
		if err == nil && status == "succeeded" {
			var body []byte
			if body, err = stripe.Download(ctx, apiKey, file, 512*1024); errors.As(err, &provider) {
				status, message, err = "failed", provider.Message, nil
			} else if err == nil {
				if check := stripeReport(body, stripe.Reports[report].Columns); check != nil {
					status, message = "failed", check.Error()
				} else {
					err = saveStripeReport(ctx, db, id, closeID, period, actor, report, body)
				}
			} else if strings.Contains(err.Error(), "size") {
				status, message, err = "failed", "The report is larger than the 512 KiB evidence limit. Download it from Stripe in parts and upload them.", nil
			}
		}
		switch {
		case err != nil:
			// The reason stays visible while the report waits; other failures stay in the log.
			message := "Waiting to save the report; it is tried again every few minutes."
			if !strings.Contains(err.Error(), "Firm active job limit reached") {
				log.Print("Stripe report not saved yet: ", err)
			} else {
				message = "Waiting: the firm's active preparation limit is reached. Resolve or cancel work to let this report in."
			}
			if _, err = db.ExecContext(ctx, `UPDATE stripe_report_runs SET error=$2 WHERE id::text=$1 AND state='pending'`, id, message); err == nil {
				err = later()
			}
		case status == "failed" || status == "expired":
			err = fail(strings.TrimSpace("Stripe could not produce the report. " + message))
		case status != "succeeded":
			err = later()
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// saveStripeReport saves a finished report as evidence. The payout reports start an intake job like an upload; the
// balance activity report is chosen as the settlement's third file, so it has no job of its own.
func saveStripeReport(ctx context.Context, db *sql.DB, runID, closeID, period, actor, report string, body []byte) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT 1 FROM client_closes WHERE id::text=$1 FOR UPDATE`, closeID); err != nil {
		return err
	}
	var pending bool
	if err = tx.QueryRowContext(ctx, `SELECT state='pending' FROM stripe_report_runs WHERE id::text=$1 FOR UPDATE`, runID).Scan(&pending); err != nil || !pending {
		return err
	}
	digest := preparation.Digest(body)
	var evidence, purpose string
	err = tx.QueryRowContext(ctx, `SELECT id::text,purpose FROM evidence WHERE close_id::text=$1 AND digest=$2`, closeID, digest).Scan(&evidence, &purpose)
	if err == nil && purpose != "work" {
		if _, err = tx.ExecContext(ctx, `UPDATE stripe_report_runs SET state='failed',error='This exact report is already in the close with a different purpose.',finished_at=now() WHERE id::text=$1`, runID); err != nil {
			return err
		}
		return tx.Commit()
	}
	if err == sql.ErrNoRows {
		var count, size int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE NOT (created_by='quickbooks-sync' AND superseded_by IS NOT NULL)),COALESCE(sum(COALESCE(octet_length(original),octet_length(content))) FILTER (WHERE NOT (created_by='quickbooks-sync' AND superseded_by IS NOT NULL)),0) FROM evidence WHERE close_id::text=$1`, closeID).Scan(&count, &size); err != nil {
			return err
		}
		if count >= 100 || size+len(body) > 20*1024*1024 {
			if _, err = tx.ExecContext(ctx, `UPDATE stripe_report_runs SET state='failed',error='This close has reached its evidence limit (100 files or 20 MiB).',finished_at=now() WHERE id::text=$1`, runID); err != nil {
				return err
			}
			return tx.Commit()
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO evidence(close_id,name,digest,content,created_by,extraction_state,purpose) VALUES($1::bigint,$2,$3,$4,'stripe-report','ready','work') RETURNING id::text`, closeID, stripe.Reports[report].Name+" "+period+".csv", digest, string(body)).Scan(&evidence)
	}
	if err != nil {
		return err
	}
	if report != "activity" {
		if _, err = preparation.StartIntake(ctx, tx, closeID, evidence, actor); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE stripe_report_runs SET state='saved',evidence_id=$2::bigint,finished_at=now(),error='' WHERE id::text=$1`, runID, evidence); err != nil {
		return err
	}
	return tx.Commit()
}

// RunStripeReports moves report runs forward once a minute.
func RunStripeReports(ctx context.Context, db *sql.DB) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := sweepStripeReports(ctx, db); err != nil && ctx.Err() == nil {
			log.Print("Stripe reports could not run: ", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// registerStripe connects a client's Stripe account read-only through a restricted report-scoped key a person
// confirms, and captures the close month's itemized payout reconciliation for the existing settlement flow.
func registerStripe(mux *http.ServeMux, db *sql.DB) {
	path := "/v1/closes/{id}/stripe"
	status := func(ctx context.Context, closeID string) (json.RawMessage, error) {
		var raw json.RawMessage
		err := db.QueryRowContext(ctx, `SELECT jsonb_build_object(
 'connection',(SELECT jsonb_build_object('id',x.id::text,'state',x.state,'livemode',x.livemode,'key_last4',x.key_last4,'account_id',x.account_id,'account_name',x.account_name) FROM stripe_connections x JOIN client_closes c ON c.organization_id=x.organization_id AND lower(c.entity)=lower(x.client_entity) WHERE c.id::text=$1 AND x.state<>'disconnected'),
 'runs',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',r.id::text,'period',r.period,'report',r.report,'state',r.state,'error',r.error,'evidence_id',r.evidence_id::text,'created_at',r.created_at) ORDER BY r.id DESC) FROM (SELECT * FROM stripe_report_runs WHERE close_id::text=$1 ORDER BY id DESC LIMIT 10) r),'[]'::jsonb))`, closeID).Scan(&raw)
		return raw, err
	}
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		raw, err := status(r.Context(), r.PathValue("id"))
		if err != nil {
			problem(w, 503, "Stripe status unavailable.")
			return
		}
		jsonReply(w, 200, raw)
	})
	mux.HandleFunc("POST "+path, func(w http.ResponseWriter, r *http.Request) {
		a := auth.Actor(r.Context())
		if a == nil || a.Organization == "" || !slices.Contains(a.Permissions, "integrations:manage") {
			problem(w, 403, "A hosted session with integrations:manage is required.")
			return
		}
		var in struct {
			Action       string `json:"action"`
			Key          string `json:"key"`
			ConnectionID string `json:"connection_id"`
			Confirmed    bool   `json:"confirmed"`
			Report       string `json:"report"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !in.Confirmed {
			problem(w, 422, "Confirm this Stripe action and client.")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		var entity, period string
		if err := db.QueryRowContext(ctx, `SELECT entity,period FROM client_closes WHERE id::text=$1 AND organization_id=$2`, r.PathValue("id"), a.Organization).Scan(&entity, &period); err != nil {
			problem(w, 404, "Client close not found.")
			return
		}
		switch in.Action {
		case "connect":
			if !stripe.ValidKey(in.Key) {
				problem(w, 422, "Paste a restricted Stripe key (rk_live_… or rk_test_…) that can read reports and account details and create report runs. Secret keys are refused.")
				return
			}
			account, err := stripe.Verify(ctx, in.Key)
			if err != nil {
				problem(w, 422, err.Error())
				return
			}
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				problem(w, 503, "Connection storage unavailable.")
				return
			}
			defer tx.Rollback()
			var id string
			if err = tx.QueryRowContext(ctx, `INSERT INTO stripe_connections(close_id,organization_id,client_entity,actor,state,livemode,key_last4,account_id,account_name) VALUES($1,$2,$3,$4,'unconfirmed',$5,$6,$7,$8) ON CONFLICT DO NOTHING RETURNING id::text`, r.PathValue("id"), a.Organization, entity, a.Subject, strings.HasPrefix(in.Key, "rk_live_"), in.Key[len(in.Key)-4:], account.ID, account.Name).Scan(&id); err != nil {
				problem(w, 409, "A Stripe connection already exists for this client. Disconnect it before connecting again.")
				return
			}
			sealed, err := secrets.Seal(a.Organization+"/stripe/"+id, []byte(in.Key))
			if err == nil {
				_, err = tx.ExecContext(ctx, `UPDATE stripe_connections SET credential=$2 WHERE id::text=$1`, id, sealed)
			}
			if err != nil || tx.Commit() != nil {
				problem(w, 503, "The Stripe key could not be stored. Check the server key.")
				return
			}
		case "confirm":
			result, err := db.ExecContext(ctx, `UPDATE stripe_connections SET state='active',confirmed_by=$4,confirmed_at=now(),updated_at=now() WHERE id::text=$1 AND organization_id=$2 AND lower(client_entity)=lower($3) AND state='unconfirmed'`, in.ConnectionID, a.Organization, entity, a.Subject)
			if err != nil {
				problem(w, 503, "Connection storage unavailable.")
				return
			}
			if n, _ := result.RowsAffected(); n != 1 {
				problem(w, 409, "The connection changed. Refresh its status.")
				return
			}
		case "disconnect":
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				problem(w, 503, "Connection storage unavailable.")
				return
			}
			defer tx.Rollback()
			result, err := tx.ExecContext(ctx, `UPDATE stripe_connections SET state='disconnected',credential=NULL,updated_at=now() WHERE id::text=$1 AND organization_id=$2 AND lower(client_entity)=lower($3) AND state<>'disconnected'`, in.ConnectionID, a.Organization, entity)
			if err != nil {
				problem(w, 503, "Connection storage unavailable.")
				return
			}
			if n, _ := result.RowsAffected(); n != 1 {
				problem(w, 409, "The connection changed. Refresh its status.")
				return
			}
			if _, err = tx.ExecContext(ctx, `UPDATE stripe_report_runs SET state='failed',error='Stripe was disconnected before the report was saved.',finished_at=now() WHERE connection_id::text=$1 AND state IN ('requesting','pending')`, in.ConnectionID); err != nil || tx.Commit() != nil {
				problem(w, 503, "Disconnection could not be confirmed.")
				return
			}
		case "capture":
			if in.Report == "" {
				in.Report = "payout_reconciliation"
			}
			if _, ok := stripe.Reports[in.Report]; !ok {
				problem(w, 422, "Choose the payout reconciliation, payouts or balance activity report.")
				return
			}
			var inFlight bool
			if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM stripe_report_runs WHERE close_id::text=$1 AND period=$2 AND report=$3 AND state IN ('requesting','pending'))`, r.PathValue("id"), period, in.Report).Scan(&inFlight); err != nil {
				problem(w, 503, "Report storage unavailable.")
				return
			}
			if inFlight {
				break
			}
			key, account, err := stripeKey(ctx, db, a.Organization, entity, in.ConnectionID)
			var inactive refusal
			if errors.As(err, &inactive) {
				refuse(w, err)
				return
			}
			if err != nil {
				problem(w, 503, "The Stripe connection is temporarily unavailable; try again.")
				return
			}
			current, err := stripe.Verify(ctx, key)
			if err != nil {
				problem(w, 422, err.Error())
				return
			}
			month, _ := time.Parse("2006-01", period)
			end := month.AddDate(0, 1, 0)
			if current.ID != account {
				problem(w, 409, "This key now belongs to another Stripe account. Disconnect and connect the client's account again.")
				return
			}
			from, through, err := stripe.Availability(ctx, key, in.Report)
			if err != nil {
				problem(w, 422, err.Error())
				return
			}
			if end.After(through) || month.Before(from) {
				problem(w, 409, "Stripe has "+stripe.Reports[in.Report].Name+" data from "+from.Format("2 January 2006")+" through "+through.Format("2 January 2006 15:04 MST")+". Capture "+period+" once Stripe has the whole month.")
				return
			}
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				problem(w, 503, "Report storage unavailable.")
				return
			}
			defer tx.Rollback()
			// Only the request that saved the intent asks Stripe; a concurrent one leaves it to that request or the worker.
			var runID, idempotency string
			err = tx.QueryRowContext(ctx, `INSERT INTO stripe_report_runs(close_id,connection_id,period,state,actor,report) VALUES($1,$2,$3,'requesting',$4,$5) ON CONFLICT (close_id,period,report) WHERE state IN ('requesting','pending') DO NOTHING RETURNING id::text,idempotency_key::text`, r.PathValue("id"), in.ConnectionID, period, a.Subject, in.Report).Scan(&runID, &idempotency)
			inserted := err == nil
			if err == sql.ErrNoRows {
				err = nil
			}
			if err != nil || tx.Commit() != nil {
				problem(w, 503, "Report storage unavailable.")
				return
			}
			if inserted {
				// The same idempotency key makes a retry of a lost answer return the same Stripe run.
				created, err := stripe.CreateRun(ctx, key, idempotency, in.Report, month, end)
				var provider stripe.ProviderError
				switch {
				case errors.As(err, &provider):
					db.ExecContext(ctx, `UPDATE stripe_report_runs SET state='failed',error=$2,finished_at=now() WHERE id::text=$1 AND state='requesting'`, runID, provider.Message)
					problem(w, 422, provider.Message)
					return
				case err == nil:
					_, err = db.ExecContext(ctx, `UPDATE stripe_report_runs SET run_id=$2,state='pending' WHERE id::text=$1 AND state='requesting'`, runID, created)
				}
				if err != nil {
					log.Print("Stripe report request unconfirmed; the worker will ask again: ", err)
				}
			}
		default:
			problem(w, 422, "Choose connect, confirm, capture or disconnect.")
			return
		}
		raw, err := status(ctx, r.PathValue("id"))
		if err != nil {
			problem(w, 503, "Stripe status unavailable.")
			return
		}
		jsonReply(w, 200, raw)
	})
}
