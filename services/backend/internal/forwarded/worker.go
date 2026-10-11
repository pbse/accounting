package forwarded

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"
)

func ProcessOne(ctx context.Context, db *sql.DB) (bool, error) {
	claim := rand.Text()
	var id, digest string
	var body []byte
	var attempts int
	if _, err := db.ExecContext(ctx, `UPDATE forwarded_files SET state='failed',claim=NULL,error='Automated scan exhausted retries after a worker interruption' WHERE state='scanning' AND claimed_at<now()-interval '8 minutes' AND attempts>=3`); err != nil {
		return false, err
	}
	err := db.QueryRowContext(ctx, `UPDATE forwarded_files f SET state='scanning',claim=$1,claimed_at=now(),attempts=attempts+1,error=''
 WHERE f.id=(SELECT id FROM forwarded_files WHERE attempts<3 AND ((state='queued' AND next_attempt_at<=now()) OR (state='scanning' AND claimed_at<now()-interval '8 minutes')) ORDER BY forwarded_files.id FOR UPDATE SKIP LOCKED LIMIT 1)
 RETURNING f.id::text,f.body,f.digest,f.attempts`, claim).Scan(&id, &body, &digest, &attempts)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	status, version := scan(ctx, body, digest)
	if status == "clean" {
		_, err = db.ExecContext(ctx, `UPDATE forwarded_files SET state='clean',claim=NULL,scanned_digest=digest,scanned_at=now(),scan_version=$3,error='' WHERE id=$1 AND claim=$2 AND state='scanning'`, id, claim, version)
		return true, err
	}
	state := "queued"
	if status == "rejected" || attempts >= 3 {
		state = "failed"
	}
	message := "Automated scan unavailable; will retry unchanged."
	if status == "rejected" {
		message = "Automated scan did not clear this file. Keep it quarantined and request a new copy."
	} else if state == "failed" {
		message = "Scanner unavailable after three attempts. Restore the scanner, then request a fresh scan."
	}
	_, err = db.ExecContext(ctx, `UPDATE forwarded_files SET state=$3,claim=NULL,error=$4,next_attempt_at=now()+interval '5 minutes' WHERE id=$1 AND claim=$2 AND state='scanning'`, id, claim, state, message)
	return true, err
}

func scan(ctx context.Context, body []byte, digest string) (string, string) {
	if fmt.Sprintf("%x", sha256.Sum256(body)) != digest {
		return "rejected", ""
	}
	key := os.Getenv("UPLOAD_SCAN_KEY")
	u, err := url.Parse(os.Getenv("TEXT_SCANNER_URL"))
	if err != nil || len(key) < 32 || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "unavailable", ""
	}
	u.Path = "/scan-file"
	work, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(work, "POST", u.String(), bytes.NewReader(body))
	if err != nil {
		return "unavailable", ""
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/octet-stream")
	response, err := (&http.Client{Timeout: 4 * time.Minute}).Do(req)
	if err != nil {
		return "unavailable", ""
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		if response.StatusCode == 422 {
			return "rejected", ""
		}
		return "unavailable", ""
	}
	var receipt struct{ Status, Version string }
	if json.NewDecoder(response.Body).Decode(&receipt) != nil || receipt.Status != "clean" || receipt.Version == "" || len(receipt.Version) > 500 {
		return "unavailable", ""
	}
	return "clean", receipt.Version
}

func Run(ctx context.Context, db *sql.DB) {
	for ctx.Err() == nil {
		worked, err := ProcessOne(ctx, db)
		if err != nil && ctx.Err() == nil {
			log.Print("Forwarded evidence scanner could not update durable state")
		}
		if worked && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}
