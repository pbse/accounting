// Package documents extracts untrusted PDF text outside preparation transactions.
package documents

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
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

	"close.local/backend/internal/budget"
	"close.local/backend/internal/llm"
)

type boundedOutput struct{ bytes.Buffer }

var markerLike = regexp.MustCompile(`(?m)^--- Page `)

// UnreadPage stands for a PDF page the extractor could not read.
const UnreadPage = "[No text detected; inspect this page in the original PDF.]"

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

// Reading is how a PDF's pages are read beyond their text layer: Gemini, when configured, reads every page; Reserve
// counts each Gemini call against the firm's daily model limit first. Tesseract reads a page without a text layer that
// Gemini could not.
type Reading struct {
	Gemini  PageReader // nil: no machine reading but Tesseract's
	Reserve func(context.Context) error
	// local is the time the document's scanned pages from this one on need to be read locally, should Gemini fail
	// them: Gemini is asked only while the time left covers its call and that.
	local time.Duration
	// off is set once the firm's limit or the time left rules Gemini out for the rest of the document.
	off bool
}

// A scanned page takes at most about this long to render and read with Tesseract; geminiMargin is kept besides.
const (
	localPerPage = 6 * time.Second
	geminiMargin = 10 * time.Second
)

// page reads one page. Its text layer, when it has one, is exact and always kept, lines and all: Gemini's reading of
// the page image only checks it, and the page's label says whether the two agree, so a page whose image holds more
// than its text, or other text, is flagged. A page without a text layer takes Gemini's reading, or else Tesseract's.
// render draws the page as a PNG; tesseract reads one.
func (r *Reading) page(ctx context.Context, layer string, render func() ([]byte, error), tesseract func([]byte) (string, error)) (string, string, error) {
	var image []byte
	var renderErr error
	rendered := func() ([]byte, error) {
		if image == nil && renderErr == nil {
			image, renderErr = render()
		}
		return image, renderErr
	}
	hasLayer := strings.TrimSpace(layer) != ""
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < llm.GeminiTimeout+r.local+geminiMargin {
		r.off = true
	}
	if r.Gemini != nil && !r.off {
		// A page Gemini would refuse unsent (an empty or oversized image) is never charged to the firm's limit.
		if png, err := rendered(); err == nil && len(png) > 0 && len(png) <= llm.GeminiMaxImage {
			if r.Reserve != nil {
				if err = r.Reserve(ctx); err != nil {
					r.off = true
				}
			}
			if err == nil {
				text, model, err := r.Gemini.ReadPage(ctx, png)
				switch {
				case err != nil:
					log.Printf("Gemini could not read a PDF page; using the local reading: %v", err)
				case strings.TrimSpace(text) == "":
				case !hasLayer:
					return text, "Gemini " + model + " (verify against original)", nil
				case sameReading(text, layer):
					return layer, "PDF text; Gemini " + model + " read the same text", nil
				default:
					return layer, "PDF text; Gemini " + model + imageDiffers, nil
				}
			}
		}
	}
	if hasLayer {
		// With Gemini configured, a page it did not compare with its image says so; without it, a page is plain PDF
		// text, as it always was.
		if r.Gemini != nil {
			return layer, "PDF text" + imageUnchecked, nil
		}
		return layer, "PDF text", nil
	}
	png, err := rendered()
	if err != nil {
		return "", "", errRender
	}
	text, err := tesseract(png)
	if err != nil {
		return "", "", errOCR
	}
	return text, "OCR (English; verify against original)", nil
}

var errRender, errOCR = errors.New("render"), errors.New("ocr")

// imageDiffers ends the label of a page whose image Gemini read differently from its text layer; Check turns it into
// a question that holds the document's facts until a person compares the original.
const imageDiffers = " read it differently, so check the original"

// imageUnchecked ends the label of a text page Gemini, though configured, did not compare with its image (it failed or
// read nothing, the image was too large, or the firm's limit or the document's time ruled it out). The page is then
// exactly what it was before Gemini: its own text, unchecked. Check names it; it holds nothing, since an unchecked
// page is the product's standing case without Gemini.
const imageUnchecked = "; Gemini could not compare it with the page image"

// Extract retains page numbers and labels machine readings separately from the PDF text layer.
// ponytail: English OCR, 20 pages, 5 MiB originals; larger documents need a separate intake budget.
func Extract(ctx context.Context, original []byte, reading Reading) (string, error) {
	// Under the three-minute extraction lease, with Gemini's per-page calls inside it.
	ctx, cancel := context.WithTimeout(ctx, 150*time.Second)
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
	// Every page's text layer first, so the scanned pages, which only a machine can read, are known before any is.
	layers := make([]string, pages+1)
	for page := 1; page <= pages; page++ {
		n := strconv.Itoa(page)
		if layers[page], err = command(ctx, "pdftotext", "-f", n, "-l", n, "-layout", "-enc", "UTF-8", pdf, "-"); err != nil {
			return "", fmt.Errorf("PDF text extraction failed on page %d", page)
		}
	}
	var output boundedOutput
	for page := 1; page <= pages; page++ {
		n, layer := strconv.Itoa(page), layers[page]
		reading.local = 0
		for _, later := range layers[page:] {
			if strings.TrimSpace(later) == "" {
				reading.local += localPerPage
			}
		}
		prefix := filepath.Join(dir, "page")
		text, method, err := reading.page(ctx, layer, func() ([]byte, error) {
			if _, err := command(ctx, "pdftoppm", "-f", n, "-l", n, "-singlefile", "-scale-to", "2000", "-png", pdf, prefix); err != nil {
				return nil, err
			}
			return os.ReadFile(prefix + ".png")
		}, func([]byte) (string, error) {
			return command(ctx, "tesseract", prefix+".png", "stdout", "-l", "eng")
		})
		os.Remove(prefix + ".png")
		switch err {
		case nil:
		case errRender:
			return "", fmt.Errorf("PDF rendering failed on page %d", page)
		default:
			return "", fmt.Errorf("OCR failed on page %d; check Tesseract and English language data", page)
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
			text = UnreadPage
		}
		// A line in the document shaped like a page marker is indented, so only the extractor marks pages.
		text = markerLike.ReplaceAllString(text, " $0")
		if _, err = fmt.Fprintf(&output, "--- Page %d · %s ---\n%s\n", page, method, text); err != nil {
			return "", err
		}
	}
	return output.String(), nil
}

// Run extracts queued PDFs, one at a time, in the worker's loop of its own: a page reading can take Gemini up to 30
// seconds, and a document up to its 150, which must not hold up intake or preparation.
func Run(ctx context.Context, db *sql.DB) {
	for ctx.Err() == nil {
		extracted, err := ProcessOne(ctx, db)
		if err != nil && ctx.Err() == nil {
			log.Print("Document worker could not persist extraction; will retry")
		}
		if extracted && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

// A fenced three-minute lease survives crashes without holding a transaction during OCR.
func ProcessOne(ctx context.Context, db *sql.DB) (bool, error) {
	claim := rand.Text()
	var id, org string
	var original []byte
	err := db.QueryRowContext(ctx, `UPDATE evidence SET extraction_state='processing',extraction_claim=$1,extraction_started_at=now() WHERE id=(SELECT id FROM evidence WHERE close_id IN (SELECT c.id FROM client_closes c JOIN organizations o ON o.id=c.organization_id WHERE o.enabled AND ($2::bigint[] IS NULL OR c.id=ANY($2::bigint[]))) AND original IS NOT NULL AND (extraction_state='queued' OR (extraction_state='processing' AND extraction_started_at<now()-interval '3 minutes')) ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1)
 RETURNING id::text,original,(SELECT organization_id FROM client_closes WHERE id=evidence.close_id)`, claim, CloseScope(ctx)).Scan(&id, &original, &org)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// Each Gemini page reading counts against the firm's daily model limit, as preparation's calls do, and as Gemini's.
	reading := Reading{Reserve: func(ctx context.Context) error { return budget.ReserveGeminiCalls(ctx, db, org, 1) }}
	if g := llm.GeminiFromEnv(); g != nil {
		reading.Gemini = g
	}
	text, extractErr := Extract(ctx, original, reading)
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

type scopeKey struct{}

// WithinCloses limits the work a queue step takes to the given closes; without it a step takes the next job of any
// enabled firm. The demonstration seed works its own closes' queue this way.
func WithinCloses(ctx context.Context, closes []int64) context.Context {
	return context.WithValue(ctx, scopeKey{}, closes)
}

// CloseScope is the closes a step is limited to, or NULL for every close.
func CloseScope(ctx context.Context) any {
	closes, _ := ctx.Value(scopeKey{}).([]int64)
	if len(closes) == 0 {
		return nil
	}
	return closes
}
