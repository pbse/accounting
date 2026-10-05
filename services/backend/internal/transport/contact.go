package transport

import (
	"database/sql"
	"net"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// The public contact page (docs/marketing-site.md). The API keeps each note in contact_notes; the edge then forwards
// it to our inbox from the fields this reply returns. A note that fills in the hidden trap field, which people never
// see, is thanked and dropped.
type contactNote struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Firm    string `json:"firm"`
	Note    string `json:"note"`
	Website string `json:"website"`
}

type contactLimits struct {
	perAddress, perHour int
}

// Five notes an hour from one address, sixty an hour from everyone: enough for people, little for a script.
var defaultContactLimits = contactLimits{perAddress: 5, perHour: 60}

type contactThrottle struct {
	mu   sync.Mutex
	seen map[string][]time.Time
}

// allow records a note from address unless it already sent the most allowed in the past hour.
func (t *contactThrottle) allow(address string, most int, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	cutoff := now.Add(-time.Hour)
	for key, times := range t.seen {
		kept := times[:0]
		for _, at := range times {
			if at.After(cutoff) {
				kept = append(kept, at)
			}
		}
		if len(kept) == 0 {
			delete(t.seen, key)
		} else {
			t.seen[key] = kept
		}
	}
	if len(t.seen[address]) >= most {
		return false
	}
	t.seen[address] = append(t.seen[address], now)
	return true
}

// Cloudflare sets CF-Connecting-IP to the visitor's address and replaces any value a visitor sends.
func contactAddress(r *http.Request) string {
	if address := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); address != "" {
		return address
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// cleanField trims a one-line field and refuses any control character, so no field can carry a line break into the
// email the edge sends.
func cleanField(value string, most int) (string, bool) {
	value = strings.TrimSpace(value)
	return value, value != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= most && !strings.ContainsFunc(value, unicode.IsControl)
}

func contactForm(db *sql.DB, limits contactLimits) http.HandlerFunc {
	throttle := &contactThrottle{seen: map[string][]time.Time{}}
	return func(w http.ResponseWriter, r *http.Request) {
		var in contactNote
		if !decode(w, r, &in) {
			return
		}
		name, okName := cleanField(in.Name, 120)
		email, okEmail := cleanField(in.Email, 254)
		firm, okFirm := cleanField(in.Firm, 160)
		note := strings.TrimSpace(in.Note)
		okNote := utf8.ValidString(note) && utf8.RuneCountInString(note) <= 4000 && !strings.ContainsFunc(note, func(c rune) bool { return unicode.IsControl(c) && c != '\n' && c != '\r' && c != '\t' })
		if parsed, err := mail.ParseAddress(email); err != nil || parsed.Address != email || parsed.Name != "" {
			okEmail = false
		}
		if !okName || !okEmail || !okFirm || !okNote {
			problem(w, 400, "Add your name, a work email and your firm, and keep the note under 4,000 characters.")
			return
		}
		if strings.TrimSpace(in.Website) != "" {
			jsonReply(w, 201, map[string]any{"forward": false})
			return
		}
		if db == nil {
			problem(w, 503, "Notes can't be received right now. Write to hello@getkrith.com instead.")
			return
		}
		if !throttle.allow(contactAddress(r), limits.perAddress, time.Now()) {
			problem(w, 429, "You've sent several notes already. Please try again later, or write to hello@getkrith.com.")
			return
		}
		var recent int
		if err := db.QueryRowContext(r.Context(), `SELECT count(*) FROM contact_notes WHERE created_at > now() - interval '1 hour'`).Scan(&recent); err != nil {
			problem(w, 503, "Notes can't be received right now. Write to hello@getkrith.com instead.")
			return
		}
		if recent >= limits.perHour {
			problem(w, 429, "We're receiving a lot of notes just now. Please try again later, or write to hello@getkrith.com.")
			return
		}
		var id int64
		if err := db.QueryRowContext(r.Context(), `INSERT INTO contact_notes(name,email,firm,note) VALUES($1,$2,$3,$4) RETURNING id`, name, email, firm, note).Scan(&id); err != nil {
			problem(w, 503, "Notes can't be received right now. Write to hello@getkrith.com instead.")
			return
		}
		jsonReply(w, 201, map[string]any{"forward": true, "id": id, "name": name, "email": email, "firm": firm, "note": note})
	}
}
