package transport

import (
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func sendContact(t *testing.T, h http.Handler, address, body string) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequest("POST", "/v1/contact", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("CF-Connecting-IP", address)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var reply map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
		t.Fatalf("reply %d is not JSON: %s", w.Code, w.Body.String())
	}
	return w.Code, reply
}

func TestContactRefusesNotesItCannotForwardSafely(t *testing.T) {
	h := contactForm(nil, defaultContactLimits)
	for name, body := range map[string]string{
		"no name":             `{"name":" ","email":"ana@firm.example","firm":"Firm"}`,
		"no firm":             `{"name":"Ana","email":"ana@firm.example","firm":""}`,
		"not an address":      `{"name":"Ana","email":"ana at firm","firm":"Firm"}`,
		"a display name":      `{"name":"Ana","email":"Ana <ana@firm.example>","firm":"Firm"}`,
		"a line break":        `{"name":"Ana\r\nBcc: someone@else.example","email":"ana@firm.example","firm":"Firm"}`,
		"a long name":         `{"name":"` + strings.Repeat("a", 121) + `","email":"ana@firm.example","firm":"Firm"}`,
		"a long note":         `{"name":"Ana","email":"ana@firm.example","firm":"Firm","note":"` + strings.Repeat("n", 4001) + `"}`,
		"a control character": `{"name":"Ana","email":"ana@firm.example","firm":"Firm","note":"\u0007"}`,
		"an unknown field":    `{"name":"Ana","email":"ana@firm.example","firm":"Firm","phone":"1"}`,
	} {
		if code, _ := sendContact(t, h, "198.51.100.1", body); code != 400 {
			t.Errorf("%s: got %d, want 400", name, code)
		}
	}
	// A filled-in trap field is thanked and never kept or forwarded, even with no database.
	if code, reply := sendContact(t, h, "198.51.100.1", `{"name":"Bot","email":"bot@spam.example","firm":"Spam","website":"https://spam.example"}`); code != 201 || reply["forward"] != false {
		t.Fatalf("trap: %d %v", code, reply)
	}
	// A good note with no database is refused, not lost silently.
	if code, _ := sendContact(t, h, "198.51.100.1", `{"name":"Ana","email":"ana@firm.example","firm":"Firm"}`); code != 503 {
		t.Fatalf("no database: got %d", code)
	}
}

func TestContactAllowsTheSiteOnAnotherOrigin(t *testing.T) {
	t.Setenv("FRONTEND_ORIGIN", "https://getkrith.com")
	h := API()
	for origin, want := range map[string]int{"https://getkrith.com": 204, "https://untrusted.example": 403} {
		r := httptest.NewRequest("OPTIONS", "/v1/contact", nil)
		r.Header.Set("Origin", origin)
		r.Header.Set("Access-Control-Request-Method", "POST")
		r.Header.Set("Access-Control-Request-Headers", "content-type")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("preflight from %s: got %d, want %d", origin, w.Code, want)
		}
	}
}

func TestContactKeepsANoteAndReturnsItForForwarding(t *testing.T) {
	db := adminDB(t)
	marker := "Contact test " + rand.Text()
	t.Cleanup(func() { db.Exec(`DELETE FROM contact_notes WHERE firm=$1`, marker) })
	h := apiWithStore(db, "")
	code, reply := sendContact(t, h, "203.0.113."+rand.Text()[:3], `{"name":" Ana Lima ","email":"ana@firm.example","firm":"`+marker+`","note":"40 clients on QuickBooks Online.\nMost closes take a week.","website":""}`)
	if code != 201 || reply["forward"] != true || reply["name"] != "Ana Lima" || reply["email"] != "ana@firm.example" || reply["note"] != "40 clients on QuickBooks Online.\nMost closes take a week." {
		t.Fatalf("kept note: %d %v", code, reply)
	}
	var name, note string
	if err := db.QueryRow(`SELECT name,note FROM contact_notes WHERE id=$1 AND firm=$2`, int64(reply["id"].(float64)), marker).Scan(&name, &note); err != nil || name != "Ana Lima" {
		t.Fatalf("stored note: %q %q %v", name, note, err)
	}
}

func TestContactLimitsEachAddressAndEveryone(t *testing.T) {
	db := adminDB(t)
	marker := "Contact limit " + rand.Text()
	t.Cleanup(func() { db.Exec(`DELETE FROM contact_notes WHERE firm=$1`, marker) })
	h := system(contactForm(db, contactLimits{perAddress: 2, perHour: 1000}))
	body := `{"name":"Ana","email":"ana@firm.example","firm":"` + marker + `"}`
	one, another := "limit-"+rand.Text(), "limit-"+rand.Text()
	for i := 0; i < 2; i++ {
		if code, reply := sendContact(t, h, one, body); code != 201 {
			t.Fatalf("note %d: %d %v", i+1, code, reply)
		}
	}
	if code, _ := sendContact(t, h, one, body); code != 429 {
		t.Fatalf("third note from one address: got %d", code)
	}
	// The count is kept with the notes, so a restarted API still holds the address to it.
	if code, _ := sendContact(t, system(contactForm(db, contactLimits{perAddress: 2, perHour: 1000})), one, body); code != 429 {
		t.Fatalf("third note after a restart: got %d", code)
	}
	if code, _ := sendContact(t, h, another, body); code != 201 {
		t.Fatalf("another address: got %d", code)
	}
	// Everyone together: a cap already reached refuses the next note.
	full := system(contactForm(db, contactLimits{perAddress: 5, perHour: 1}))
	if code, _ := sendContact(t, full, "192.0.2.12", body); code != 429 {
		t.Fatalf("hourly cap: got %d", code)
	}
}

func TestContactSenderIsForgottenAfterAnHour(t *testing.T) {
	db := adminDB(t)
	marker := "Contact hour " + rand.Text()
	t.Cleanup(func() { db.Exec(`DELETE FROM contact_notes WHERE firm=$1`, marker) })
	address := "hour-" + rand.Text()
	if _, err := db.Exec(`INSERT INTO contact_notes(name,email,firm,sender_digest,created_at) VALUES('Ana','ana@firm.example',$1,$2,now()-interval '61 minutes')`, marker, contactSender(address)); err != nil {
		t.Fatal(err)
	}
	h := system(contactForm(db, contactLimits{perAddress: 1, perHour: 1000}))
	if code, reply := sendContact(t, h, address, `{"name":"Ana","email":"ana@firm.example","firm":"`+marker+`"}`); code != 201 {
		t.Fatalf("an hour later the address may send again: %d %v", code, reply)
	}
	var kept int
	if err := db.QueryRow(`SELECT count(*) FROM contact_notes WHERE firm=$1 AND sender_digest IS NOT NULL`, marker).Scan(&kept); err != nil || kept != 1 {
		t.Fatalf("only the new note keeps its sender's digest: %d %v", kept, err)
	}
	if strings.Contains(contactSender(address), address) || len(contactSender(address)) != 64 {
		t.Fatal("the address is kept only as a digest")
	}
}

func TestContactRetryReturnsTheKeptNote(t *testing.T) {
	db := adminDB(t)
	marker := "Contact retry " + rand.Text()
	t.Cleanup(func() { db.Exec(`DELETE FROM contact_notes WHERE firm=$1`, marker) })
	body := `{"name":"Ana","email":"ana@firm.example","firm":"` + marker + `","submission":"` + "retry-" + strings.ToLower(rand.Text()) + `"}`
	h := system(contactForm(db, contactLimits{perAddress: 5, perHour: 1000}))
	code, first := sendContact(t, h, "retry-"+rand.Text(), body)
	if code != 201 || first["forward"] != true {
		t.Fatalf("first: %d %v", code, first)
	}
	// The answer was lost and the visitor sends again, even once the hour's notes are all taken: the same note comes
	// back, not forwarded twice.
	full := system(contactForm(db, contactLimits{perAddress: 5, perHour: 0}))
	code, again := sendContact(t, full, "retry-"+rand.Text(), body)
	if code != 201 || again["forward"] != false || again["id"] != first["id"] {
		t.Fatalf("retry: %d %v", code, again)
	}
	var notes int
	if err := db.QueryRow(`SELECT count(*) FROM contact_notes WHERE firm=$1`, marker).Scan(&notes); err != nil || notes != 1 {
		t.Fatalf("one note kept: %d %v", notes, err)
	}
	// The same key with other words is not answered with the earlier note.
	edited := strings.Replace(body, `"name":"Ana"`, `"name":"Ana Lima"`, 1)
	if code, reply := sendContact(t, h, "retry-"+rand.Text(), edited); code != 409 {
		t.Fatalf("an edited note under a kept key: %d %v", code, reply)
	}
	if code, _ := sendContact(t, h, "retry-x", `{"name":"Ana","email":"ana@firm.example","firm":"F","submission":"bad key!"}`); code != 400 {
		t.Fatalf("a malformed submission key: got %d", code)
	}
}

func TestContactNotesAreCountedOneAtATime(t *testing.T) {
	db := adminDB(t)
	marker := "Contact turn " + rand.Text()
	t.Cleanup(func() { db.Exec(`DELETE FROM contact_notes WHERE firm=$1`, marker) })
	// While another note holds the turn, a note waits rather than counting alongside it.
	other, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer other.Rollback()
	if _, err := other.Exec(`SELECT pg_advisory_xact_lock(hashtext('contact_notes'))`); err != nil {
		t.Fatal(err)
	}
	h := system(contactForm(db, defaultContactLimits))
	done := make(chan int, 1)
	go func() {
		r := httptest.NewRequest("POST", "/v1/contact", strings.NewReader(`{"name":"Ana","email":"ana@firm.example","firm":"`+marker+`"}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("CF-Connecting-IP", "turn-"+rand.Text())
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		done <- w.Code
	}()
	select {
	case code := <-done:
		t.Fatalf("a note was counted while another held the turn: %d", code)
	case <-time.After(300 * time.Millisecond):
	}
	other.Rollback()
	select {
	case code := <-done:
		if code != 201 {
			t.Fatalf("after its turn: got %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the note never took its turn")
	}
}

func TestStaffReadContactNotesNewestFirst(t *testing.T) {
	db := adminDB(t)
	t.Setenv("WORKOS_CLIENT_ID", "")
	token := strings.Repeat("c", 32)
	marker := "Contact staff " + rand.Text()
	t.Cleanup(func() { db.Exec(`DELETE FROM contact_notes WHERE firm=$1`, marker) })
	var first, second int64
	for _, id := range []*int64{&first, &second} {
		if err := db.QueryRow(`INSERT INTO contact_notes(name,email,firm,note) VALUES('Ana','ana@firm.example',$1,'Forwarding failed') RETURNING id`, marker).Scan(id); err != nil {
			t.Fatal(err)
		}
	}
	h := apiWithStore(db, token)
	page := adminJSON(t, h, token, "GET", "/v1/admin/contact-notes?before="+strconv.FormatInt(second+1, 10), "", 200)
	notes := page["notes"].([]any)
	if len(notes) < 2 || notes[0].(map[string]any)["id"].(float64) != float64(second) || notes[1].(map[string]any)["firm"] != marker {
		t.Fatalf("newest first: %v", mustString(page))
	}
	older := adminJSON(t, h, token, "GET", "/v1/admin/contact-notes?before="+strconv.FormatInt(second, 10), "", 200)
	if got := older["notes"].([]any)[0].(map[string]any)["id"].(float64); got != float64(first) {
		t.Fatalf("before the newest: got %v", got)
	}
	adminJSON(t, h, token, "GET", "/v1/admin/contact-notes?before=x", "", 422)
	adminJSON(t, h, "", "GET", "/v1/admin/contact-notes", "", 401)
}
