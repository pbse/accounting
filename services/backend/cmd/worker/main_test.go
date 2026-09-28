package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSuperviseRestartsAfterPanicOnly(t *testing.T) {
	restartAfter = time.Millisecond
	runs, panics := 0, []string{}
	supervise(context.Background(), "demo", func() {
		runs++
		if runs == 1 {
			panic("boom")
		}
	}, func(name string, p any, stack []byte) {
		if len(stack) == 0 {
			t.Error("stack was not captured at the panic")
		}
		panics = append(panics, name+": "+p.(string))
	})
	if runs != 2 || len(panics) != 1 || panics[0] != "demo: boom" {
		t.Fatalf("runs=%d panics=%v", runs, panics)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runs = 0
	supervise(ctx, "stopping", func() { runs++; panic("again") }, func(string, any, []byte) {})
	if runs != 1 {
		t.Fatalf("a cancelled worker restarted its loop: runs=%d", runs)
	}
}

func TestRepeatedPanicsFailHealth(t *testing.T) {
	restartAfter = time.Millisecond
	health := reportFailing(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	check := func() int {
		w := httptest.NewRecorder()
		health.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
		return w.Code
	}
	runs := 0
	supervise(context.Background(), "twice", func() {
		if runs++; runs <= 2 {
			panic("boom")
		}
	}, func(string, any, []byte) {})
	if check() != 200 {
		t.Fatal("two panics failed health")
	}
	runs = 0
	supervise(context.Background(), "thrice", func() {
		if runs++; runs <= 3 {
			panic("boom")
		}
	}, func(string, any, []byte) {})
	if check() != 503 {
		t.Fatal("a loop that kept panicking left the worker healthy")
	}
}
