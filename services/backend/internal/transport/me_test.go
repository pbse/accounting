package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

func TestMemberSettingsArePersonalAndLeaveWithTheMembership(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	org := "org_Me" + rand.Text()
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Settings fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture'),($1,'user_b',true,'Synthetic','fixture'),($1,'user_admin',true,'Synthetic','fixture')`, org)
	must(err)
	defer func() {
		for _, q := range []string{`DELETE FROM member_settings WHERE organization_id=$1`, `DELETE FROM attention_preferences WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	mux := http.NewServeMux()
	registerMe(mux, db)
	registerAttention(mux, db, db)
	call := func(subject, method, path string, body any) (int, map[string]any) {
		t.Helper()
		var reader *bytes.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			reader = bytes.NewReader(raw)
		} else {
			reader = bytes.NewReader(nil)
		}
		r := httptest.NewRequest(method, path, reader).WithContext(auth.WithActor(context.Background(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org}))
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}

	// Without a sign-in provider the name is read-only and Drive is off; the notification kinds default to all of them.
	code, me := call("user_a", "GET", "/v1/closes/me", nil)
	if code != 200 || me["time_zone"] != "" {
		t.Fatalf("fresh settings: %d %v", code, me)
	}
	if code, profile := call("user_a", "GET", "/v1/closes/me/profile", nil); code != 200 || profile["name_editable"] != false {
		t.Fatalf("local profile: %d %v", code, profile)
	}
	if code, drive := call("user_a", "GET", "/v1/closes/me/drive", nil); code != 200 || drive["configured"] != false {
		t.Fatalf("local drive: %d %v", code, drive)
	}
	if n := me["notifications"].(map[string]any); len(n["kinds"].([]any)) != len(attentionKinds) || len(n["all_kinds"].([]any)) != len(attentionKinds) || n["browser"] != false {
		t.Fatalf("default notifications: %v", n)
	}
	if code, _ = call("", "GET", "/v1/closes/me", nil); code != 403 {
		t.Fatalf("anonymous read: %d", code)
	}

	// A time zone is kept only as a real region/city name.
	for _, zone := range []string{"Mars/Olympus", "Local", "EST"} {
		if code, _ = call("user_a", "POST", "/v1/closes/me", map[string]any{"time_zone": zone}); code != 422 {
			t.Fatalf("zone %q accepted: %d", zone, code)
		}
	}
	if code, out := call("user_a", "POST", "/v1/closes/me", map[string]any{"time_zone": " Europe/London "}); code != 200 || out["time_zone"] != "Europe/London" {
		t.Fatalf("set zone: %d %v", code, out)
	}
	if code, out := call("user_a", "POST", "/v1/closes/me", map[string]any{"time_zone": "UTC"}); code != 200 || out["time_zone"] != "UTC" {
		t.Fatalf("UTC, which a device may report: %d %v", code, out)
	}
	if code, out := call("user_a", "POST", "/v1/closes/me", map[string]any{"time_zone": "America/New_York"}); code != 200 || out["time_zone"] != "America/New_York" {
		t.Fatalf("change zone: %d %v", code, out)
	}
	if code, me = call("user_a", "GET", "/v1/closes/me", nil); code != 200 || me["time_zone"] != "America/New_York" {
		t.Fatalf("read zone back: %d %v", code, me)
	}
	if code, me = call("user_b", "GET", "/v1/closes/me", nil); code != 200 || me["time_zone"] != "" {
		t.Fatalf("another member sees their own, empty, zone: %d %v", code, me)
	}
	if code, _ = call("user_a", "POST", "/v1/closes/me", map[string]any{"first_name": "Ada", "last_name": "L"}); code != 409 {
		t.Fatalf("name without a sign-in provider: %d", code)
	}
	if code, _ = call("user_a", "POST", "/v1/closes/me", map[string]any{"first_name": "Ada"}); code != 422 {
		t.Fatalf("half a name: %d", code)
	}
	// A refused request keeps nothing: the valid zone beside the bad name is not saved.
	if code, _ = call("user_a", "POST", "/v1/closes/me", map[string]any{"first_name": "", "last_name": "L", "time_zone": "Asia/Tokyo"}); code != 422 {
		t.Fatalf("empty first name: %d", code)
	}
	if code, me = call("user_a", "GET", "/v1/closes/me", nil); code != 200 || me["time_zone"] != "America/New_York" {
		t.Fatalf("zone changed by a refused request: %d %v", code, me)
	}
	if code, _ = call("user_a", "POST", "/v1/closes/me", map[string]any{"colour": "blue"}); code != 400 {
		t.Fatalf("unknown field: %d", code)
	}

	// Notification kinds saved on the attention route read back here.
	if code, _ = call("user_a", "POST", "/v1/closes/attention/preferences", map[string]any{"kinds": []string{"approve"}, "browser": true}); code != 200 {
		t.Fatalf("save kinds: %d", code)
	}
	if code, me = call("user_a", "GET", "/v1/closes/me", nil); code != 200 || len(me["notifications"].(map[string]any)["kinds"].([]any)) != 1 || me["notifications"].(map[string]any)["browser"] != true {
		t.Fatalf("read kinds back: %d %v", code, me)
	}

	// Removing the membership removes the member's settings with their other personal state.
	var current string
	must(db.QueryRow(`SELECT id::text FROM current_organization_members WHERE organization_id=$1 AND subject='user_a'`, org).Scan(&current))
	tx, err := db.Begin()
	must(err)
	if _, err = decideMemberAccess(context.Background(), tx, org, "user_a", false, current, "Left the firm", "user_admin"); err != nil {
		t.Fatal(err)
	}
	must(tx.Commit())
	var left int
	must(db.QueryRow(`SELECT count(*) FROM member_settings WHERE organization_id=$1 AND subject='user_a'`, org).Scan(&left))
	if left != 0 {
		t.Fatalf("settings outlived the membership: %d", left)
	}
}
