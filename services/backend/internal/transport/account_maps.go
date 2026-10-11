package transport

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"close.local/backend/internal/bookfiles"
)

// Account maps (docs/books-sources.md, How an upload becomes the month's books, step 4; delivery step 6): each account a
// file's confirmed reading names needs a class and a type, as a QuickBooks capture's account directory gives them. The
// reader proposes them from the file's type column, the heading the account sits under, its name and its number; a
// reviewer confirms. The map is the client's, versioned (books_account_maps), and next month's file finds its accounts
// mapped already, by number, else by name.

// mappedAccount is an account as the map holds it, with the files whose readings it was confirmed on: a file deleted
// takes with it the accounts only it named (books_forget_file_accounts).
type mappedAccount struct {
	Class  string   `json:"class"`
	Type   string   `json:"type"`
	Name   string   `json:"name"`
	Number string   `json:"number,omitempty"`
	Files  []string `json:"files,omitempty"`
}

// sameMapping says whether two entries map an account alike, whichever files they were confirmed on.
func sameMapping(a, b mappedAccount) bool {
	return a.Class == b.Class && a.Type == b.Type && a.Name == b.Name && a.Number == b.Number
}

// addAmounts is two exact amounts' sum, written to the cent when it is exact to the cent.
func addAmounts(a, b string) string {
	x, okA := new(big.Rat).SetString(cmp.Or(a, "0"))
	y, okB := new(big.Rat).SetString(cmp.Or(b, "0"))
	if !okA || !okB {
		return a
	}
	sum := x.Add(x, y)
	if new(big.Rat).Mul(sum, big.NewRat(100, 1)).IsInt() {
		return sum.FloatString(2)
	}
	return exactDecimal(sum)
}

// accountMap is a version of a client's map.
type accountMap struct {
	ID       string                   `json:"id"`
	Period   string                   `json:"period"`
	Reason   string                   `json:"reason"`
	Actor    string                   `json:"actor"`
	At       time.Time                `json:"at"`
	Accounts map[string]mappedAccount `json:"-"`
}

// fileAccount is an account a confirmed reading names: where it was read, the class and type proposed for it, and what
// the client's map holds for it.
type fileAccount struct {
	Key      string             `json:"key"`
	Number   string             `json:"number,omitempty"`
	Name     string             `json:"name"`
	Balance  string             `json:"balance"`
	Ref      string             `json:"ref"`
	Proposal bookfiles.Proposal `json:"proposal"`
	Mapped   *mappedAccount     `json:"mapped,omitempty"`
}

// latestAccountMap is client name's map in force, or nil.
func latestAccountMap(ctx context.Context, q rowReader, org, name string) (*accountMap, error) {
	var m accountMap
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT id::text,period,reason,actor,created_at,accounts FROM books_account_maps
 WHERE organization_id=$1 AND client_identity(organization_id,client_entity)=client_identity($1,$2) ORDER BY id DESC LIMIT 1`, org, name).
		Scan(&m.ID, &m.Period, &m.Reason, &m.Actor, &m.At, &raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, json.Unmarshal(raw, &m.Accounts)
}

// readingAccounts is import id's month and the accounts its confirmed reading names, once each, in the order read, an
// account on several rows (one per department, say) with their balances summed, each with its proposal and with what
// the map holds for it; false when its layout is not confirmed yet.
func readingAccounts(ctx context.Context, q rowReader, org, name, id string, current *accountMap) (string, []fileAccount, bool, error) {
	var period string
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT i.period,COALESCE(l.reading->'lines','[]'::jsonb) FROM books_layouts l JOIN books_imports i ON i.id=l.import_id
 WHERE l.organization_id=$1 AND client_identity(l.organization_id,l.client_entity)=client_identity($1,$2) AND l.import_id::text=$3 ORDER BY l.id DESC LIMIT 1`, org, name, id).Scan(&period, &raw)
	if err == sql.ErrNoRows {
		return "", nil, false, nil
	}
	if err != nil {
		return "", nil, false, err
	}
	var lines []bookfiles.Line
	if err = json.Unmarshal(raw, &lines); err != nil {
		return "", nil, false, err
	}
	accounts := []fileAccount{}
	seen := map[string]int{}
	heading := ""
	for _, l := range lines {
		switch l.Kind {
		case bookfiles.Heading:
			heading = l.Name
		case bookfiles.Posting:
			key := bookfiles.AccountKey(l.Number, l.Name)
			if at, ok := seen[key]; ok {
				accounts[at].Balance = addAmounts(accounts[at].Balance, l.Amount)
				continue
			}
			seen[key] = len(accounts)
			a := fileAccount{Key: key, Number: l.Number, Name: l.Name, Balance: l.Amount, Ref: l.Ref, Proposal: bookfiles.Propose(l.Type, heading, l.Name, l.Number)}
			if current != nil {
				if m, ok := current.Accounts[key]; ok {
					a.Mapped = &m
				}
			}
			accounts = append(accounts, a)
		}
	}
	return period, accounts, true, nil
}

func registerAccountMaps(mux *http.ServeMux, db *sql.DB) {
	// The accounts of a file's confirmed reading, two hundred from ?offset=, with what each is proposed as and mapped to.
	mux.HandleFunc("GET /v1/closes/client/import/accounts", func(w http.ResponseWriter, r *http.Request) {
		name, ok := importOwner(w, r, db)
		if !ok {
			return
		}
		offset, err := strconv.Atoi(cmp.Or(r.URL.Query().Get("offset"), "0"))
		if err != nil || offset < 0 {
			problem(w, 422, "Say where the accounts continue from, as a number.")
			return
		}
		org, id := organization(r), r.URL.Query().Get("id")
		if _, err = importFormat(r.Context(), db, org, name, id); errors.Is(err, sql.ErrNoRows) {
			problem(w, 404, "File not found.")
			return
		} else if err != nil {
			problem(w, 503, "The file is unavailable.")
			return
		}
		current, err := latestAccountMap(r.Context(), db, org, name)
		var accounts []fileAccount
		var found bool
		if err == nil {
			_, accounts, found, err = readingAccounts(r.Context(), db, org, name, id, current)
		}
		on, onErr := fileImportsOn(r.Context(), db, org)
		if err != nil || onErr != nil {
			problem(w, 503, "The accounts are unavailable.")
			return
		}
		if !found {
			problem(w, 409, "Confirm how the file is read before mapping its accounts.")
			return
		}
		unmapped := 0
		for _, a := range accounts {
			if a.Mapped == nil {
				unmapped++
			}
		}
		from := min(offset, len(accounts))
		out := map[string]any{"accounts": accounts[from:min(from+200, len(accounts))], "count": len(accounts), "unmapped": unmapped, "more": len(accounts) > from+200,
			"classes": bookfiles.Types, "on": on, "can_confirm": canReview(r) && on, "map": nil}
		if current != nil {
			out["map"] = current
		}
		jsonReply(w, 200, out)
	})
	// A reviewer confirms the map for a file's accounts: the classes and types they set, and, with accept, what is
	// proposed for every account not mapped yet. The client's map gets a new version holding all of it.
	mux.HandleFunc("POST /v1/closes/client/import/accounts", func(w http.ResponseWriter, r *http.Request) {
		name, ok := importClient(w, r, db)
		if !ok {
			return
		}
		if !canReview(r) {
			problem(w, 403, "Confirming an account map requires workpapers:review.")
			return
		}
		var in struct {
			Accounts map[string]struct {
				Class string `json:"class"`
				Type  string `json:"type"`
			} `json:"accounts"`
			Accept bool   `json:"accept"`
			Reason string `json:"reason"`
			// BasedOn is the version of the map the reviewer saw ("" for none): one confirmed since is never overwritten
			// by choices made before it.
			BasedOn string `json:"based_on"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Reason = strings.TrimSpace(in.Reason); in.Reason == "" || utf8.RuneCountInString(in.Reason) > 500 {
			problem(w, 422, "Say why (1–500 characters).")
			return
		}
		for _, a := range in.Accounts {
			if !bookfiles.ValidType(a.Class, a.Type) {
				problem(w, 422, "Choose each account's class, and a type of that class.")
				return
			}
		}
		org, id := organization(r), r.URL.Query().Get("id")
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "The account map is unavailable.")
			return
		}
		defer tx.Rollback()
		// One confirmation at a time for a client, so none is built on a version another replaced meanwhile.
		if _, err = tx.ExecContext(r.Context(), `SELECT pg_advisory_xact_lock(hashtext('books_account_maps:'||$1||':'||client_identity($1,$2)))`, org, name); err != nil {
			problem(w, 503, "The account map is unavailable.")
			return
		}
		current, err := latestAccountMap(r.Context(), tx, org, name)
		if err == nil && (current == nil && in.BasedOn != "" || current != nil && current.ID != in.BasedOn) {
			problem(w, 409, "The map was confirmed again since you opened it: reopen it to see what changed.")
			return
		}
		var period string
		var accounts []fileAccount
		var found bool
		if err == nil {
			period, accounts, found, err = readingAccounts(r.Context(), tx, org, name, id, current)
		}
		if err != nil {
			problem(w, 503, "The account map is unavailable.")
			return
		}
		if !found {
			problem(w, 409, "Confirm how the file is read before mapping its accounts.")
			return
		}
		next := map[string]mappedAccount{}
		if current != nil {
			maps.Copy(next, current.Accounts)
		}
		named := map[string]bool{}
		changed := false
		for _, a := range accounts {
			named[a.Key] = true
			m, set := mappedAccount{Name: a.Name, Number: a.Number}, false
			if asked, ok := in.Accounts[a.Key]; ok {
				m.Class, m.Type, set = asked.Class, asked.Type, true
			} else if in.Accept && a.Mapped == nil && a.Proposal.Class != "" {
				m.Class, m.Type, set = a.Proposal.Class, a.Proposal.Type, true
			}
			if set && (a.Mapped == nil || !sameMapping(*a.Mapped, m)) {
				next[a.Key], changed = m, true
			}
		}
		// Every account this file names that the map holds was confirmed on it: the files it came from are its own.
		for _, a := range accounts {
			if m, ok := next[a.Key]; ok && !slices.Contains(m.Files, id) {
				m.Files = append(slices.Clone(m.Files), id)
				next[a.Key] = m
			}
		}
		for key := range in.Accounts {
			if !named[key] {
				problem(w, 422, "Map only the accounts the file names.")
				return
			}
		}
		if !changed {
			problem(w, 422, "Nothing to confirm: every account is mapped as it was.")
			return
		}
		raw, _ := json.Marshal(next)
		var version string
		if err = tx.QueryRowContext(r.Context(), `INSERT INTO books_account_maps(organization_id,client_entity,import_id,period,accounts,reason,actor) VALUES($1,$2,$3::bigint,$4,$5::jsonb,$6,$7) RETURNING id::text`,
			org, name, id, period, string(raw), in.Reason, reviewerName(r)).Scan(&version); err != nil {
			problem(w, 503, "The account map could not be saved.")
			return
		}
		if err = tx.Commit(); err != nil {
			problem(w, 503, "The account map could not be saved.")
			return
		}
		unmapped := 0
		for _, a := range accounts {
			if _, ok := next[a.Key]; !ok {
				unmapped++
			}
		}
		jsonReply(w, 200, map[string]any{"id": version, "unmapped": unmapped})
	})
}
