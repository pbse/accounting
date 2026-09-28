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
	"log"
	"os"
	"os/signal"
	"syscall"
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
	go preparation.Run(ctx, db)
	go delivery.Run(ctx, db)
	go forwarded.Run(ctx, db)
	go quickbooks.RunSync(ctx, db)
	go transport.RunAlerts(ctx, db)
	go transport.RunStandingApprovals(ctx, db)
	go transport.RunComparisons(ctx, db)
	go transport.RunReplays(ctx, db)
	go transport.RunXeroRefresh(ctx, db)
	go transport.BackfillXeroRows(ctx, db)
	go transport.RunStripeReports(ctx, db)
	go transport.RunChatExpiry(ctx, db)
	go transport.RunCloseGoals(ctx, db)
	go wf.Run(ctx, db)
	transport.Run(transport.Worker())
}
