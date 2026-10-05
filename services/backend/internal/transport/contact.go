package transport

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
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
	// The page's key for this note, the same on a retry.
	Submission string `json:"submission"`
}

type contactLimits struct {
	perAddress, perHour int
}

// Five kept notes an hour from one address, sixty an hour from everyone: enough for people, little for a script.
var defaultContactLimits = contactLimits{perAddress: 5, perHour: 60}

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
		if in.Submission != "" && !contactSubmission.MatchString(in.Submission) {
			okNote = false
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
		id, kept, err := keepContactNote(r.Context(), db, limits, contactSender(contactAddress(r)), in.Submission, name, email, firm, note)
		switch {
		case errors.Is(err, errContactAddressBusy):
			problem(w, 429, "You've sent several notes already. Please try again later, or write to hello@getkrith.com.")
			return
		case errors.Is(err, errContactKeyReused):
			problem(w, 409, "That note was already sent with other words. Reload the page to send this one.")
			return
		case errors.Is(err, errContactBusy):
			problem(w, 429, "We're receiving a lot of notes just now. Please try again later, or write to hello@getkrith.com.")
			return
		case err != nil:
			problem(w, 503, "Notes can't be received right now. Write to hello@getkrith.com instead.")
			return
		case !kept:
			// A retry of a note already kept, and already forwarded.
			jsonReply(w, 201, map[string]any{"forward": false, "id": id})
			return
		}
		jsonReply(w, 201, map[string]any{"forward": true, "id": id, "name": name, "email": email, "firm": firm, "note": note})
	}
}

var (
	errContactBusy        = errors.New("contact notes: the hourly limit is reached")
	errContactAddressBusy = errors.New("contact notes: this address's hourly limit is reached")
	errContactKeyReused   = errors.New("contact notes: a kept submission sent with other words")
	contactSubmission     = regexp.MustCompile(`^[A-Za-z0-9-]{16,64}$`)
)

// contactSender is a one-way digest of the address a note came from; the address itself is never stored.
func contactSender(address string) string {
	sum := sha256.Sum256([]byte("krith contact sender\x00" + address))
	return hex.EncodeToString(sum[:])
}

// keepContactNote stores a note, or finds the one a retry of the same submission already stored (kept is then false); the
// same key with other words is refused rather than answered with the earlier note.
// It refuses a note once its sender, or everyone together, reached the hourly limit. Notes are counted and kept one at a
// time, so a burst cannot pass the limits together, and only kept notes count. Digests over an hour old are cleared.
func keepContactNote(ctx context.Context, db *sql.DB, limits contactLimits, sender, submission, name, email, firm, note string) (id int64, kept bool, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('contact_notes'))`); err != nil {
		return 0, false, err
	}
	if submission != "" {
		var same bool
		switch err = tx.QueryRowContext(ctx, `SELECT id,(name,email,firm,note)=($2,$3,$4,$5) FROM contact_notes WHERE submission=$1`, submission, name, email, firm, note).Scan(&id, &same); {
		case err == nil && !same:
			return 0, false, errContactKeyReused
		case err == nil:
			return id, false, nil
		case !errors.Is(err, sql.ErrNoRows):
			return 0, false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE contact_notes SET sender_digest=NULL WHERE sender_digest IS NOT NULL AND created_at <= now() - interval '1 hour'`); err != nil {
		return 0, false, err
	}
	var fromSender, everyone int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE sender_digest=$1), count(*) FROM contact_notes WHERE created_at > now() - interval '1 hour'`, sender).Scan(&fromSender, &everyone); err != nil {
		return 0, false, err
	}
	if fromSender >= limits.perAddress {
		return 0, false, errContactAddressBusy
	}
	if everyone >= limits.perHour {
		return 0, false, errContactBusy
	}
	if err = tx.QueryRowContext(ctx, `INSERT INTO contact_notes(name,email,firm,note,submission,sender_digest) VALUES($1,$2,$3,$4,NULLIF($5,''),$6) RETURNING id`, name, email, firm, note, submission, sender).Scan(&id); err != nil {
		return 0, false, err
	}
	return id, true, tx.Commit()
}
