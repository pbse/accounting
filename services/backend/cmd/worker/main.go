package main

import (
	"close.local/backend/internal/delivery"
	"close.local/backend/internal/documents"
	"close.local/backend/internal/forwarded"
	"close.local/backend/internal/llm"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/quickbooks"
	"close.local/backend/internal/transport"
	"close.local/backend/internal/wf"
	_ "close.local/backend/workflows"
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

func main() {
	models, err := llm.FromEnv()
	if err != nil {
		log.Fatal(err)
	}
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
	ctx = llm.WithClient(ctx, models)
	loops := map[string]func(context.Context, *sql.DB){
		"preparation":     preparation.Run,
		"documents":       documents.Run,
		"delivery":        delivery.Run,
		"platform_emails": delivery.RunPlatformEmails,
		"forwarded":       forwarded.Run,
		"quickbooks_sync": quickbooks.RunSync,
		"platform_alerts": transport.RunPlatformAlerts,
		"books_versions":  transport.RunBooksVersions,
		"books_parity":    transport.RunBooksParity,
		"workflow_engine": wf.Run,
		"heartbeat": func(ctx context.Context, db *sql.DB) {
			transport.RunHeartbeat(ctx, db, "worker", map[string]any{"ocr": llm.OCRSetting()})
		},
	}
	for name, loop := range loops {
		go supervise(ctx, name, func() { loop(ctx, db) }, recordPanic(db))
	}
	transport.Run(reportFailing(transport.Worker()))
}

// restartAfter spaces restarts, so a loop that panics on every pass does not spin.
var restartAfter = 30 * time.Second

// failing holds, per loop that keeps panicking, when its last repeated panic stops counting against health.
var failing = struct {
	sync.Mutex
	until map[string]time.Time
}{until: map[string]time.Time{}}

// supervise runs one background loop and restarts it after a panic, so one failing loop no
// longer stops the others. A loop that returns is finished and is not restarted. Three panics
// without ten clean minutes between them fail the worker's health check for fifteen minutes.
func supervise(ctx context.Context, name string, loop func(), panicked func(name string, p any, stack []byte)) {
	streak := 0
	for {
		started := time.Now()
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
		if time.Since(started) > 10*time.Minute {
			streak = 0
		}
		if streak++; streak >= 3 {
			failing.Lock()
			failing.until[name] = time.Now().Add(15 * time.Minute)
			failing.Unlock()
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

// reportFailing fails /healthz, which the queue Worker's cron checks every minute, while a loop keeps panicking.
func reportFailing(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			failing.Lock()
			names := []string{}
			for name, until := range failing.until {
				if time.Now().Before(until) {
					names = append(names, name)
				}
			}
			failing.Unlock()
			if len(names) > 0 {
				slices.Sort(names)
				http.Error(w, "Worker loops keep panicking: "+strings.Join(names, ", "), http.StatusServiceUnavailable)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
