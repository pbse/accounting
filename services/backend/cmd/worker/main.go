package main

import (
	"close.local/backend/internal/delivery"
	"close.local/backend/internal/forwarded"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/quickbooks"
	"close.local/backend/internal/transport"
	"context"
	"database/sql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	if os.Getenv("DATABASE_URL") == "" {
		log.Fatal("DATABASE_URL is required for durable preparation")
	}
	db, err := sql.Open("pgx", os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal("Invalid database configuration")
	}
	defer db.Close()
	db.SetMaxOpenConns(2)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go preparation.Run(ctx, db)
	go delivery.Run(ctx, db)
	go forwarded.Run(ctx, db)
	go quickbooks.RunSync(ctx, db)
	transport.Run(transport.Worker())
}
