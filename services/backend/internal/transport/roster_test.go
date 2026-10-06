package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
	"github.com/golang-jwt/jwt/v5"
)

// Every active client on the firm's roster owes a close each ended month: a month nobody opened shows in the morning
// brief, to whoever can work on the client, until it is opened; opened, it takes the firm's monthly close template (the
// client's own overrides first). Only a firm administrator changes the roster and the template, with an owner who can do
// the work; a client marked inactive owes nothing more.
func TestTheRosterListsEveryMonthOwed(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	org := "org_Roster" + rand.Text()
	// Ann and Bea review every client; Cy reviews only Beta, which he is assigned to.
	asFirm(t, map[string]string{"user_a": "partner", "user_b": "partner", "user_c": "reviewer"}, org)
	for _, q := range []string{`INSERT INTO organizations(id,name) VALUES($1,'Roster fixture')`,
		`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) SELECT $1,s,true,'Synthetic','fixture' FROM unnest(ARRAY['user_a','user_b','user_c']) s`,
		`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_c','Beta',true,'Synthetic','fixture')`} {
		_, err = db.Exec(q, org)
		must(err)
	}
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM close_goals WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM workflow_runs WHERE organization_id=$1`,
			`DELETE FROM close_routines WHERE organization_id=$1`, `DELETE FROM client_aliases WHERE organization_id=$1`, `DELETE FROM close_deletion_receipts WHERE organization_id=$1`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_roster WHERE organization_id=$1`, `DELETE FROM close_templates WHERE organization_id=$1`,
			`DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			if _, e := db.Exec(q, org); e != nil {
				t.Errorf("cleanup %s: %v", q, e)
			}
		}
	})
	ended := endedMonth(time.Now())
	before := monthEnd(ended).AddDate(0, -1, 0).Format("2006-01")
	_, err = db.Exec(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha',$2)`, org, before)
	must(err)

	store := savedCloses(db, strings.Repeat("a", 32))
	call := func(subject string, permissions []string, method, path string, body any, want int) string {
		t.Helper()
		var reader *bytes.Reader
		if body == nil {
			reader = bytes.NewReader(nil)
		} else {
			raw, _ := json.Marshal(body)
			reader = bytes.NewReader(raw)
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
	admin := []string{"workpapers:review", "clients:all", "members:manage"}
	partner, reviewer := []string{"workpapers:review", "clients:all"}, []string{"workpapers:review"}
	ten, five := 10, 5
	cy, nobody := "user_c", "user_x"

	// The template's owner must review every client; a client's own owner, that client.
	call("user_b", partner, "POST", "/v1/closes/close-template", map[string]any{"due_days": ten, "owner": "user_b", "reason": "Monthly closes"}, 403)
	if body := call("user_a", admin, "POST", "/v1/closes/close-template", map[string]any{"due_days": ten, "owner": "user_c", "reason": "Monthly closes"}, 422); !strings.Contains(body, "reviews every client") {
		t.Fatal(body)
	}
	call("user_a", admin, "POST", "/v1/closes/close-template", map[string]any{"due_days": ten, "owner": "user_b", "reason": "Monthly closes"}, 200)
	call("user_a", admin, "POST", "/v1/closes/roster", map[string]any{"entity": "Alpha", "active": true, "since": before, "reason": "A client"}, 200)
	call("user_a", admin, "POST", "/v1/closes/roster", map[string]any{"entity": "Gamma", "active": true, "since": ended, "owner": cy, "reason": "A client"}, 422)
	call("user_a", admin, "POST", "/v1/closes/roster", map[string]any{"entity": "Beta", "active": true, "since": ended, "owner": nobody, "reason": "A client"}, 422)
	call("user_a", admin, "POST", "/v1/closes/roster", map[string]any{"entity": "Beta", "active": true, "since": ended, "due_days": five, "owner": cy, "reason": "Cy's client"}, 200)
	call("user_b", partner, "POST", "/v1/closes/roster", map[string]any{"entity": "Delta", "active": true, "since": ended, "reason": "A client"}, 403)
	// Every owed month is listed on each read, so the first is within the last five years.
	if body := call("user_a", admin, "POST", "/v1/closes/roster", map[string]any{"entity": "Delta", "active": true, "since": "0001-01", "reason": "A client"}, 422); !strings.Contains(body, "within the last five years") {
		t.Fatal(body)
	}

	type owed struct {
		NotOpened []owedMonth `json:"not_opened"`
	}
	brief := func(subject string, permissions []string, filter ...string) []string {
		t.Helper()
		var got owed
		must(json.Unmarshal([]byte(call(subject, permissions, "GET", "/v1/closes/morning?client="+strings.Join(filter, ""), nil, 200)), &got))
		out := []string{}
		for _, o := range got.NotOpened {
			out = append(out, o.Entity+" "+o.Period+" due "+o.DueOn+map[bool]string{true: " mine", false: ""}[o.Mine]+map[bool]string{true: " by hand-over", false: ""}[o.HandOver])
		}
		return out
	}
	due := func(days int) string { return monthEnd(ended).AddDate(0, 0, days).Format("2006-01-02") }
	// Alpha owes the month after its last close; Beta, from the month it joined, on its own terms.
	if got := strings.Join(brief("user_b", partner), " | "); got != "Alpha "+ended+" due "+due(10)+" mine | Beta "+ended+" due "+due(5) {
		t.Fatalf("the months owed and not opened: %s", got)
	}
	if got := strings.Join(brief("user_c", reviewer), " | "); got != "Beta "+ended+" due "+due(5)+" mine" {
		t.Fatalf("a reviewer sees the months of the clients assigned to them: %s", got)
	}
	// A month is yours only while you can do the work: Bea without review permission is not shown Alpha's as hers.
	if got := strings.Join(brief("user_b", []string{"clients:all"}), " | "); strings.Contains(got, "mine") {
		t.Fatalf("not yours without review permission: %s", got)
	}
	// A month the client's monthly hand-over is set to open still shows, as the hand-over's, until a close exists.
	_, err = db.Exec(`INSERT INTO close_routines(organization_id,client_entity,author,via_grant,finish,last_period) VALUES($1,'Alpha','user_b',false,'review',$2)`, org, before)
	must(err)
	if got := strings.Join(brief("user_b", partner, "Alpha"), " | "); got != "Alpha "+ended+" due "+due(10)+" mine by hand-over" {
		t.Fatalf("a month the hand-over has yet to open: %s", got)
	}
	_, err = db.Exec(`DELETE FROM close_routines WHERE organization_id=$1 AND client_entity='Alpha'`, org)
	must(err)
	// Work's client or month filter narrows them as it narrows the due closes.
	if beta, month, other := brief("user_b", partner, "bet"), brief("user_b", partner, ended), brief("user_b", partner, "Gamma"); len(beta) != 1 || len(month) != 2 || len(other) != 0 {
		t.Fatalf("filtered: %v %v %v", beta, month, other)
	}

	// Opening the month gives it the template's due date and owner, as a change of hands.
	var opened savedClose
	must(json.Unmarshal([]byte(call("user_a", admin, "POST", "/v1/closes", map[string]string{"entity": "Alpha", "period": ended}, 200)), &opened))
	var dueOn, holder, actor string
	must(db.QueryRow(`SELECT c.due_on::text,c.owner,o.actor FROM client_closes c JOIN close_owner_changes o ON o.close_id=c.id WHERE c.id::text=$1`, opened.ID).Scan(&dueOn, &holder, &actor))
	if dueOn != due(10) || holder != "user_b" || actor != "monthly close template" {
		t.Fatalf("the opened month takes the template: %s %s %s", dueOn, holder, actor)
	}
	if got := strings.Join(brief("user_b", partner), " | "); got != "Beta "+ended+" due "+due(5) {
		t.Fatalf("an opened month is no longer owed: %s", got)
	}

	// The roster shows each client with the months it owes; marking one inactive ends what it owes.
	var roster struct {
		Template  map[string]any `json:"template"`
		Clients   []rosterClient `json:"clients"`
		CanManage bool           `json:"can_manage"`
	}
	call("user_c", reviewer, "GET", "/v1/closes/roster", nil, 403)
	must(json.Unmarshal([]byte(call("user_b", partner, "GET", "/v1/closes/roster", nil, 200)), &roster))
	if roster.CanManage || roster.Template["owner"] != "user_b" || len(roster.Clients) != 2 || roster.Clients[0].Entity != "Alpha" || roster.Clients[0].LastPeriod != ended ||
		roster.Clients[1].Entity != "Beta" || strings.Join(roster.Clients[1].Owed, ",") != ended || *roster.Clients[1].Owner != "user_c" || *roster.Clients[1].DueDays != 5 {
		t.Fatalf("the roster: %+v", roster)
	}
	// An administrator chooses owners from the members who can do the work.
	var people struct {
		People []rosterPerson `json:"people"`
	}
	must(json.Unmarshal([]byte(call("user_a", admin, "GET", "/v1/closes/roster", nil, 200)), &people))
	got := []string{}
	for _, p := range people.People {
		got = append(got, p.Subject+":"+map[bool]string{true: "all", false: strings.Join(p.Clients, ",")}[p.FirmWide])
	}
	if strings.Join(got, " ") != "user_a:all user_b:all user_c:beta" {
		t.Fatalf("the people an administrator chooses owners from: %v", got)
	}

	// A month opened under a confirmed alias of the client is the client's; the roster names the client by its own name.
	call("user_a", admin, "POST", "/v1/closes/roster", map[string]any{"entity": "Gamma", "active": true, "since": ended, "reason": "A client"}, 200)
	_, err = db.Exec(`INSERT INTO client_aliases(organization_id,client_entity,alias,confirmed_by) VALUES($1,'Gamma','gamma group','fixture')`, org)
	must(err)
	if got := brief("user_b", partner, "Gamma"); len(got) != 1 {
		t.Fatalf("Gamma owes its month: %v", got)
	}
	_, err = db.Exec(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Gamma Group',$2)`, org, ended)
	must(err)
	if got := brief("user_b", partner, "Gamma"); len(got) != 0 {
		t.Fatalf("a month opened under the client's alias is the client's: %v", got)
	}
	if body := call("user_a", admin, "POST", "/v1/closes/roster", map[string]any{"entity": "Gamma Group", "active": true, "since": ended, "reason": "A client"}, 422); !strings.Contains(body, "confirmed alias of gamma") {
		t.Fatalf("an alias is not a second client: %s", body)
	}
	call("user_a", admin, "POST", "/v1/closes/roster", map[string]any{"entity": "Gamma", "active": false, "since": ended, "reason": "Fixture done"}, 200)

	// A name added on its own and later confirmed as an alias of a rostered client is that client: one entry, one month.
	call("user_a", admin, "POST", "/v1/closes/roster", map[string]any{"entity": "Epsilon", "active": true, "since": ended, "reason": "A client"}, 200)
	call("user_a", admin, "POST", "/v1/closes/roster", map[string]any{"entity": "Epsilon Partners", "active": true, "since": ended, "reason": "A client"}, 200)
	_, err = db.Exec(`INSERT INTO client_aliases(organization_id,client_entity,alias,confirmed_by) VALUES($1,'Epsilon','epsilon partners','fixture')`, org)
	must(err)
	if got := brief("user_b", partner, "Epsilon"); len(got) != 1 || !strings.HasPrefix(got[0], "Epsilon "+ended) {
		t.Fatalf("a confirmed alias is one client, owing one month under its own name: %v", got)
	}
	call("user_a", admin, "POST", "/v1/closes/roster", map[string]any{"entity": "Epsilon", "active": false, "since": ended, "reason": "Fixture done"}, 200)
	if got := brief("user_b", partner, "Epsilon"); len(got) != 0 {
		t.Fatalf("its latest decision covers both names: %v", got)
	}

	// A month opened under a client's confirmed alias is the client's: the template applies, but Cy, assigned to "Zeta"
	// and not to "Zeta Co", cannot open a close named "Zeta Co", so it is not given to him. Deactivating the client stops a
	// hand-over set up under the alias.
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_c','Zeta',true,'Synthetic','fixture')`, org)
	must(err)
	call("user_a", admin, "POST", "/v1/closes/roster", map[string]any{"entity": "Zeta", "active": true, "since": ended, "owner": cy, "reason": "Cy's client"}, 200)
	_, err = db.Exec(`INSERT INTO client_aliases(organization_id,client_entity,alias,confirmed_by,confirmed_at) VALUES($1,'Zeta','zeta co','fixture',now()-interval '1 day')`, org)
	must(err)
	var zeta savedClose
	must(json.Unmarshal([]byte(call("user_a", admin, "POST", "/v1/closes", map[string]string{"entity": "Zeta Co", "period": ended}, 200)), &zeta))
	must(db.QueryRow(`SELECT owner FROM client_closes WHERE id::text=$1`, zeta.ID).Scan(&holder))
	var zetaDue string
	must(db.QueryRow(`SELECT due_on::text FROM client_closes WHERE id::text=$1`, zeta.ID).Scan(&zetaDue))
	if holder != "" || zetaDue != due(10) {
		t.Fatalf("the month opened under Zeta's alias takes the template, without an owner who cannot open it: %q %s", holder, zetaDue)
	}
	_, err = db.Exec(`INSERT INTO close_routines(organization_id,client_entity,author,via_grant,finish,last_period) VALUES($1,'Zeta Co','user_b',false,'review',$2)`, org, ended)
	must(err)
	call("user_a", admin, "POST", "/v1/closes/roster", map[string]any{"entity": "Zeta", "active": false, "since": ended, "reason": "Fixture done"}, 200)
	var zetaStopped bool
	must(db.QueryRow(`SELECT stopped_at IS NOT NULL FROM close_routines WHERE organization_id=$1 AND client_entity='Zeta Co'`, org).Scan(&zetaStopped))
	if !zetaStopped {
		t.Fatal("deactivating Zeta stops the hand-over set up under its alias")
	}

	// Assignments follow the app's access rule, by name: Cy, assigned only under Eta's alias "Eta Co", cannot open closes
	// named Eta, so he is neither Eta's owner nor shown its month.
	_, err = db.Exec(`INSERT INTO client_aliases(organization_id,client_entity,alias,confirmed_by,confirmed_at) VALUES($1,'Eta','eta co','fixture',now()-interval '1 day')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_c','Eta Co',true,'Synthetic','fixture')`, org)
	must(err)
	call("user_a", admin, "POST", "/v1/closes/roster", map[string]any{"entity": "Eta", "active": true, "since": ended, "owner": cy, "reason": "Cy's client"}, 422)
	call("user_a", admin, "POST", "/v1/closes/roster", map[string]any{"entity": "Eta", "active": true, "since": ended, "reason": "A client"}, 200)
	if got := brief("user_c", reviewer, "Eta"); slices.ContainsFunc(got, func(m string) bool { return strings.HasPrefix(m, "Eta ") }) {
		t.Fatalf("Cy is not shown a month he cannot open: %v", got)
	}
	call("user_a", admin, "POST", "/v1/closes/roster", map[string]any{"entity": "Eta", "active": false, "since": ended, "reason": "Fixture done"}, 200)

	// A month whose close was deleted under the retention policy was done: it is not owed again.
	call("user_a", admin, "POST", "/v1/closes/roster", map[string]any{"entity": "Theta", "active": true, "since": ended, "reason": "A client"}, 200)
	if got := brief("user_b", partner, "Theta"); len(got) != 1 {
		t.Fatalf("Theta owes its month: %v", got)
	}
	_, err = db.Exec(`INSERT INTO close_deletion_receipts(organization_id,close_id,entity_digest,period,production_postings,actor,reason_code) VALUES($1,0,$2,$3,0,'fixture','retention_expired')`,
		org, preparation.Digest([]byte(org+"\x00"+strings.ToLower("Theta")+"\x00"+ended)), ended)
	must(err)
	if got := brief("user_b", partner, "Theta"); len(got) != 0 {
		t.Fatalf("a month deleted under the retention policy is not owed again: %v", got)
	}
	call("user_a", admin, "POST", "/v1/closes/roster", map[string]any{"entity": "Theta", "active": false, "since": ended, "reason": "Fixture done"}, 200)
	// So is a month whose close was opened under a confirmed alias, "Iota Co.", and deleted: its receipt keeps the digest
	// of the client the close belonged to.
	_, err = db.Exec(`INSERT INTO client_aliases(organization_id,client_entity,alias,confirmed_by,confirmed_at) VALUES($1,'Iota','iota co','fixture',now()-interval '1 day')`, org)
	must(err)
	call("user_a", admin, "POST", "/v1/closes/roster", map[string]any{"entity": "Iota", "active": true, "since": ended, "reason": "A client"}, 200)
	_, err = db.Exec(`INSERT INTO close_deletion_receipts(organization_id,close_id,entity_digest,period,production_postings,actor,reason_code,client_digest) VALUES($1,0,$2,$3,0,'fixture','retention_expired',$4)`,
		org, preparation.Digest([]byte(org+"\x00iota co.\x00"+ended)), ended, preparation.Digest([]byte(org+"\x00iota\x00"+ended)))
	must(err)
	if got := brief("user_b", partner, "Iota"); len(got) != 0 {
		t.Fatalf("a month deleted under the client's alias is not owed again: %v", got)
	}
	call("user_a", admin, "POST", "/v1/closes/roster", map[string]any{"entity": "Iota", "active": false, "since": ended, "reason": "Fixture done"}, 200)

	// A client can have no due date even though the template sets one.
	zero := 0
	call("user_a", admin, "POST", "/v1/closes/roster", map[string]any{"entity": "Delta", "active": true, "since": ended, "due_days": zero, "reason": "No deadline"}, 200)
	if got := strings.Join(brief("user_b", partner, "Delta"), " | "); got != "Delta "+ended+" due  mine" {
		t.Fatalf("a client with no due date: %s", got)
	}
	call("user_a", admin, "POST", "/v1/closes/roster", map[string]any{"entity": "Delta", "active": false, "since": ended, "reason": "Fixture done"}, 200)

	// An owner who can no longer do the work is not given the month: Cy loses Beta, so its month opens dated but unheld.
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_c','Beta',false,'Reassigned','fixture')`, org)
	must(err)
	must(json.Unmarshal([]byte(call("user_a", admin, "POST", "/v1/closes", map[string]string{"entity": "Beta", "period": ended}, 200)), &opened))
	must(db.QueryRow(`SELECT due_on::text,owner FROM client_closes WHERE id::text=$1`, opened.ID).Scan(&dueOn, &holder))
	if dueOn != due(5) || holder != "" {
		t.Fatalf("Beta's month is dated but not given to Cy: %s %q", dueOn, holder)
	}

	// Marking a client inactive ends what it owes and stops its monthly hand-over.
	_, err = db.Exec(`INSERT INTO close_routines(organization_id,client_entity,author,via_grant,finish,last_period) VALUES($1,'Beta','user_b',false,'review',$2)`, org, ended)
	must(err)
	call("user_a", admin, "POST", "/v1/closes/roster", map[string]any{"entity": "beta", "active": false, "since": ended, "reason": "Left the firm"}, 200)
	if got := brief("user_b", partner); len(got) != 0 {
		t.Fatalf("an inactive client owes nothing: %v", got)
	}
	var stopped string
	must(db.QueryRow(`SELECT COALESCE(stop_reason,'') FROM close_routines WHERE organization_id=$1 AND client_entity='Beta'`, org).Scan(&stopped))
	if stopped != "Stopped when beta left the firm's roster: Left the firm" {
		t.Fatalf("the client's monthly hand-over stops: %q", stopped)
	}
	must(json.Unmarshal([]byte(call("user_a", admin, "GET", "/v1/closes/roster", nil, 200)), &roster))
	if !roster.CanManage || roster.Clients[1].Active || roster.Clients[1].Reason != "Left the firm" {
		t.Fatalf("the roster keeps the inactive client, last: %+v", roster.Clients)
	}
	// An inactive client gets no new month: not from the new-close route (its existing months still open), not from a
	// new monthly hand-over, and a hand-over left running stops instead of opening one.
	next := monthEnd(ended).AddDate(0, 0, 1).Format("2006-01")
	if body := call("user_a", admin, "POST", "/v1/closes", map[string]string{"entity": "Beta", "period": next}, 409); !strings.Contains(body, "inactive on the firm's roster") {
		t.Fatalf("a new month of an inactive client: %s", body)
	}
	call("user_a", admin, "POST", "/v1/closes", map[string]string{"entity": "Beta", "period": ended}, 200)
	if body := call("user_b", partner, "POST", "/v1/closes/"+opened.ID+"/goal", map[string]any{"confirmed": true, "every_month": true}, 409); !strings.Contains(body, "inactive on the firm's roster") {
		t.Fatalf("a monthly hand-over for an inactive client: %s", body)
	}
	// Delta's next month is already open (from before it left); its hand-over still stops rather than hand that over.
	var deltaClose string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Delta',$2) RETURNING id::text`, org, ended).Scan(&deltaClose))
	_, err = db.Exec(`INSERT INTO close_routines(organization_id,client_entity,author,via_grant,finish,last_period) VALUES($1,'Delta','user_b',false,'review',$2)`, org, before)
	must(err)
	sweepCloseRoutines(context.Background(), db, time.Now())
	var deltaStopped string
	var deltaCloses, deltaGoals int
	must(db.QueryRow(`SELECT COALESCE(stop_reason,''),(SELECT count(*) FROM client_closes WHERE organization_id=$1 AND entity='Delta'),(SELECT count(*) FROM close_goals WHERE close_id::text=$2)
 FROM close_routines WHERE organization_id=$1 AND client_entity='Delta'`, org, deltaClose).Scan(&deltaStopped, &deltaCloses, &deltaGoals))
	if deltaStopped != "Stopped because Delta is inactive on the firm's roster." || deltaCloses != 1 || deltaGoals != 0 {
		t.Fatalf("a hand-over for an inactive client stops, opening and handing over nothing: %q %d %d", deltaStopped, deltaCloses, deltaGoals)
	}
}

// The local development firm, which has no WorkOS, manages its own roster.
func TestLocalDevelopmentManagesTheRoster(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	entity := "Local roster " + rand.Text()
	t.Cleanup(func() { db.Exec(`DELETE FROM client_roster WHERE client_entity=$1`, entity) })
	token := strings.Repeat("l", 32)
	handler := apiWithStore(db, token)
	call := func(method, path string, body any, want int) string {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	t.Cleanup(func() {
		db.Exec(`DELETE FROM close_templates WHERE organization_id='local-development'`)
		db.Exec(`DELETE FROM client_closes WHERE entity=$1`, entity)
	})
	ended := endedMonth(time.Now())
	call("POST", "/v1/closes/roster", map[string]any{"entity": entity, "active": true, "since": ended, "reason": "Local client"}, 200)
	var roster struct {
		CanManage bool           `json:"can_manage"`
		Clients   []rosterClient `json:"clients"`
		People    []rosterPerson `json:"people"`
	}
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(json.Unmarshal([]byte(call("GET", "/v1/closes/roster", nil, 200)), &roster))
	if !roster.CanManage || len(roster.People) == 0 || !slices.ContainsFunc(roster.Clients, func(c rosterClient) bool { return c.Entity == entity }) {
		t.Fatalf("the local roster offers its reader as an owner: %+v", roster)
	}
	// The local firm records no members; its reader can still be the template's owner and is given the month opened.
	me := roster.People[len(roster.People)-1].Subject
	call("POST", "/v1/closes/close-template", map[string]any{"due_days": 2, "owner": me, "reason": "Local template"}, 200)
	var opened savedClose
	must(json.Unmarshal([]byte(call("POST", "/v1/closes", map[string]string{"entity": entity, "period": ended}, 200)), &opened))
	var owner string
	must(db.QueryRow(`SELECT owner FROM client_closes WHERE id::text=$1`, opened.ID).Scan(&owner))
	if owner != me {
		t.Fatalf("the local template's owner holds the month: %q, want %q", owner, me)
	}
}
