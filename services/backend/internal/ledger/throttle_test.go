package ledger

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDoThrottledRepeatsOnlyThrottledReads(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/busy" || calls < 3 {
			w.Header().Set("Retry-After", "0")
			if calls == 2 {
				w.Header().Set("Retry-After", time.Now().Add(-time.Second).UTC().Format(http.TimeFormat))
			}
			w.WriteHeader(429)
			return
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	send := func(method, path string) int {
		t.Helper()
		calls = 0
		req, _ := http.NewRequest(method, server.URL+path, nil)
		response, err := DoThrottled(server.Client(), req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response.StatusCode
	}
	if status := send("GET", "/"); status != 200 || calls != 3 {
		t.Fatalf("a read throttled twice: %d after %d calls", status, calls)
	}
	if status := send("GET", "/busy"); status != 429 || calls != 3 {
		t.Fatalf("a read throttled throughout: %d after %d calls", status, calls)
	}
	if status := send("PUT", "/"); status != 429 || calls != 1 {
		t.Fatalf("a throttled write was repeated: %d after %d calls", status, calls)
	}
}
