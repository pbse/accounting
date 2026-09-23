package transport

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"close.local/backend/internal/accruals"
	"close.local/backend/internal/reference"
)

func health(w http.ResponseWriter, r *http.Request) {
	mode := "preparation_only"
	if os.Getenv("WORKOS_CLIENT_ID") != "" && sandboxConfiguration().Ready() {
		mode = "sandbox_posting"
	}
	jsonReply(w, 200, map[string]string{"status": "ok", "mode": mode})
}

func API() http.Handler { return apiWithStore(nil, "") }

func apiWithStore(db *sql.DB, token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/quickbooks/callback", func(w http.ResponseWriter, r *http.Request) { quickbooksReturn(w, r, db) })
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if db == nil {
			problem(w, 503, "Database is not configured.")
			return
		}
		var ready bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conname='workpapers_kind_check' AND conrelid=to_regclass('workpapers') AND position('accrual' in pg_get_constraintdef(oid))>0) AND to_regclass('quickbooks_ledger_selections') IS NOT NULL AND to_regclass('email_delivery') IS NOT NULL AND to_regclass('client_attachments') IS NOT NULL AND to_regclass('coverage_bindings') IS NOT NULL AND to_regclass('request_jobs') IS NOT NULL AND to_regclass('quickbooks_snapshots') IS NOT NULL AND to_regclass('quickbooks_connections') IS NOT NULL AND to_regclass('close_preparations') IS NOT NULL AND to_regclass('email_outbox') IS NOT NULL AND to_regclass('current_client_access') IS NOT NULL AND to_regclass('procedure_versions') IS NOT NULL AND to_regclass('close_scopes') IS NOT NULL AND to_regclass('close_signoffs') IS NOT NULL AND to_regclass('organizations') IS NOT NULL AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='client_closes' AND column_name='organization_id') AND to_regclass('client_closes') IS NOT NULL AND to_regclass('workpapers') IS NOT NULL AND to_regclass('sandbox_postings') IS NOT NULL AND to_regclass('evidence') IS NOT NULL AND to_regclass('preparation_plans') IS NOT NULL AND to_regclass('preparation_jobs') IS NOT NULL AND to_regclass('workpaper_review_state') IS NOT NULL AND to_regclass('request_drafts') IS NOT NULL AND to_regclass('work_replies') IS NOT NULL AND to_regclass('client_request_links') IS NOT NULL AND to_regclass('workpaper_source_review_state') IS NOT NULL AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='preparation_intakes' AND column_name='agent_claim') AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='evidence' AND column_name='extraction_state')`).Scan(&ready); err != nil || !ready {
			problem(w, 503, "Database or required migrations are unavailable.")
			return
		}
		jsonReply(w, 200, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("GET /v1/reference/prepaid", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Disposition", `attachment; filename="synthetic-prepaid.json"`)
		w.Write(reference.Prepaid)
	})
	mux.HandleFunc("POST /v1/webhooks/resend/{connection}", emailWebhook(db))
	mux.HandleFunc("GET /v1/client-request", clientRequest(db))
	mux.HandleFunc("POST /v1/client-request", clientRequest(db))
	mux.HandleFunc("POST /v1/client-request/scan", clientAttachmentScan(db))
	mux.HandleFunc("POST /v1/client-request/scan-text", clientAttachmentScan(db))
	verifier := workspaceVerifier()
	protectPreparation := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if verifier.ClientID != "" {
				if _, err := verifier.Authenticate(r, token); err != nil {
					problem(w, 401, "Sign in to prepare client work.")
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
	allowedOrigin := os.Getenv("FRONTEND_ORIGIN")
	mux.HandleFunc("POST /v1/reconciliations:prepare", protectPreparation(prepareReconciliation))
	store := savedCloses(db, token)
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
			if name != "" && name != "content-type" && name != "authorization" {
				http.Error(w, "Header not allowed", 403)
				return
			}
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.WriteHeader(http.StatusNoContent)
	}
	for _, path := range []string{"/v1/accruals:prepare", "/v1/workpapers:prepare", "/v1/reconciliations:prepare", "/v1/closes", "/v1/closes/{path...}", "/v1/client-request", "/v1/client-request/scan", "/v1/client-request/scan-text"} {
		mux.HandleFunc("OPTIONS "+path, preflight)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin")
		if origin := r.Header.Get("Origin"); origin != "" && origin == allowedOrigin {
			w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		}
		mux.ServeHTTP(w, r)
	})
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
