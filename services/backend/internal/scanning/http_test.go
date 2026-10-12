package scanning

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestIsolatedScanner(t *testing.T) {
	h := Handler(strings.Repeat("k", 32), t.TempDir())
	r := httptest.NewRequest("POST", "/scan", strings.NewReader("synthetic invoice"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("unauthorized scanner invocation", w.Code)
	}
	state, _ := scan(context.Background(), t.TempDir(), []byte("synthetic invoice"), false)
	if state == "clean" {
		t.Fatal("missing signatures accepted")
	}
	database := os.Getenv("TEST_CLAMAV_DATABASE_DIR")
	if database == "" {
		return
	}
	state, version := scan(context.Background(), database, []byte("synthetic invoice"), false)
	if state != "clean" || version == "" {
		t.Fatal("clean file rejected")
	}
	eicar := "X5O!P%@AP[4\\PZX54(P^)7CC)7}$" + "EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*"
	state, _ = scan(context.Background(), database, []byte(eicar), false)
	if state == "clean" {
		t.Fatal("EICAR accepted")
	}
}
