package transport

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

// Every response says how long the API spent before it began and how many database round trips it made by then, as a
// Server-Timing header the browser's network panel shows. For an ordinary reply that is the whole request; a streamed
// agent reply begins with its first event, so its header covers only that much. A request slower than slowRequest in
// full, streamed or not, is also logged by route with all its round trips, without its query string. Pages are meant to arrive with their data in under 400 ms, so the round trips are the number to watch.
const slowRequest = 300 * time.Millisecond

type requestStats struct {
	trips atomic.Int64
	db    atomic.Int64 // nanoseconds spent waiting on the database
}

type statsKey struct{}

// roundTrip counts one database round trip for the request in ctx and returns the function that ends its timing.
func roundTrip(ctx context.Context) func() {
	stats, _ := ctx.Value(statsKey{}).(*requestStats)
	if stats == nil {
		return func() {}
	}
	stats.trips.Add(1)
	start := time.Now()
	return func() { stats.db.Add(int64(time.Since(start))) }
}

type timedWriter struct {
	// first is when the response began: for a stream, the delay that counts.
	first time.Time
	http.ResponseWriter
	start  time.Time
	stats  *requestStats
	status int
}

func (w *timedWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.first = time.Now()
		w.Header().Add("Server-Timing", fmt.Sprintf(`app;desc="until the response began";dur=%.1f, db;desc="%d round trips";dur=%.1f`, ms(time.Since(w.start)), w.stats.trips.Load(), ms(time.Duration(w.stats.db.Load()))))
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *timedWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// Flush keeps streamed replies (the agent's answers) streaming.
func (w *timedWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		if w.status == 0 {
			w.WriteHeader(http.StatusOK)
		}
		f.Flush()
	}
}

func (w *timedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// Identifiers in a path (numbers, UUIDs, provider IDs) are replaced so a slow-request log names the route, not the record.
var pathIdentifier = regexp.MustCompile(`/([0-9]+|[0-9a-fA-F-]{32,36}|[a-z]+_[0-9A-Za-z]{20,})(/|$)`)

func timed(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stats := &requestStats{}
		tw := &timedWriter{ResponseWriter: w, start: time.Now(), stats: stats}
		next.ServeHTTP(tw, r.WithContext(context.WithValue(r.Context(), statsKey{}, stats)))
		// A stream (an answer's events, the attention stream) lives as long as it is listened to: what counts for it is
		// how long it took to begin.
		took := time.Since(tw.start)
		if strings.HasPrefix(tw.Header().Get("Content-Type"), "application/x-ndjson") && !tw.first.IsZero() {
			took = tw.first.Sub(tw.start)
		}
		if took > slowRequest {
			route := pathIdentifier.ReplaceAllString(r.URL.Path, "/:id$2")
			route = pathIdentifier.ReplaceAllString(route, "/:id$2")
			log.Printf("slow request: %s %s %d in %.0f ms, %d database round trips (%.0f ms)", r.Method, route, tw.status, ms(took), stats.trips.Load(), ms(time.Duration(stats.db.Load())))
		}
	})
}
