// Package documents extracts untrusted PDF text outside preparation transactions.
package documents

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type boundedOutput struct{ bytes.Buffer }

var markerLike = regexp.MustCompile(`(?m)^--- Page `)

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 524288 {
		return 0, fmt.Errorf("extracted text exceeds 512 KiB")
	}
	return b.Buffer.Write(p)
}
func command(ctx context.Context, name string, args ...string) (string, error) {
	// No shell, document-controlled arguments or inherited database/provider credentials.
	if runtime.GOOS == "linux" {
		args = append([]string{"--as=1073741824", "--cpu=30", "--fsize=16777216", "--", name}, args...)
		name = "prlimit"
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LANG=C.UTF-8", "OMP_THREAD_LIMIT=1"}
	cmd.WaitDelay = time.Second
	var out boundedOutput
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	err := cmd.Run()
	return out.String(), err
}

// Extract retains page numbers and labels OCR separately from the PDF text layer.
// ponytail: English OCR, 20 pages, 5 MiB originals; larger documents need a separate intake budget.
func Extract(ctx context.Context, original []byte) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	dir, err := os.MkdirTemp("", "close-pdf-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	pdf := filepath.Join(dir, "source.pdf")
	if err = os.WriteFile(pdf, original, 0600); err != nil {
		return "", err
	}
	info, err := command(ctx, "pdfinfo", pdf)
	if err != nil {
		return "", fmt.Errorf("PDF cannot be read; check that it is valid and unencrypted and PDF tools are installed")
	}
	pages := 0
	for _, line := range strings.Split(info, "\n") {
		if strings.HasPrefix(line, "Pages:") {
			pages, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Pages:")))
		}
	}
	if pages < 1 || pages > 20 {
		return "", fmt.Errorf("PDF must contain 1–20 pages; no pages were silently omitted")
	}
	var output boundedOutput
	for page := 1; page <= pages; page++ {
		n := strconv.Itoa(page)
		text, err := command(ctx, "pdftotext", "-f", n, "-l", n, "-layout", "-enc", "UTF-8", pdf, "-")
		if err != nil {
			return "", fmt.Errorf("PDF text extraction failed on page %d", page)
		}
		method := "PDF text"
		if strings.TrimSpace(text) == "" {
			method = "OCR (English; verify against original)"
			prefix := filepath.Join(dir, "page")
			if _, err = command(ctx, "pdftoppm", "-f", n, "-l", n, "-singlefile", "-scale-to", "2000", "-png", pdf, prefix); err != nil {
				return "", fmt.Errorf("PDF rendering failed on page %d", page)
			}
			text, err = command(ctx, "tesseract", prefix+".png", "stdout", "-l", "eng")
			os.Remove(prefix + ".png")
			if err != nil {
				return "", fmt.Errorf("OCR failed on page %d; check Tesseract and English language data", page)
			}
		}
		if !utf8.ValidString(text) {
			return "", fmt.Errorf("Invalid text on page %d", page)
		}
		text = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
				return -1
			}
			return r
		}, text)
		if strings.TrimSpace(text) == "" {
			text = "[No text detected; inspect this page in the original PDF.]"
		}
		// A line in the document shaped like a page marker is indented, so only the extractor marks pages.
		text = markerLike.ReplaceAllString(text, " $0")
		if _, err = fmt.Fprintf(&output, "--- Page %d · %s ---\n%s\n", page, method, text); err != nil {
			return "", err
		}
	}
	return output.String(), nil
}

// A fenced three-minute lease survives crashes without holding a transaction during OCR.
func ProcessOne(ctx context.Context, db *sql.DB) (bool, error) {
	claim := rand.Text()
	var id string
	var original []byte
	err := db.QueryRowContext(ctx, `UPDATE evidence SET extraction_state='processing',extraction_claim=$1,extraction_started_at=now() WHERE id=(SELECT id FROM evidence WHERE close_id IN (SELECT c.id FROM client_closes c JOIN organizations o ON o.id=c.organization_id WHERE o.enabled) AND original IS NOT NULL AND (extraction_state='queued' OR (extraction_state='processing' AND extraction_started_at<now()-interval '3 minutes')) ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id::text,original`, claim).Scan(&id, &original)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	text, extractErr := Extract(ctx, original)
	state, message := "ready", ""
	if extractErr != nil {
		state = "failed"
		message = extractErr.Error()
		text = ""
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return true, err
	}
	defer tx.Rollback()
	var closeID string
	if err = tx.QueryRowContext(ctx, `SELECT c.id::text FROM client_closes c JOIN evidence e ON e.close_id=c.id WHERE e.id=$1 FOR UPDATE OF c`, id).Scan(&closeID); err != nil {
		return true, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE evidence SET content=$3,extraction_state=$4,extraction_error=$5,extraction_claim=NULL WHERE id=$1 AND extraction_claim=$2`, id, claim, text, state, message)
	if err != nil {
		return true, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return true, nil
	}
	_, err = tx.ExecContext(ctx, `UPDATE preparation_jobs SET state='waiting_evidence',error='' WHERE close_id=$1 AND state='needs_confirmation' AND id IN (SELECT t.job_id FROM request_jobs t JOIN work_replies r ON r.request_id=t.request_id WHERE r.source_id=$2)`, closeID, id)
	if err != nil {
		return true, err
	}
	return true, tx.Commit()
}
