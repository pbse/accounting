package scanning

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// Handler has no database or application credentials. Definitions and the engine
// are loaded only for an authorized text upload; the host may sleep when idle.
func Handler(key, database string) http.Handler {
	var lock sync.Mutex
	var updated time.Time
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/healthz" {
			w.WriteHeader(200)
			return
		}
		binary := r.URL.Path == "/scan-file"
		if r.Method != "POST" || (r.URL.Path != "/scan" && !binary) || len(key) < 32 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+key)) != 1 {
			http.Error(w, "Unavailable", 403)
			return
		}
		// A binary file is a client's books (a workbook, up to the 50 MiB a books file may be) or a document.
		limit := int64(512 * 1024)
		if binary {
			limit = 50 * 1024 * 1024
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		body, err := io.ReadAll(r.Body)
		if err != nil || len(body) == 0 || (!binary && !utf8.Valid(body)) {
			http.Error(w, "Invalid text upload", 422)
			return
		}
		if !binary {
			for _, c := range string(body) {
				if unicode.IsControl(c) && c != '\n' && c != '\r' && c != '\t' {
					http.Error(w, "Invalid text upload", 422)
					return
				}
			}
		}
		if !lock.TryLock() {
			http.Error(w, "Scanner busy; retry unchanged", 503)
			return
		}
		defer lock.Unlock()
		// Room for a signature update (two minutes at most) and then a binary scan (150 seconds).
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()
		if time.Since(updated) > 6*time.Hour {
			if os.MkdirAll(database, 0700) != nil || strings.ContainsAny(database, "\r\n") {
				http.Error(w, "Scanner storage unavailable", 503)
				return
			}
			config := filepath.Join(database, "freshclam.conf")
			if os.WriteFile(config, []byte("DatabaseDirectory "+database+"\nDatabaseMirror database.clamav.net\n"), 0600) != nil {
				http.Error(w, "Scanner configuration unavailable", 503)
				return
			}
			update, stop := context.WithTimeout(ctx, 2*time.Minute)
			err = exec.CommandContext(update, "freshclam", "--config-file="+config, "--quiet").Run()
			stop()
			if err == nil {
				updated = time.Now()
			}
			// Scan still enforces signature freshness if the update service is unavailable.
		}
		state, version := scan(ctx, database, body, binary)
		if state != "clean" {
			status := 503
			if state == "infected" {
				status = 422
			}
			http.Error(w, "Scan not clean or unavailable; file remains quarantined", status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "clean", "version": version})
	})
}

// scan checks body with ClamAV. A text file is small; a binary one may be a workbook, a zip whose sheets unpack to many
// times its size, so it is scanned whole within larger limits and given longer. Anything past the limits is an alert,
// never a pass.
func scan(ctx context.Context, database string, body []byte, binary bool) (string, string) {
	timeout, limits := 90*time.Second, []string{"--max-filesize=6M", "--max-scansize=50M", "--max-files=100"}
	if binary {
		timeout, limits = 150*time.Second, []string{"--max-filesize=256M", "--max-scansize=1024M", "--max-files=1000"}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	dir, err := os.MkdirTemp("", "close-text-scan-")
	if err != nil {
		return "failed", ""
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "attachment")
	if os.WriteFile(path, body, 0600) != nil {
		return "failed", ""
	}
	version, err := exec.CommandContext(ctx, "clamscan", "--database="+database, "--version").Output()
	if err != nil || len(version) > 500 {
		return "failed", ""
	}
	err = exec.CommandContext(ctx, "clamscan", append(append([]string{"--database=" + database, "--official-db-only=yes", "--fail-if-cvd-older-than=2", "--alert-encrypted=yes", "--alert-exceeds-max=yes"}, limits...), "--no-summary", path)...).Run()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return "infected", ""
		}
		return "unavailable", ""
	}
	return "clean", strings.TrimSpace(string(version))
}
