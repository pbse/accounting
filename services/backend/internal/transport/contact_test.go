package transport

import (
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	for i := 0; i < 2; i++ {
		if code, reply := sendContact(t, h, "192.0.2.10", body); code != 201 {
			t.Fatalf("note %d: %d %v", i+1, code, reply)
		}
	}
	if code, _ := sendContact(t, h, "192.0.2.10", body); code != 429 {
		t.Fatalf("third note from one address: got %d", code)
	}
	if code, _ := sendContact(t, h, "192.0.2.11", body); code != 201 {
		t.Fatalf("another address: got %d", code)
	}
	// Everyone together: a cap already reached refuses the next note.
	full := system(contactForm(db, contactLimits{perAddress: 5, perHour: 1}))
	if code, _ := sendContact(t, full, "192.0.2.12", body); code != 429 {
		t.Fatalf("hourly cap: got %d", code)
	}
}

func TestContactThrottleForgetsAfterAnHour(t *testing.T) {
	throttle := &contactThrottle{seen: map[string][]time.Time{}}
	start := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	if !throttle.allow("a", 1, start) || throttle.allow("a", 1, start.Add(time.Minute)) {
		t.Fatal("one note an hour")
	}
	if !throttle.allow("a", 1, start.Add(61*time.Minute)) {
		t.Fatal("an hour later the address may send again")
	}
	if len(throttle.seen) != 1 || len(throttle.seen["a"]) != 1 {
		t.Fatalf("old notes are forgotten: %v", throttle.seen)
	}
}
