package main

import (
	"close.local/backend/internal/delivery"
	"close.local/backend/internal/forwarded"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/quickbooks"
	"close.local/backend/internal/transport"
	"close.local/backend/internal/wf"
	_ "close.local/backend/workflows"
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"
)

func main() {
	if os.Getenv("DATABASE_URL") == "" {
		log.Fatal("DATABASE_URL is required for durable preparation")
	}
	db, err := transport.OpenTenantDB(os.Getenv("DATABASE_URL"), "*")
	if err != nil {
		log.Fatal("Invalid database configuration")
	}
	defer db.Close()
	// Two for the job loops; each workflow run in flight (WF_CONCURRENCY, default 4) holds one while a
	// step writes, and its lease heartbeat briefly another.
	db.SetMaxOpenConns(8)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	loops := map[string]func(context.Context, *sql.DB){
		"preparation":        preparation.Run,
		"delivery":           delivery.Run,
		"forwarded":          forwarded.Run,
		"quickbooks_sync":    quickbooks.RunSync,
		"alerts":             transport.RunAlerts,
		"standing_approvals": transport.RunStandingApprovals,
		"comparisons":        transport.RunComparisons,
		"replays":            transport.RunReplays,
		"xero_refresh":       transport.RunXeroRefresh,
		"xero_backfill":      transport.BackfillXeroRows,
		"stripe_reports":     transport.RunStripeReports,
		"chat_expiry":        transport.RunChatExpiry,
		"close_goals":        transport.RunCloseGoals,
		"workflow_engine":    wf.Run,
		"heartbeat":          func(ctx context.Context, db *sql.DB) { transport.RunHeartbeat(ctx, db, "worker") },
	}
	for name, loop := range loops {
		go supervise(ctx, name, func() { loop(ctx, db) }, recordPanic(db))
	}
	transport.Run(transport.Worker())
}

// restartAfter spaces restarts, so a loop that panics on every pass does not spin.
var restartAfter = 30 * time.Second

// supervise runs one background loop and restarts it after a panic, so one failing loop no
// longer stops the others. A loop that returns is finished and is not restarted.
func supervise(ctx context.Context, name string, loop func(), panicked func(name string, p any, stack []byte)) {
	for {
		p, stack := func() (p any, stack []byte) {
			defer func() {
				if p = recover(); p != nil {
					stack = debug.Stack()
				}
			}()
			loop()
			return nil, nil
		}()
		if p == nil {
			return
		}
		panicked(name, p, stack)
		select {
		case <-ctx.Done():
			return
		case <-time.After(restartAfter):
		}
	}
}

// recordPanic logs the stack and puts the latest panic on the worker's heartbeat, where the admin overview shows it.
func recordPanic(db *sql.DB) func(string, any, []byte) {
	instance, _ := os.Hostname()
	return func(name string, p any, stack []byte) {
		log.Printf("Worker loop %s panicked; restarting in %s: %v\n%s", name, restartAfter, p, stack)
		message := []rune(fmt.Sprintf("%s: %v", name, p))
		if len(message) > 300 {
			message = message[:300]
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := db.ExecContext(ctx, `INSERT INTO worker_heartbeats(loop,instance,detail) VALUES('worker',$1,jsonb_build_object('panic',$2::text,'panicked_at',now()))
 ON CONFLICT (loop) DO UPDATE SET detail=worker_heartbeats.detail||EXCLUDED.detail`, instance, string(message)); err != nil {
			log.Printf("Panic in %s could not be recorded: %v", name, err)
		}
	}
}
