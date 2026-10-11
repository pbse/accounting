package transport

import (
	"context"
	"database/sql"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"close.local/backend/internal/preparation"
)

// A client's confirmed profile (docs/books-sources.md, Client profile): the currency and fiscal-year start its books are
// kept in, independent of any connection, effective from a month. A member who reviews confirms it for a client they
// may see whole; each change is a new row with its reason, so earlier months keep the profile they were closed under.
// Currency checks, fiscal-year calculations, statement confirmation and the year-end handoff read it through
// preparation.ClientProfile; where a connection reports otherwise, the difference is a question, not an override.

// confirmedProfile is one row of a client's profile history.
type confirmedProfile struct {
	ID            string    `json:"id"`
	EffectiveFrom string    `json:"effective_from"`
	Currency      string    `json:"currency"`
	FiscalStart   int       `json:"fiscal_year_start"`
	Reason        string    `json:"reason"`
	Actor         string    `json:"actor"`
	At            time.Time `json:"at"`
}

// profileHistory is the client's confirmed profiles, newest first, under any of its names.
func profileHistory(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, org, name string) ([]confirmedProfile, error) {
	rows, err := q.QueryContext(ctx, `SELECT id::text,effective_from,currency,fiscal_year_start,reason,actor,created_at FROM client_profiles
 WHERE organization_id=$1 AND client_identity(organization_id,client_entity)=client_identity($1,$2) ORDER BY id DESC LIMIT 50`, org, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	history := []confirmedProfile{}
	for rows.Next() {
		var p confirmedProfile
		if err = rows.Scan(&p.ID, &p.EffectiveFrom, &p.Currency, &p.FiscalStart, &p.Reason, &p.Actor, &p.At); err != nil {
			return nil, err
		}
		history = append(history, p)
	}
	return history, rows.Err()
}

var currencyCode = regexp.MustCompile(`^[A-Z]{3}$`)

func registerClientProfiles(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("POST /v1/closes/client/profile", func(w http.ResponseWriter, r *http.Request) {
		if !canReview(r) {
			problem(w, 403, "Confirming a client's profile requires workpapers:review.")
			return
		}
		var in struct {
			Client        string `json:"client"`
			Currency      string `json:"currency"`
			FiscalStart   int    `json:"fiscal_year_start"`
			EffectiveFrom string `json:"effective_from"`
			Reason        string `json:"reason"`
			Rev           string `json:"rev"` // the newest profile as read, "" for none
		}
		if !decode(w, r, &in) {
			return
		}
		in.Client, in.Reason, in.Currency = strings.TrimSpace(in.Client), strings.TrimSpace(in.Reason), strings.ToUpper(strings.TrimSpace(in.Currency))
		if in.Client == "" || utf8.RuneCountInString(in.Client) > 200 {
			problem(w, 422, "Name the client.")
			return
		}
		if !currencyCode.MatchString(in.Currency) {
			problem(w, 422, "Enter the currency as its three-letter code, such as USD.")
			return
		}
		if in.FiscalStart < 1 || in.FiscalStart > 12 {
			problem(w, 422, "Choose the month the fiscal year starts.")
			return
		}
		if from, err := time.Parse("2006-01", in.EffectiveFrom); err != nil || from.Year() < 1990 || from.After(time.Now().UTC().AddDate(1, 0, 0)) {
			problem(w, 422, "Enter the first month the profile applies to as YYYY-MM, no more than a year ahead.")
			return
		}
		if in.Reason == "" || utf8.RuneCountInString(in.Reason) > 500 {
			problem(w, 422, "Say why (1–500 characters).")
			return
		}
		ctx := r.Context()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			problem(w, 503, "The client's profile is unavailable.")
			return
		}
		defer tx.Rollback()
		org := organization(r)
		name, known, allowed, whole, err := clientNamed(ctx, tx, org, in.Client, firmAccess(r), subject(r))
		if err != nil {
			problem(w, 503, "The client's profile is unavailable.")
			return
		}
		if !known || !allowed {
			problem(w, 404, "Client not found.")
			return
		}
		if !whole {
			problem(w, 403, "A client's profile is confirmed under its own name; ask for access to "+name+".")
			return
		}
		// One change at a time: the client's profiles are locked, and the newest must be the one the person read.
		if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('client_profiles:'||$1||':'||client_identity($1,$2)))`, org, name); err != nil {
			problem(w, 503, "The client's profile is unavailable.")
			return
		}
		var newest sql.NullInt64
		if err = tx.QueryRowContext(ctx, `SELECT max(id) FROM client_profiles WHERE organization_id=$1 AND client_identity(organization_id,client_entity)=client_identity($1,$2)`, org, name).Scan(&newest); err != nil {
			problem(w, 503, "The client's profile is unavailable.")
			return
		}
		read := ""
		if newest.Valid {
			read = strconv.FormatInt(newest.Int64, 10)
		}
		if in.Rev != read {
			problem(w, 409, "The client's profile changed since you read it. Review it and try again.")
			return
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO client_profiles(organization_id,client_entity,effective_from,currency,fiscal_year_start,reason,actor) VALUES($1,$2,$3,$4,$5,$6,$7)`,
			org, name, in.EffectiveFrom, in.Currency, in.FiscalStart, in.Reason, reviewerName(r)); err != nil {
			problem(w, 503, "The client's profile could not be saved.")
			return
		}
		profile, err := preparation.ClientProfileOf(ctx, tx, org, name, time.Now().UTC().Format("2006-01"))
		if err != nil {
			problem(w, 503, "The client's profile could not be saved.")
			return
		}
		history, err := profileHistory(ctx, tx, org, name)
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "The client's profile could not be saved.")
			return
		}
		jsonReply(w, 200, map[string]any{"profile": profile, "profiles": history})
	})
}
