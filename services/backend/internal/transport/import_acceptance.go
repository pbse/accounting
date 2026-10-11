package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"close.local/backend/internal/bookfiles"
	"close.local/backend/internal/preparation"
)

// Checks and acceptance (docs/books-sources.md, How an upload becomes the month's books, steps 6 and 7, and Checks): the
// checks run on a file's confirmed reading, each passing, holding the results that depend on it, or noting; none
// refuses. A member with review authority accepts the reading as the client's books for its month, from a named
// system, exported on a named date, with its attested scope and basis and whether the final adjustments are in; what
// the checks found is kept with the acceptance. Holding applies once a file can be a month's books (step 9).

// importCheck is one check of a file's reading: whether it passes, holds what depends on it, or is a note.
type importCheck struct {
	Key    string   `json:"key"`
	Title  string   `json:"title"`
	State  string   `json:"state"`
	Detail string   `json:"detail,omitempty"`
	Holds  string   `json:"holds,omitempty"`
	Items  []string `json:"items,omitempty"`
}

// fileAcceptance is a reviewer's acceptance of a file's reading, current while the reading and attestation it was given on
// are the file's latest.
type fileAcceptance struct {
	ID               string        `json:"id"`
	Period           string        `json:"period"`
	System           string        `json:"system"`
	ExportedOn       string        `json:"exported_on"`
	FinalAdjustments bool          `json:"final_adjustments"`
	Explained        string        `json:"explained,omitempty"`
	Checks           []importCheck `json:"checks"`
	Reason           string        `json:"reason"`
	Actor            string        `json:"actor"`
	At               time.Time     `json:"at"`
	Current          bool          `json:"current"`
}

// fileState is what a file's checks read: its confirmed reading and layout, its latest attestation (nil unless of this
// reading), the client's approved basis and currency for the month, the map as it stands, and the accounts with a
// balance in the prior month's accepted books that this reading does not name.
type fileState struct {
	reading  fileReading
	layout   *confirmedRead
	brief    *confirmedRead
	scope    *importScope
	approved string
	currency string
	mapStamp string
	recorded bool
	prior    string
	missing  []string
}

// previousMonth is the month before period (YYYY-MM).
func previousMonth(period string) string {
	t, err := time.Parse("2006-01", period)
	if err != nil {
		return ""
	}
	return t.AddDate(0, -1, 0).Format("2006-01")
}

// monthEndDay is period's last day (YYYY-MM-DD).
func monthEndDay(period string) string {
	return monthEnd(period).Format("2006-01-02")
}

// readFileState reads what import id's checks need; false when its layout is not confirmed yet.
func readFileState(ctx context.Context, q rowReader, org, name, id string) (fileState, bool, error) {
	var s fileState
	current, err := latestAccountMap(ctx, q, org, name)
	if err != nil {
		return s, false, err
	}
	if current != nil {
		s.mapStamp = current.Stamp
	}
	reading, found, err := readingAccounts(ctx, q, org, name, id, current)
	if err != nil || !found {
		return s, found, err
	}
	s.reading = reading
	if s.layout, err = latestLayout(ctx, q, org, name, id); err != nil {
		return s, false, err
	}
	if s.brief, err = confirmedBrief(ctx, q, org, name, id); err != nil {
		return s, false, err
	}
	if s.layout == nil || s.brief == nil || s.layout.ID != reading.ID || s.brief.ID != reading.ID {
		return s, false, fmt.Errorf("the file's layout changed while it was read")
	}
	scope, err := latestImportScope(ctx, q, org, name, id)
	if err != nil {
		return s, false, err
	}
	if scope != nil && scope.LayoutID == reading.ID {
		s.scope = scope
	}
	if s.approved, _, err = approvedBasis(ctx, q, org, name); err != nil {
		return s, false, err
	}
	profile, err := preparation.ClientProfileOf(ctx, q, org, name, reading.Period)
	if err != nil {
		return s, false, err
	}
	s.currency = profile.Currency
	s.recorded = true
	for _, a := range reading.Accounts {
		if a.Mapped == nil || !slices.Contains(a.Mapped.Readings, reading.ID) {
			s.recorded = false
		}
	}
	// The prior month's accepted books, if any are current: every account with a balance there is in this reading, or
	// is explained.
	s.prior = previousMonth(reading.Period)
	var priorLines []byte
	err = q.QueryRowContext(ctx, `SELECT COALESCE(l.reading->'lines','[]'::jsonb) FROM books_import_acceptances a JOIN books_layouts l ON l.id=a.layout_id
 WHERE a.organization_id=$1 AND client_identity(a.organization_id,a.client_entity)=client_identity($1,$2) AND a.period=$3
  AND a.layout_id=(SELECT max(x.id) FROM books_layouts x WHERE x.import_id=a.import_id) AND a.scope_id=(SELECT max(y.id) FROM books_import_scopes y WHERE y.import_id=a.import_id)
 ORDER BY a.id DESC LIMIT 1`, org, name, s.prior).Scan(&priorLines)
	if err == sql.ErrNoRows {
		s.prior = ""
		return s, true, nil
	}
	if err != nil {
		return s, false, err
	}
	var lines []bookfiles.Line
	if err = json.Unmarshal(priorLines, &lines); err != nil {
		return s, false, err
	}
	here := map[string]bool{}
	for _, a := range reading.Accounts {
		here[a.Key] = true
	}
	// An account on several rows there (one per department, say) had the balance of their sum.
	var order []string
	balances, names := map[string]string{}, map[string]string{}
	for _, l := range lines {
		if l.Kind != bookfiles.Posting || strings.TrimSpace(l.Number) == "" && strings.TrimSpace(l.Name) == "" {
			continue
		}
		key := bookfiles.AccountKey(l.Number, l.Name)
		if _, ok := balances[key]; !ok {
			order = append(order, key)
			balances[key], names[key] = "0", strings.TrimSpace(l.Number+" "+l.Name)
		}
		balances[key] = addAmounts(balances[key], l.Amount)
	}
	for _, key := range order {
		if !here[key] && nonZero(balances[key]) {
			s.missing = append(s.missing, names[key])
		}
	}
	return s, true, nil
}

// nonZero says whether an exact amount is not zero.
func nonZero(amount string) bool {
	return strings.Trim(strings.TrimPrefix(amount, "-"), "0.") != ""
}

// fileChecks are the checks of a file's state; explained, when given, is the reviewer's account of the prior month's
// accounts the reading does not name, which makes that a note.
func fileChecks(s fileState, explained string) []importCheck {
	var checks []importCheck
	add := func(c importCheck) { checks = append(checks, c) }
	period := s.reading.Period
	// Scope: attested complete, and every account with a balance in the prior accepted month present or explained.
	switch {
	case s.scope == nil:
		add(importCheck{Key: "scope", Title: "The scope is attested", State: "holds", Detail: "Attest what the file covers and its basis, for the reading confirmed now.",
			Holds: "Every statement, total and check, until what the file covers is attested."})
	case !s.scope.Complete:
		add(importCheck{Key: "scope", Title: "The scope is complete", State: "holds", Detail: "It leaves out " + s.scope.Missing + ".",
			Holds: "Every statement, total and check that includes what is missing, until the rest arrives. A report on a narrower population names that population on its face."})
	case len(s.missing) > 0 && strings.TrimSpace(explained) != "":
		add(importCheck{Key: "scope", Title: "The scope is complete", State: "note", Detail: fmt.Sprintf("%d account%s with a balance in %s's accepted books %s not in this file, explained: %s", len(s.missing), suffix(len(s.missing)), monthTitle(s.prior), isAre(len(s.missing)), strings.TrimSpace(explained)), Items: s.missing})
	case len(s.missing) > 0:
		add(importCheck{Key: "scope", Title: "The scope is complete", State: "holds", Detail: fmt.Sprintf("%d account%s with a balance in %s's accepted books %s not in this file: explain %s when accepting, or send the rest.", len(s.missing), suffix(len(s.missing)), monthTitle(s.prior), isAre(len(s.missing)), itThem(len(s.missing))),
			Holds: "Every statement, total and check that includes those accounts, until they are explained or arrive.", Items: s.missing})
	case s.prior != "":
		add(importCheck{Key: "scope", Title: "The scope is complete", State: "passes", Detail: "The client's complete books for the month, as attested, with every account in " + monthTitle(s.prior) + "'s accepted books."})
	default:
		add(importCheck{Key: "scope", Title: "The scope is complete", State: "passes", Detail: "The client's complete books for the month, as attested. No earlier month is accepted to compare with."})
	}
	if s.brief.Reading.Balanced {
		add(importCheck{Key: "balanced", Title: "Debits equal credits", State: "passes", Detail: "Debits " + s.brief.Reading.Debits + ", credits " + s.brief.Reading.Credits + ", after totals are excluded."})
	} else {
		add(importCheck{Key: "balanced", Title: "Debits equal credits", State: "holds", Detail: "Debits " + s.brief.Reading.Debits + ", credits " + s.brief.Reading.Credits + ".", Holds: "Everything that reads balances."})
	}
	if end := monthEndDay(period); s.layout.Layout.AsOf == end {
		add(importCheck{Key: "as_of", Title: "As of the month-end", State: "passes", Detail: "As of " + end + "."})
	} else {
		add(importCheck{Key: "as_of", Title: "As of the month-end", State: "holds", Detail: "The file is as of " + s.layout.Layout.AsOf + "; " + monthTitle(period) + " ends " + end + ".", Holds: "Its use as the month's books. It can still be kept as history."})
	}
	if len(s.brief.Reading.Problems) == 0 {
		add(importCheck{Key: "totals", Title: "Totals equal their children", State: "passes", Detail: "Every total equals the rows it covers, and every amount reads."})
	} else {
		add(importCheck{Key: "totals", Title: "Totals equal their children", State: "holds", Detail: "The reading has problems to resolve.", Holds: "The import, until its rows are read and its hierarchy confirmed.", Items: s.brief.Reading.Problems})
	}
	unmapped := 0
	for _, a := range s.reading.Accounts {
		if a.Mapped == nil {
			unmapped++
		}
	}
	switch {
	case unmapped == 0 && s.reading.Unidentified == 0 && s.recorded:
		add(importCheck{Key: "classes", Title: "Every account has a class", State: "passes", Detail: fmt.Sprintf("All %d account%s mapped, on this reading.", len(s.reading.Accounts), suffix(len(s.reading.Accounts)))})
	default:
		var said []string
		if unmapped > 0 {
			said = append(said, fmt.Sprintf("%d account%s to map", unmapped, suffix(unmapped)))
		}
		if s.reading.Unidentified > 0 {
			said = append(said, fmt.Sprintf("%d row%s naming no account", s.reading.Unidentified, suffix(s.reading.Unidentified)))
		}
		if unmapped == 0 && !s.recorded {
			said = append(said, "the map not yet confirmed on this reading")
		}
		add(importCheck{Key: "classes", Title: "Every account has a class", State: "holds", Detail: strings.Join(said, "; ") + ".", Holds: "The statements and coverage."})
	}
	cents := true
	for _, a := range s.reading.Accounts {
		if !a.Cents {
			cents = false
		}
	}
	switch got := strings.ToUpper(strings.TrimSpace(s.layout.Layout.Currency)); {
	case got != s.currency:
		add(importCheck{Key: "currency", Title: "In the client's currency, to the cent", State: "holds", Detail: "The file is in " + got + "; the client's currency for " + monthTitle(period) + " is " + s.currency + ".", Holds: "Its use as the month's books."})
	case !cents:
		add(importCheck{Key: "currency", Title: "In the client's currency, to the cent", State: "holds", Detail: "Some amounts are not exact to the cent: the file may be rounded.", Holds: "Its use as the month's books."})
	default:
		add(importCheck{Key: "currency", Title: "In the client's currency, to the cent", State: "passes", Detail: "In " + got + ", every amount exact to the cent."})
	}
	// Formula amounts are the last calculation, which the reader does not repeat: a values-only export has none, and a
	// reviewer's attestation of a recalculated copy releases them.
	var formulas []string
	for _, a := range s.reading.Accounts {
		if a.Formula {
			formulas = append(formulas, strings.TrimSpace(a.Number+" "+a.Name))
		}
	}
	switch {
	case len(formulas) == 0:
		add(importCheck{Key: "formulas", Title: "Amounts are stored values", State: "passes", Detail: "No month-end amount is a formula's result."})
	case s.scope != nil && s.scope.Recalculated:
		add(importCheck{Key: "formulas", Title: "Amounts are stored values", State: "note", Detail: fmt.Sprintf("%d account%s' amounts are formula results, released by the reviewer's attestation that the file is a recalculated copy.", len(formulas), suffix(len(formulas))), Items: formulas})
	default:
		add(importCheck{Key: "formulas", Title: "Amounts are stored values", State: "holds", Detail: fmt.Sprintf("%d account%s' amounts are formula results, the workbook's last calculation, which the reader does not repeat.", len(formulas), suffix(len(formulas))),
			Holds: "Its use as the month's books, until a values-only export from the ledger replaces it, or a reviewer attests, with its scope, that it is a copy recalculated after every linked workbook was refreshed.", Items: formulas})
	}
	switch {
	case s.scope == nil:
		add(importCheck{Key: "basis", Title: "On the client's approved basis", State: "holds", Detail: "Attest the basis the export is on.", Holds: "Comparisons, roll-forwards and the statements built on the file."})
	case s.approved == "":
		add(importCheck{Key: "basis", Title: "On the client's approved basis", State: "note", Detail: "The file is " + onBasis(s.scope.Basis) + "; no basis is approved for the client yet."})
	case s.scope.Basis != s.approved:
		add(importCheck{Key: "basis", Title: "On the client's approved basis", State: "holds", Detail: "The file is " + onBasis(s.scope.Basis) + "; the client's approved basis is " + basisLabels[s.approved] + ".",
			Holds: "Comparisons, derived activity and roll-forwards across the difference, the treatments the approved basis does not support, and the statements, sign-off and publication built on the file."})
	default:
		add(importCheck{Key: "basis", Title: "On the client's approved basis", State: "passes", Detail: strings.ToUpper(onBasis(s.approved)[:1]) + onBasis(s.approved)[1:] + ", as approved."})
	}
	return checks
}

// suffix is "s" for any count but one.
func suffix(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

func itThem(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

// monthTitle is a month as a person reads it: "September 2026".
func monthTitle(period string) string {
	t, err := time.Parse("2006-01", period)
	if err != nil {
		return period
	}
	return t.Format("January 2006")
}

// explainedFor is a current acceptance's explanation, while it covers every account now missing since the prior
// month's accepted books: the accounts its scope check listed when it was given. An account those books add later is
// not covered, and holds again.
func explainedFor(a *fileAcceptance, missing []string) string {
	if a == nil || !a.Current || a.Explained == "" {
		return ""
	}
	for _, c := range a.Checks {
		if c.Key != "scope" {
			continue
		}
		for _, m := range missing {
			if !slices.Contains(c.Items, m) {
				return ""
			}
		}
		return a.Explained
	}
	return ""
}

// holding counts the checks that hold.
func holding(checks []importCheck) int {
	n := 0
	for _, c := range checks {
		if c.State == "holds" {
			n++
		}
	}
	return n
}

// latestAcceptance is import id's latest acceptance, or nil, and whether it is current: given on the file's latest
// reading and attestation.
func latestAcceptance(ctx context.Context, q rowReader, org, name, id string) (*fileAcceptance, error) {
	var a fileAcceptance
	var exported time.Time
	var checks []byte
	err := q.QueryRowContext(ctx, `SELECT a.id::text,a.period,a.system,a.exported_on,a.final_adjustments,a.explained,a.checks,a.reason,a.actor,a.created_at,
 a.layout_id=(SELECT max(x.id) FROM books_layouts x WHERE x.import_id=a.import_id) AND a.scope_id=(SELECT max(y.id) FROM books_import_scopes y WHERE y.import_id=a.import_id)
 FROM books_import_acceptances a WHERE a.organization_id=$1 AND client_identity(a.organization_id,a.client_entity)=client_identity($1,$2) AND a.import_id::text=$3 ORDER BY a.id DESC LIMIT 1`, org, name, id).
		Scan(&a.ID, &a.Period, &a.System, &exported, &a.FinalAdjustments, &a.Explained, &checks, &a.Reason, &a.Actor, &a.At, &a.Current)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a.ExportedOn = exported.Format("2006-01-02")
	return &a, json.Unmarshal(checks, &a.Checks)
}

func registerImportAcceptance(mux *http.ServeMux, db *sql.DB) {
	// A file's checks as they stand, and its latest acceptance.
	mux.HandleFunc("GET /v1/closes/client/import/acceptance", func(w http.ResponseWriter, r *http.Request) {
		name, ok := importOwner(w, r, db)
		if !ok {
			return
		}
		org, id := organization(r), r.URL.Query().Get("id")
		if _, err := importFormat(r.Context(), db, org, name, id); err == sql.ErrNoRows {
			problem(w, 404, "File not found.")
			return
		} else if err != nil {
			problem(w, 503, "The file is unavailable.")
			return
		}
		state, found, err := readFileState(r.Context(), db, org, name, id)
		var accepted *fileAcceptance
		if err == nil {
			accepted, err = latestAcceptance(r.Context(), db, org, name, id)
		}
		on, onErr := fileImportsOn(r.Context(), db, org)
		if err != nil || onErr != nil {
			problem(w, 503, "The file's checks are unavailable.")
			return
		}
		out := map[string]any{"confirmed": found, "acceptance": accepted, "on": on, "can_accept": false, "checks": []importCheck{}}
		if found {
			checks := fileChecks(state, explainedFor(accepted, state.missing))
			out["checks"], out["holds"], out["reading"], out["map_stamp"] = checks, holding(checks), state.reading.ID, state.mapStamp
			out["period"], out["can_accept"] = state.reading.Period, canReview(r) && on && state.scope != nil
			if state.scope != nil {
				out["scope"] = state.scope.ID
			}
		}
		jsonReply(w, 200, out)
	})
	// A reviewer accepts a file's reading as the client's books for its month, on the reading, attestation and map they
	// saw.
	mux.HandleFunc("POST /v1/closes/client/import/acceptance", func(w http.ResponseWriter, r *http.Request) {
		name, ok := importClient(w, r, db)
		if !ok {
			return
		}
		if !canReview(r) {
			problem(w, 403, "Accepting a file's books requires workpapers:review.")
			return
		}
		var in struct {
			System           string `json:"system"`
			ExportedOn       string `json:"exported_on"`
			FinalAdjustments bool   `json:"final_adjustments"`
			Explained        string `json:"explained"`
			Reason           string `json:"reason"`
			Reading          string `json:"reading"`
			Scope            string `json:"scope"`
			BasedOn          string `json:"based_on"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.System, in.Explained, in.Reason = strings.TrimSpace(in.System), strings.TrimSpace(in.Explained), strings.TrimSpace(in.Reason)
		// Not after today anywhere: the date at the earliest time zone, so a member ahead of UTC can name their today.
		_, dateErr := time.Parse("2006-01-02", in.ExportedOn)
		latest := time.Now().UTC().Add(14 * time.Hour).Format("2006-01-02")
		switch {
		case in.System == "" || utf8.RuneCountInString(in.System) > 200:
			problem(w, 422, "Name the system the books were exported from (up to 200 characters).")
			return
		case dateErr != nil || in.ExportedOn > latest:
			problem(w, 422, "Enter the date the books were exported, as YYYY-MM-DD, not after today.")
			return
		case utf8.RuneCountInString(in.Explained) > 1000:
			problem(w, 422, "Keep the explanation to 1,000 characters.")
			return
		case in.Reason == "" || utf8.RuneCountInString(in.Reason) > 500:
			problem(w, 422, "Say why (1–500 characters).")
			return
		}
		org, id := organization(r), r.URL.Query().Get("id")
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "The acceptance could not be saved.")
			return
		}
		defer tx.Rollback()
		// Under the client's map lock, the reading and the map stand still while they are checked and accepted.
		if _, err = tx.ExecContext(r.Context(), `SELECT pg_advisory_xact_lock(hashtext('books_account_maps:'||$1||':'||client_identity($1,$2)))`, org, name); err != nil {
			problem(w, 503, "The acceptance could not be saved.")
			return
		}
		state, found, err := readFileState(r.Context(), tx, org, name, id)
		if err != nil {
			problem(w, 503, "The file's checks are unavailable.")
			return
		}
		switch {
		case !found:
			problem(w, 409, "Confirm how the file is read before accepting it.")
			return
		case state.scope == nil:
			problem(w, 409, "Attest what the file covers and its basis, for the reading confirmed now, before accepting it.")
			return
		case state.reading.ID != in.Reading || state.scope.ID != in.Scope || state.mapStamp != in.BasedOn:
			problem(w, 409, "The file's reading, scope or account map changed since you opened its checks: reopen them to see what changed.")
			return
		}
		checks := fileChecks(state, in.Explained)
		raw, _ := json.Marshal(checks)
		var accepted string
		if err = tx.QueryRowContext(r.Context(), `INSERT INTO books_import_acceptances(organization_id,client_entity,import_id,layout_id,scope_id,map_stamp,period,system,exported_on,final_adjustments,explained,checks,reason,actor)
 VALUES($1,$2,$3::bigint,$4::bigint,$5::bigint,$6,$7,$8,$9::date,$10,$11,$12::jsonb,$13,$14) RETURNING id::text`,
			org, name, id, state.reading.ID, state.scope.ID, state.mapStamp, state.reading.Period, in.System, in.ExportedOn, in.FinalAdjustments, in.Explained, string(raw), in.Reason, reviewerName(r)).Scan(&accepted); err != nil {
			problem(w, 503, "The acceptance could not be saved.")
			return
		}
		if err = tx.Commit(); err != nil {
			problem(w, 503, "The acceptance could not be saved.")
			return
		}
		jsonReply(w, 200, map[string]any{"id": accepted, "checks": checks, "holds": holding(checks)})
	})
}
