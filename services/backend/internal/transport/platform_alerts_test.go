package transport

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestPlatformProblemsAlertOnceAndResolve(t *testing.T) {
	db := adminDB(t)
	t.Setenv("WORKOS_CLIENT_ID", "")
	ctx := context.Background()
	loop := "test_loop_" + strings.ToLower(rand.Text())
	if _, err := db.Exec(`INSERT INTO worker_heartbeats(loop,instance,beat_at) VALUES($1,'test',now()-interval '10 minutes')`, loop); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec(`DELETE FROM worker_heartbeats WHERE loop=$1`, loop)
		db.Exec(`DELETE FROM platform_alerts WHERE item_id=$1`, loop)
	})
	var mu sync.Mutex
	received := map[string]map[string]string{}
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &body)
		if r.Header.Get("Authorization") != "Bearer hook-token" {
			t.Errorf("the webhook token must be sent")
		}
		mu.Lock()
		received[r.Header.Get("Idempotency-Key")] = body
		mu.Unlock()
	}))
	defer hook.Close()
	ours := func() []map[string]string {
		mu.Lock()
		defer mu.Unlock()
		out := []map[string]string{}
		for key, body := range received {
			if body["item_id"] == loop && strings.HasPrefix(key, "close-platform-alert/") {
				out = append(out, body)
			}
		}
		return out
	}

	// A stalled loop opens one alert, delivered once, however often the worker checks.
	for range 2 {
		if err := collectPlatformAlerts(ctx, db); err != nil {
			t.Fatal(err)
		}
		if err := deliverPlatformAlerts(ctx, db, hook.URL, "hook-token"); err != nil {
			t.Fatal(err)
		}
	}
	if got := ours(); len(got) != 1 || got[0]["kind"] != "platform.loop_stale" || got[0]["scope"] != "platform" || !strings.Contains(got[0]["detail"], "last beat") {
		t.Fatalf("one delivered alert for the stalled loop: %v", got)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM platform_alerts WHERE item_id=$1 AND resolved_at IS NULL`, loop).Scan(&status); err != nil || status != "accepted" {
		t.Fatalf("delivery %q %v", status, err)
	}

	// Staff see it on the overview.
	h := apiWithStore(db, strings.Repeat("a", 32))
	if problems := mustString(adminJSON(t, h, strings.Repeat("a", 32), "GET", "/v1/admin/overview", "", 200)["platform_problems"]); !strings.Contains(problems, loop) || !strings.Contains(problems, `"delivery":"accepted"`) {
		t.Fatalf("overview problems: %s", problems)
	}

	// When the loop beats again, the alert resolves; a later stall opens a new one.
	db.Exec(`UPDATE worker_heartbeats SET beat_at=now() WHERE loop=$1`, loop)
	if err := collectPlatformAlerts(ctx, db); err != nil {
		t.Fatal(err)
	}
	var open int
	if db.QueryRow(`SELECT count(*) FROM platform_alerts WHERE item_id=$1 AND resolved_at IS NULL`, loop).Scan(&open); open != 0 {
		t.Fatal("the alert must resolve once the loop beats")
	}
	db.Exec(`UPDATE worker_heartbeats SET beat_at=now()-interval '10 minutes' WHERE loop=$1`, loop)
	if err := collectPlatformAlerts(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := deliverPlatformAlerts(ctx, db, hook.URL, "hook-token"); err != nil {
		t.Fatal(err)
	}
	if got := ours(); len(got) != 2 {
		t.Fatalf("a new stall is a new alert: %v", got)
	}

	// A claim the worker dropped before the webhook answered becomes unconfirmed, not stuck sending.
	db.Exec(`UPDATE platform_alerts SET status='sending',claimed_at=now()-interval '5 minutes',finished_at=NULL WHERE item_id=$1 AND resolved_at IS NULL`, loop)
	if err := deliverPlatformAlerts(ctx, db, hook.URL, "hook-token"); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT status FROM platform_alerts WHERE item_id=$1 AND resolved_at IS NULL`, loop).Scan(&status); err != nil || status != "unknown" || len(ours()) != 2 {
		t.Fatalf("a dropped claim: %q %v, deliveries %d", status, err, len(ours()))
	}

	// A webhook that fails leaves the delivery unconfirmed, not lost or repeated.
	db.Exec(`UPDATE worker_heartbeats SET beat_at=now() WHERE loop=$1`, loop)
	collectPlatformAlerts(ctx, db)
	db.Exec(`UPDATE worker_heartbeats SET beat_at=now()-interval '10 minutes' WHERE loop=$1`, loop)
	collectPlatformAlerts(ctx, db)
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer failing.Close()
	deliverPlatformAlerts(ctx, db, failing.URL, "hook-token")
	if err := db.QueryRow(`SELECT status FROM platform_alerts WHERE item_id=$1 AND resolved_at IS NULL`, loop).Scan(&status); err != nil || status != "unknown" {
		t.Fatalf("failed delivery %q %v", status, err)
	}
}

// An email of Krith's whose outcome is unknown raises a platform alert of its own, naming the row and Resend's
// idempotency key but not the address, and it resolves once the row is settled.
func TestUnknownPlatformEmailsAlertUntilSettled(t *testing.T) {
	db := adminDB(t)
	ctx := context.Background()
	org := "org_UnknownEmail" + strings.ToLower(rand.Text()[:8])
	if _, err := db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Acme')`, org); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := db.QueryRow(`INSERT INTO platform_emails(kind,organization_id,subject,recipient,state,attempts) VALUES('welcome',$1,'user_ana','ana@acme.test','unknown',3) RETURNING id::text`, org).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec(`DELETE FROM platform_alerts WHERE kind='platform.unknown_email' AND item_id=$1`, id)
		db.Exec(`DELETE FROM organizations WHERE id=$1`, org)
	})
	if err := collectPlatformAlerts(ctx, db); err != nil {
		t.Fatal(err)
	}
	var detail string
	if err := db.QueryRow(`SELECT detail FROM platform_alerts WHERE kind='platform.unknown_email' AND item_id=$1 AND resolved_at IS NULL`, id).Scan(&detail); err != nil || !strings.Contains(detail, "krith-platform/"+id) || strings.Contains(detail, "ana@") {
		t.Fatalf("an open alert names the idempotency key, not the address: %q %v", detail, err)
	}
	if _, err := db.Exec(`UPDATE platform_emails SET state='sent' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := collectPlatformAlerts(ctx, db); err != nil {
		t.Fatal(err)
	}
	var open int
	if db.QueryRow(`SELECT count(*) FROM platform_alerts WHERE kind='platform.unknown_email' AND item_id=$1 AND resolved_at IS NULL`, id).Scan(&open); open != 0 {
		t.Fatal("the alert resolves once the email is settled")
	}
}
