package transport

import (
	"close.local/backend/internal/llm"
	"close.local/backend/internal/loans"
	"close.local/backend/internal/owners"
	"close.local/backend/internal/payroll"
	"close.local/backend/internal/preparation"
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"close.local/backend/internal/accruals"
	"close.local/backend/internal/reconciliation"
)

func health(w http.ResponseWriter, r *http.Request) {
	mode := "preparation_only"
	if os.Getenv("WORKOS_CLIENT_ID") != "" && sandboxConfiguration().Ready() {
		mode = "sandbox_posting"
	}
	jsonReply(w, 200, map[string]string{"status": "ok", "mode": mode})
}

func API() http.Handler { return apiWithStore(nil, "") }

func apiWithStore(db *sql.DB, token string) http.Handler { return apiWithStores(db, db, token) }

// apiWithStores serves the API from db; sessions, when it is another pool, reads sign-in sessions, which are not one
// firm's rows. Keeping them apart leaves db's connections on the firm they last served, so a request does not switch a
// connection to all firms for its session and back for its own reads, two database round trips each time.
func apiWithStores(db, sessions *sql.DB, token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/quickbooks/callback", system(func(w http.ResponseWriter, r *http.Request) { quickbooksReturn(w, r, db) }))
	mux.HandleFunc("GET /v1/xero/callback", system(func(w http.ResponseWriter, r *http.Request) { xeroReturn(w, r, db) }))
	mux.HandleFunc("POST /internal/forwarded-email", system(receiveForwardedEmail(db)))
	mux.HandleFunc("POST /internal/email-reply", system(receiveEmailReply(db)))
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("GET /healthz/worker", workerHealth(db))
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		machineCtx, machineCancel := context.WithTimeout(r.Context(), 20*time.Second)
		err := llm.CheckContextMachine(machineCtx)
		machineCancel()
		if err != nil {
			problem(w, 503, "Context machine is not ready.")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if db == nil {
			problem(w, 503, "Database is not configured.")
			return
		}
		var ready bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conname='workpapers_kind_check' AND conrelid=to_regclass('workpapers') AND position('payroll' in pg_get_constraintdef(oid))>0) AND to_regclass('quickbooks_ledger_selections') IS NOT NULL AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='quickbooks_ledger_selections' AND column_name='search_query') AND to_regclass('email_delivery') IS NOT NULL AND to_regclass('client_attachments') IS NOT NULL AND to_regclass('coverage_bindings') IS NOT NULL AND to_regclass('request_jobs') IS NOT NULL AND to_regclass('forwarding_links') IS NOT NULL AND to_regclass('forwarded_files') IS NOT NULL AND to_regclass('inbound_replies') IS NOT NULL AND to_regclass('vendor_credit_postings') IS NOT NULL AND to_regclass('bill_corrections') IS NOT NULL AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='bill_corrections' AND column_name='target_line_id') AND to_regclass('operations_alert_deliveries') IS NOT NULL AND to_regclass('quickbooks_entity_syncs') IS NOT NULL AND to_regclass('quickbooks_transaction_rows') IS NOT NULL AND to_regclass('quickbooks_snapshots') IS NOT NULL AND to_regclass('quickbooks_connections') IS NOT NULL AND to_regclass('production_postings') IS NOT NULL AND to_regclass('production_posting_grants') IS NOT NULL AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='production_posting_grants' AND column_name='account_ids') AND to_regclass('close_preparations') IS NOT NULL AND to_regclass('email_outbox') IS NOT NULL AND to_regclass('current_client_access') IS NOT NULL AND to_regclass('current_organization_members') IS NOT NULL AND to_regclass('close_deletion_receipts') IS NOT NULL AND to_regclass('recovery_actions') IS NOT NULL AND to_regclass('procedure_versions') IS NOT NULL AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='procedure_drafts' AND column_name='engine') AND to_regclass('close_scopes') IS NOT NULL AND to_regclass('close_signoffs') IS NOT NULL AND to_regclass('organizations') IS NOT NULL AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='client_closes' AND column_name='retain_until') AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='client_closes' AND column_name='owner') AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='email_delivery_events' AND column_name='close_id') AND to_regclass('client_closes') IS NOT NULL AND to_regclass('workpapers') IS NOT NULL AND to_regclass('close_effort') IS NOT NULL AND to_regclass('close_effort_entries') IS NOT NULL AND to_regclass('approval_requests') IS NOT NULL AND to_regclass('agent_access') IS NOT NULL AND to_regclass('sandbox_postings') IS NOT NULL AND to_regclass('evidence') IS NOT NULL AND to_regclass('preparation_plans') IS NOT NULL AND to_regclass('preparation_jobs') IS NOT NULL AND to_regclass('workpaper_review_state') IS NOT NULL AND position('recurring_accrual' in pg_get_viewdef('workpaper_review_state'::regclass))>0 AND to_regclass('request_drafts') IS NOT NULL AND to_regclass('work_replies') IS NOT NULL AND to_regclass('client_request_links') IS NOT NULL AND to_regclass('workpaper_source_review_state') IS NOT NULL AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='preparation_intakes' AND column_name='agent_claim') AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='preparation_intakes' AND column_name='agent_checkpoint') AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='evidence' AND column_name='extraction_state') AND EXISTS(SELECT 1 FROM pg_policy WHERE polname='tenant' AND polrelid=to_regclass('client_closes')) AND to_regclass('xero_snapshots') IS NOT NULL AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='quickbooks_connections' AND column_name='changed_at') AND to_regclass('daily_reviews') IS NOT NULL AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='xero_connections' AND column_name='fiscal_year_start') AND position('payroll' in pg_get_constraintdef((SELECT oid FROM pg_constraint WHERE conname='production_posting_grants_kinds')))>0 AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='vendor_credit_postings' AND column_name='grant_id') AND to_regclass('client_aliases') IS NOT NULL AND to_regclass('standing_approvals') IS NOT NULL AND to_regclass('model_calls') IS NOT NULL AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='client_request_links' AND column_name='passcode_digest') AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='organizations' AND column_name='independent_review_min_amount') AND to_regclass('idempotency_keys') IS NOT NULL AND to_regclass('xero_postings') IS NOT NULL AND to_regclass('stripe_report_runs') IS NOT NULL AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='daily_reviews' AND column_name='xero_snapshot_id') AND to_regclass('index_rates') IS NOT NULL AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='bill_corrections' AND column_name='rule_reference') AND to_regclass('credit_applications') IS NOT NULL AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='organizations' AND column_name='materiality_amount') AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='xero_connections' AND column_name='changed_at') AND to_regclass('xero_corrections') IS NOT NULL AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='stripe_report_runs' AND column_name='report') AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='organizations' AND column_name='reminder_max') AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='approval_requests' AND column_name='agent') AND to_regclass('chat_messages') IS NOT NULL AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='chat_messages' AND column_name='outcome') AND to_regclass('portfolio_items') IS NOT NULL AND to_regclass('member_invitations') IS NOT NULL AND to_regclass('statement_extractions') IS NOT NULL AND to_regclass('workpaper_comments') IS NOT NULL AND to_regclass('attention_preferences') IS NOT NULL AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name='client_aliases' AND column_name='retired_at') AND to_regclass('close_goals') IS NOT NULL AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='chat_messages' AND column_name='due_on') AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='close_goals' AND column_name='finish') AND to_regclass('close_routines') IS NOT NULL AND to_regclass('goal_handoffs') IS NOT NULL AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='close_goals' AND column_name='summary') AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='organizations' AND column_name='assistant_model_answers') AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='goal_handoffs' AND column_name='account_id') AND to_regclass('xero_transaction_rows') IS NOT NULL AND position('request' in pg_get_constraintdef((SELECT oid FROM pg_constraint WHERE conname='chat_messages_action_check')))>0 AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='work_requests' AND column_name='approved_papers') AND to_regclass('assistant_answers') IS NOT NULL AND to_regclass('close_export_labels') IS NOT NULL AND to_regclass('auth_sessions') IS NOT NULL AND to_regclass('platform_alerts') IS NOT NULL AND to_regclass('content_grants') IS NOT NULL AND to_regclass('management_publications') IS NOT NULL AND to_regclass('management_acknowledgments') IS NOT NULL AND to_regclass('collection_bounces') IS NOT NULL AND to_regclass('opening_cleanup_resolutions') IS NOT NULL AND to_regclass('takeover_setups') IS NOT NULL AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='organizations' AND column_name='support_owner') AND NOT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='organizations' AND column_name='assistant_firm_answers') AND position('open_close' in pg_get_constraintdef((SELECT oid FROM pg_constraint WHERE conname='chat_messages_action_check')))>0 AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='chat_messages' AND column_name='every_month') AND to_regclass('operation_acceptances') IS NOT NULL AND to_regclass('contact_notes') IS NOT NULL AND to_regclass('close_owner_changes') IS NOT NULL AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='coverage_bindings' AND column_name='xero_snapshot_id') AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='goal_handoffs' AND column_name='xero_snapshot_id') AND to_regclass('client_roster') IS NOT NULL AND to_regclass('close_templates') IS NOT NULL AND to_regclass('current_client_roster') IS NOT NULL AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='organizations' AND column_name='email_domain') AND to_regclass('member_role_changes') IS NOT NULL AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='organizations' AND column_name='allow_ledger_writes') AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='auth_sessions' AND column_name='staff') AND to_regclass('platform_emails') IS NOT NULL AND to_regclass('member_settings') IS NOT NULL AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='email_connections' AND column_name='provider') AND to_regclass('client_profiles') IS NOT NULL AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='close_signoffs' AND column_name='profile') AND to_regclass('client_ledgers') IS NOT NULL AND to_regclass('current_client_ledgers') IS NOT NULL AND to_regclass('connector_pilots') IS NOT NULL AND to_regclass('books_sources') IS NOT NULL AND to_regclass('books_imports') IS NOT NULL AND to_regclass('books_layouts') IS NOT NULL AND to_regclass('books_import_pages') IS NOT NULL AND to_regclass('books_import_deletions') IS NOT NULL AND to_regclass('books_import_previews') IS NOT NULL AND EXISTS(SELECT 1 FROM pg_proc WHERE proname='books_source')`).Scan(&ready); err != nil || !ready {
			problem(w, 503, "Database or required migrations are unavailable.")
			return
		}
		if os.Getenv("WORKOS_CLIENT_ID") != "" {
			if err := db.QueryRowContext(ctx, rowSecurityEnforced).Scan(&ready); err != nil || !ready {
				problem(w, 503, "Row-level security is not enforced for this database role. Connect as a role that does not own the tables and lacks superuser and BYPASSRLS.")
				return
			}
		}
		jsonReply(w, 200, map[string]string{"status": "ready"})
	})
	registerEnrollment(mux, db)
	registerAdmin(mux, db, token)
	mux.HandleFunc("POST /v1/webhooks/resend/{connection}", system(emailWebhook(db)))
	mux.HandleFunc("POST /v1/webhooks/resend/krith", system(krithEmailWebhook(db)))
	mux.HandleFunc("POST /v1/webhooks/quickbooks", system(quickbooksWebhook(db)))
	mux.HandleFunc("POST /v1/webhooks/xero", system(xeroWebhook(db)))
	mux.HandleFunc("GET /v1/client-request", system(clientRequest(db)))
	mux.HandleFunc("POST /v1/client-request", system(clientRequest(db)))
	mux.HandleFunc("POST /v1/client-request/scan", system(clientAttachmentScan(db)))
	mux.HandleFunc("POST /v1/client-request/scan-text", system(clientAttachmentScan(db)))
	mux.HandleFunc("POST /v1/contact", system(contactForm(db, defaultContactLimits)))
	verifier := workspaceVerifier()
	protectPreparation := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if verifier.ClientID != "" {
				actor, err := verifier.Authenticate(r, token)
				if err != nil {
					problem(w, 401, "Sign in to prepare client work.")
					return
				}
				if slices.Contains(actor.Permissions, "executive:read") {
					problem(w, 403, "Executive access is read-only.")
					return
				}
			}
			next(w, r)
		}
	}
	mux.HandleFunc("POST /v1/workpapers:prepare", protectPreparation(preparePrepaid))
	mux.HandleFunc("POST /v1/accruals:prepare", protectPreparation(func(w http.ResponseWriter, r *http.Request) {
		var in accruals.Input
		if !decode(w, r, &in) {
			return
		}
		paper, err := accruals.Prepare(in)
		if err != nil {
			problem(w, 422, err.Error())
			return
		}
		jsonReply(w, 200, paper)
	}))
	mux.HandleFunc("POST /v1/owners:prepare", protectPreparation(func(w http.ResponseWriter, r *http.Request) {
		var in owners.Input
		if !decode(w, r, &in) {
			return
		}
		paper, err := owners.Prepare(in)
		if err != nil {
			problem(w, 422, err.Error())
			return
		}
		jsonReply(w, 200, paper)
	}))
	mux.HandleFunc("POST /v1/payroll:prepare", protectPreparation(func(w http.ResponseWriter, r *http.Request) {
		var in payroll.Input
		if !decode(w, r, &in) {
			return
		}
		paper, err := payroll.Prepare(in)
		if err != nil {
			problem(w, 422, err.Error())
			return
		}
		jsonReply(w, 200, paper)
	}))
	mux.HandleFunc("POST /v1/loans:prepare", protectPreparation(func(w http.ResponseWriter, r *http.Request) {
		var in loans.Input
		if !decode(w, r, &in) {
			return
		}
		paper, err := loans.Prepare(in)
		if err != nil {
			problem(w, 422, err.Error())
			return
		}
		body, _ := json.Marshal(paper)
		if err = preparation.CheckIndexRates(r.Context(), db, "loan", body); err != nil {
			refuseIndex(w, err)
			return
		}
		jsonReply(w, 200, paper)
	}))
	allowedOrigin := os.Getenv("FRONTEND_ORIGIN")
	mux.HandleFunc("POST /v1/reconciliations:prepare", protectPreparation(prepareReconciliation))
	mux.HandleFunc("POST /v1/settlements:prepare", protectPreparation(func(w http.ResponseWriter, r *http.Request) {
		var in reconciliation.SettlementInput
		if !decode(w, r, &in) {
			return
		}
		paper, err := reconciliation.PrepareSettlement(in)
		if err != nil {
			problem(w, 422, err.Error())
			return
		}
		jsonReply(w, 200, paper)
	}))
	store := savedCloses(db, token, sessions)
	mux.Handle("/mcp", mcpHandler(store, token, db))
	if oauth := mcpOAuth(); oauth != nil {
		metadata := func(w http.ResponseWriter, r *http.Request) {
			jsonReply(w, 200, map[string]any{"resource": oauth.Audience, "authorization_servers": []string{oauth.Issuer}, "bearer_methods_supported": []string{"header"}})
		}
		mux.HandleFunc("GET /.well-known/oauth-protected-resource", metadata)
		mux.HandleFunc("GET /.well-known/oauth-protected-resource/{path...}", metadata)
	}
	for _, method := range []string{"GET", "POST"} {
		mux.Handle(method+" /v1/closes", store)
		mux.Handle(method+" /v1/closes/", store)
	}
	preflight := func(w http.ResponseWriter, r *http.Request) {
		method := r.Header.Get("Access-Control-Request-Method")
		if allowedOrigin == "" || r.Header.Get("Origin") != allowedOrigin || (method != "POST" && method != "GET") {
			http.Error(w, "Origin or method not allowed", 403)
			return
		}
		for _, header := range strings.Split(r.Header.Get("Access-Control-Request-Headers"), ",") {
			name := strings.ToLower(strings.TrimSpace(header))
			if name != "" && name != "content-type" && name != "authorization" && name != "x-request-passcode" {
				http.Error(w, "Header not allowed", 403)
				return
			}
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-Passcode")
		w.WriteHeader(http.StatusNoContent)
	}
	for _, path := range []string{"/v1/accruals:prepare", "/v1/loans:prepare", "/v1/owners:prepare", "/v1/payroll:prepare", "/v1/workpapers:prepare", "/v1/reconciliations:prepare", "/v1/settlements:prepare", "/v1/organizations/enroll", "/v1/closes", "/v1/closes/{path...}", "/v1/client-request", "/v1/client-request/scan", "/v1/client-request/scan-text", "/v1/contact"} {
		mux.HandleFunc("OPTIONS "+path, preflight)
	}
	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin")
		if origin := r.Header.Get("Origin"); origin != "" && origin == allowedOrigin {
			w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		}
		mux.ServeHTTP(w, r)
	})
	// AUTH_SESSION=server adds backend-held sign-in; ConfiguredAPI has already refused an incomplete configuration.
	sessionStore := sessions
	if sessions, _ := serverSessions(); sessions != nil && db != nil {
		sessions.register(mux, db, verifier)
		handler = sessions.bearer(sessionStore, verifier, handler)
	}
	return timed(handler)
}

func Worker() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("POST /internal/jobs", func(w http.ResponseWriter, r *http.Request) {
		// Preparation polls durable Postgres jobs; arbitrary queue messages are not accepted.
		http.Error(w, "QUEUE_DELIVERY_NOT_ENABLED", http.StatusServiceUnavailable)
	})
	return mux
}

func Run(handler http.Handler) {
	address := os.Getenv("HTTP_ADDR")
	if address == "" {
		address = ":8080"
	}
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			log.Print(err)
		}
	}()
	log.Printf("listening on %s", address)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
