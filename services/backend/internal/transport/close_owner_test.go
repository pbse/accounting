package transport

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

// A close changes hands: its holder hands it to a teammate who can review the client, a firm administrator reassigns
// it whoever holds it, nobody else can, and each change is recorded and shown in Settings → Security.
func TestACloseChangesHandsAndEachChangeIsRecorded(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	org := "org_Owner" + rand.Text()
	// Partners review every client and reviewers only those granted to them; Dee is a viewer, who reviews nothing.
	// Xi is on the board: their review comes with executive:read, which keeps them out of close work.
	asFirm(t, map[string]string{"user_a": "partner", "user_b": "partner", "user_c": "reviewer", "user_d": "viewer", "user_e": "reviewer", "user_x": "board"}, org)
	for _, q := range []string{`INSERT INTO organizations(id,name) VALUES($1,'Owner fixture')`,
		`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) SELECT $1,s,true,'Synthetic','fixture' FROM unnest(ARRAY['user_a','user_b','user_c','user_d','user_e','user_x']) s`,
		`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_c','Alpha',true,'Synthetic','fixture'),($1,'user_d','Alpha',true,'Synthetic','fixture')`} {
		_, err = db.Exec(q, org)
		must(err)
	}
	var alpha string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09') RETURNING id::text`, org).Scan(&alpha))
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM close_signoffs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM client_closes WHERE organization_id=$1`,
			`DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			if _, err := db.Exec(q, org); err != nil {
				t.Errorf("cleanup %s: %v", q, err)
			}
		}
	})
	store := savedCloses(db, strings.Repeat("a", 32))
	call := func(subject string, permissions []string, method, path string, body any, want int) string {
		t.Helper()
		var reader *bytes.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			reader = bytes.NewReader(raw)
		} else {
			reader = bytes.NewReader(nil)
		}
		r := httptest.NewRequest(method, path, reader).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org, Permissions: permissions}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %s %v: %d %s", subject, method, path, body, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	partner, reviewer := []string{"workpapers:review", "clients:all"}, []string{"workpapers:review"}
	// The administrator manages members across every client but reviews no work; the viewer can do neither.
	admin, viewer := []string{"members:manage", "clients:all"}, []string{}
	plan := func(subject string, permissions []string, assignment, assignee, note string, want int) string {
		t.Helper()
		return call(subject, permissions, "POST", "/v1/closes/"+alpha+"/planning", map[string]string{"priority": "normal", "assignment": assignment, "assignee": assignee, "note": note}, want)
	}
	owner := func() string {
		t.Helper()
		var o string
		must(db.QueryRow(`SELECT owner FROM client_closes WHERE id=$1`, alpha).Scan(&o))
		return o
	}
	handed := func(subject string, permissions []string) *closeHandover {
		t.Helper()
		var c struct{ Close savedClose }
		must(json.Unmarshal([]byte(call(subject, permissions, "GET", "/v1/closes/"+alpha, nil, 200)), &c))
		return c.Close.Handed
	}
	handedTo := func(subject string, permissions []string) string {
		t.Helper()
		var inbox struct {
			Groups []struct {
				Events []struct {
					Key  string `json:"key"`
					Kind string `json:"kind"`
					Text string `json:"text"`
				} `json:"events"`
			} `json:"groups"`
		}
		must(json.Unmarshal([]byte(call(subject, permissions, "GET", "/v1/closes/attention", nil, 200)), &inbox))
		for _, g := range inbox.Groups {
			for _, e := range g.Events {
				if e.Kind == "handoff" && strings.HasPrefix(e.Key, "close-handoff:") {
					return e.Text
				}
			}
		}
		return ""
	}
	takeOver := func(subject string, permissions []string) bool {
		t.Helper()
		var c struct{ Close savedClose }
		must(json.Unmarshal([]byte(call(subject, permissions, "GET", "/v1/closes/"+alpha, nil, 200)), &c))
		return c.Close.CanTakeOver
	}
	canAssign := func(subject string, permissions []string) bool {
		t.Helper()
		var c struct{ Close savedClose }
		must(json.Unmarshal([]byte(call(subject, permissions, "GET", "/v1/closes/"+alpha, nil, 200)), &c))
		return c.Close.CanAssign
	}

	if !canAssign("user_c", reviewer) || canAssign("user_d", viewer) {
		t.Fatal("a reviewer may hand over a close nobody holds; a viewer may not")
	}
	call("user_d", viewer, "GET", "/v1/closes/"+alpha+"/people", nil, 403)
	if body := plan("user_d", viewer, "assign", "user_b", "", 403); !strings.Contains(body, "requires workpapers:review") {
		t.Fatal(body)
	}
	if people := call("user_a", admin, "GET", "/v1/closes/"+alpha+"/people", nil, 200); !strings.Contains(people, `"user_c"`) || strings.Contains(people, `"user_x"`) {
		t.Fatalf("an administrator who reviews nothing still sees whom to reassign to: %s", people)
	}
	plan("user_b", partner, "take", "", "", 200)
	if canAssign("user_c", reviewer) || !canAssign("user_b", partner) || !canAssign("user_a", admin) {
		t.Fatal("only the holder and a firm administrator may hand over a held close")
	}
	// The new holder must be someone else who can review Alpha: not a viewer, even one granted the client, nor a reviewer
	// without Alpha.
	for _, to := range []string{"user_d", "user_e", "user_x", "user_nobody"} {
		if body := plan("user_b", partner, "assign", to, "", 422); !strings.Contains(body, "Choose a teammate who can review Alpha's work") {
			t.Fatalf("%s: %s", to, body)
		}
	}
	if body := plan("user_b", partner, "assign", "user_b", "", 422); !strings.Contains(body, "To hold it yourself, take it") {
		t.Fatal(body)
	}
	if body := plan("user_c", reviewer, "assign", "user_a", "", 409); !strings.Contains(body, "They can hand it over, or ask a firm administrator") {
		t.Fatalf("a reviewer cannot hand over a close someone else holds: %s", body)
	}
	if body := plan("user_b", partner, "assign", "user_c", strings.Repeat("x", 501), 422); !strings.Contains(body, "500 characters") {
		t.Fatal(body)
	}
	if body := plan("user_b", partner, "assign", "user_c", "Covering while I'm away", 200); !strings.Contains(body, `"assigned":true`) || strings.Contains(body, `"owned_by_me":true`) || owner() != "user_c" {
		t.Fatalf("the holder hands the close to Cy: %s %s", body, owner())
	}
	plan("user_b", partner, "release", "", "", 409)
	plan("user_b", partner, "take", "", "", 409)
	// The new holder is told, in Attention and on the close, who handed it to them and why.
	if got := handedTo("user_c", reviewer); got != "user_b handed you this close: Covering while I'm away" {
		t.Fatalf("Cy's attention: %q", got)
	}
	if h := handed("user_c", reviewer); h == nil || h.By != "user_b" || h.Note != "Covering while I'm away" {
		t.Fatalf("Cy's close page: %+v", h)
	}
	// A firm administrator reassigns it whoever holds it.
	plan("user_a", admin, "assign", "user_b", "Back from leave", 200)
	if owner() != "user_b" {
		t.Fatalf("the administrator gave it back to Bea: %s", owner())
	}
	if handedTo("user_c", reviewer) != "" || handed("user_c", reviewer) != nil || handedTo("user_b", partner) != "user_a handed you this close: Back from leave" {
		t.Fatalf("only the current holder is told, of the hand-over that made them the holder: %q %+v %q", handedTo("user_c", reviewer), handed("user_c", reviewer), handedTo("user_b", partner))
	}
	// An administrator who reviews work may also take a colleague's close over themselves; one who reviews none, and anyone
	// else, may not.
	if body := plan("user_a", admin, "assign", "user_b", "", 409); !strings.Contains(body, "already holds this close") {
		t.Fatalf("a hand-over to the holder: %s", body)
	}
	reviewingAdmin := append([]string{"workpapers:review"}, admin...)
	plan("user_c", reviewer, "take", "", "", 409)
	plan("user_a", admin, "take", "", "", 409)
	if takeOver("user_a", admin) || takeOver("user_c", reviewer) || !takeOver("user_a", reviewingAdmin) {
		t.Fatal("only an administrator who reviews may take a held close over")
	}
	plan("user_a", reviewingAdmin, "take", "", "", 200)
	plan("user_a", admin, "assign", "user_b", "", 200)
	// A note is limited in characters, not bytes.
	plan("user_b", partner, "assign", "user_c", strings.Repeat("é", 300), 200)
	plan("user_c", reviewer, "assign", "user_b", "", 200)
	// A signed-off close is reopened before it is handed out.
	_, err = db.Exec(`INSERT INTO close_signoffs(close_id,decision,snapshot_digest,snapshot,note,actor) VALUES($1,'signed_off','fixture','{}','Done','fixture')`, alpha)
	must(err)
	if body := plan("user_b", partner, "assign", "user_c", "", 409); !strings.Contains(body, "signed off") || canAssign("user_b", partner) {
		t.Fatalf("a signed-off close is not handed out: %s", body)
	}
	if body := plan("user_a", reviewingAdmin, "take", "", "", 409); !strings.Contains(body, "signed off") || takeOver("user_a", reviewingAdmin) {
		t.Fatalf("nor taken over: %s", body)
	}
	_, err = db.Exec(`INSERT INTO close_signoffs(close_id,decision,snapshot_digest,snapshot,note,actor) VALUES($1,'reopened','fixture','{}','More work','fixture')`, alpha)
	must(err)
	plan("user_b", partner, "keep", "", "", 200)
	plan("user_b", partner, "release", "", "", 200)

	rows, err := db.Query(`SELECT from_owner,to_owner,actor,reason FROM close_owner_changes WHERE close_id=$1 ORDER BY id`, alpha)
	must(err)
	changes := []string{}
	for rows.Next() {
		var from, to, actor, reason string
		must(rows.Scan(&from, &to, &actor, &reason))
		changes = append(changes, from+">"+to+" by "+actor+" "+reason)
	}
	rows.Close()
	if got := strings.Join(changes, " | "); got != ">user_b by user_b  | user_b>user_c by user_b Covering while I'm away | user_c>user_b by user_a Back from leave | user_b>user_a by user_a  | user_a>user_b by user_a  | user_b>user_c by user_b "+strings.Repeat("é", 300)+" | user_c>user_b by user_c  | user_b> by user_b " {
		t.Fatalf("each change of hands is recorded, and a kept plan is none: %s", got)
	}
	// The close's package keeps the whole history of who held it.
	var pack struct {
		WorkHistory struct {
			CloseOwnerChanges []struct {
				ToOwner string `json:"to_owner"`
				Actor   string `json:"actor"`
				Reason  string `json:"reason"`
			} `json:"close_owner_changes"`
		} `json:"work_history"`
	}
	archive := call("user_b", partner, "GET", "/v1/closes/"+alpha+"/export", nil, 200)
	zipped, err := zip.NewReader(strings.NewReader(archive), int64(len(archive)))
	must(err)
	manifest, err := zipped.Open("manifest.json")
	must(err)
	must(json.NewDecoder(manifest).Decode(&pack))
	if held := pack.WorkHistory.CloseOwnerChanges; len(held) != 8 || held[1].ToOwner != "user_c" || held[1].Reason != "Covering while I'm away" || held[2].Actor != "user_a" {
		t.Fatalf("the close package holds every change of hands: %+v", held)
	}
	activity := call("user_a", admin, "GET", "/v1/closes/security", nil, 200)
	for _, want := range []string{"Took Alpha 2026-09", "Took Alpha 2026-09 from user_b", "Assigned Alpha 2026-09 to user_c: Covering while I'm away", "Assigned Alpha 2026-09 to user_b from user_c: Back from leave", "Released Alpha 2026-09"} {
		if !strings.Contains(activity, want) {
			t.Fatalf("Settings → Security shows %q: %s", want, activity)
		}
	}
}
