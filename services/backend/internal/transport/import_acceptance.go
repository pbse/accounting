package transport

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strconv"
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
	// MapChanged says the client's account map now classifies one of this file's accounts differently from the map the
	// acceptance was given on; the month's books keep the accepted map until the file is accepted again.
	MapChanged bool `json:"map_changed,omitempty"`
}

// acceptedMapChanged reports whether the account map acceptance id was given on classifies any of the reading's accounts
// (as the current map maps them) differently, by class or type.
func acceptedMapChanged(ctx context.Context, q rowReader, acceptance string, reading []fileAccount) (bool, error) {
	var stamp string
	if err := q.QueryRowContext(ctx, `SELECT map_stamp FROM books_import_acceptances WHERE id::text=$1`, acceptance).Scan(&stamp); err != nil {
		return false, err
	}
	accepted := map[string]mappedAccount{}
	if mapID, _, ok := strings.Cut(stamp, ":"); ok {
		var raw []byte
		if err := q.QueryRowContext(ctx, `SELECT accounts FROM books_account_maps WHERE id::text=$1`, mapID).Scan(&raw); err != nil && err != sql.ErrNoRows {
			return false, err
		}
		json.Unmarshal(raw, &accepted)
	}
	for _, a := range reading {
		was, had := accepted[a.Key]
		switch {
		case a.Mapped == nil && had, a.Mapped != nil && !had:
			return true, nil
		case a.Mapped != nil && (a.Mapped.Class != was.Class || a.Mapped.Type != was.Type):
			return true, nil
		}
	}
	return false, nil
}

// fileState is what a file's checks read: its confirmed reading and layout, its latest attestation (nil unless of this
// reading), the client's approved basis and currency for the month, the map as it stands, and the accounts with a
// balance in the prior month's accepted books that this reading does not name.
type fileState struct {
	format   string
	reading  fileReading
	layout   *confirmedRead
	brief    *confirmedRead
	scope    *importScope
	approved string
	currency string
	// fiscalStart is the client's fiscal year's first month (1–12), from its profile, when confirmed or reported by a
	// connected ledger (0 when only the default).
	fiscalStart int
	mapStamp    string
	recorded    bool
	prior       string
	missing     []string
	// priorBalances are the prior accepted month's closing balances by key, and classes each key's class in the map
	// in force, for the roll-forward.
	priorBalances map[string]string
	classes       map[string]string
	// reexport is the prior month's newest file read since its accepted one, with its balances by key: the evidence
	// that the prior month changed after it was accepted; nil without one.
	reexport *priorReexport
}

type priorReexport struct {
	name     string
	balances map[string]string
}

// monthActivity is each profit and loss account's activity for the month, by key, in the profit and loss sign (revenue
// and expenses both positive, as books.Activity has them), and where it came from (docs/books-sources.md, Monthly
// activity): the file's period-movement column; else, in the fiscal year's first month, the year-to-date balances
// themselves, unless the file is post-closing. Otherwise none, with what would supply it. It is never estimated.
func monthActivity(s fileState) (activity map[string]*big.Rat, from, missing string) {
	presented := func(class string, debitLess *big.Rat) *big.Rat {
		if class == "Revenue" {
			return new(big.Rat).Neg(debitLess)
		}
		return debitLess
	}
	var pl []fileAccount
	for _, a := range s.reading.Accounts {
		if a.Mapped != nil && (a.Mapped.Class == "Revenue" || a.Mapped.Class == "Expense") {
			pl = append(pl, a)
		}
	}
	movement := false
	for _, m := range s.layout.Layout.Columns {
		movement = movement || m == bookfiles.Movement
	}
	activity = map[string]*big.Rat{}
	switch {
	case movement:
		for _, a := range pl {
			// A movement unreadable, rounded at its source, or a formula's result (unless a recalculated copy is attested)
			// is not used: the month's activity waits for one that can be.
			switch held := a.MovementHeld; {
			case held == "unreadable":
				return nil, "", "a movement that reads for " + strings.TrimSpace(a.Number+" "+a.Name)
			case held == "rounded":
				return nil, "", "a movement exact to the cent for " + strings.TrimSpace(a.Number+" "+a.Name)
			case held == "formula" && (s.scope == nil || !s.scope.Recalculated):
				return nil, "", "a values-only export, or attest a recalculated copy: the movement for " + strings.TrimSpace(a.Number+" "+a.Name) + " is a formula's result"
			}
			m, ok := new(big.Rat).SetString(cmp.Or(a.Movement, "0"))
			if !ok {
				return nil, "", "a movement that reads for " + strings.TrimSpace(a.Number+" "+a.Name)
			}
			activity[a.Key] = presented(a.Mapped.Class, m)
		}
		return activity, "movement", ""
	case s.fiscalStart > 0 && strings.HasSuffix(s.reading.Period, fmt.Sprintf("-%02d", s.fiscalStart)):
		closed := true
		for _, a := range pl {
			b, ok := new(big.Rat).SetString(cmp.Or(a.Balance, "0"))
			if !ok {
				return nil, "", "an account's balance could not be read"
			}
			closed = closed && b.Sign() == 0
			activity[a.Key] = presented(a.Mapped.Class, b)
		}
		if closed && len(pl) > 0 {
			return nil, "", "the file is post-closing (its profit and loss accounts are closed to equity): export the Trial Balance before closing, or with the month's movement"
		}
		return activity, "year_to_date", ""
	}
	if s.fiscalStart == 0 {
		return nil, "", "a column with the month's movement (the net change for the month); the client's fiscal year is not confirmed, so its first month is not known"
	}
	return nil, "", "a column with the month's movement (the net change for the month), or a Trial Balance for the fiscal year's first month"
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
	s.classes = map[string]string{}
	if current != nil {
		s.mapStamp = current.Stamp
		for key, m := range current.Accounts {
			s.classes[key] = m.Class
		}
	}
	reading, found, err := readingAccounts(ctx, q, org, name, id, current)
	if err != nil || !found {
		return s, found, err
	}
	s.reading = reading
	if s.format, err = importFormat(ctx, q, org, name, id); err != nil {
		return s, false, err
	}
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
	if s.approved, _, err = approvedBasis(ctx, q, org, name, reading.Period); err != nil {
		return s, false, err
	}
	profile, err := preparation.ClientProfileOf(ctx, q, org, name, reading.Period)
	if err != nil {
		return s, false, err
	}
	s.currency = profile.Currency
	if profile.State == "confirmed" || profile.State == "detected" {
		s.fiscalStart = profile.FiscalStart
	}
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
	var priorImport string
	err = q.QueryRowContext(ctx, `SELECT COALESCE(l.reading->'lines','[]'::jsonb),a.import_id::text FROM books_import_acceptances a JOIN books_layouts l ON l.id=a.layout_id
 WHERE a.organization_id=$1 AND client_identity(a.organization_id,a.client_entity)=client_identity($1,$2) AND a.period=$3
  AND a.layout_id=(SELECT max(x.id) FROM books_layouts x WHERE x.import_id=a.import_id) AND a.scope_id=(SELECT max(y.id) FROM books_import_scopes y WHERE y.import_id=a.import_id)
 ORDER BY a.id DESC LIMIT 1`, org, name, s.prior).Scan(&priorLines, &priorImport)
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
	order, balances, names := lineBalances(lines)
	here := map[string]bool{}
	for _, a := range reading.Accounts {
		here[a.Key] = true
	}
	for _, key := range order {
		if !here[key] && nonZero(balances[key]) {
			s.missing = append(s.missing, names[key])
		}
	}
	s.priorBalances = balances
	if s.reexport, err = priorReexportOf(ctx, q, org, name, s.prior, priorImport); err != nil {
		return s, false, err
	}
	return s, true, nil
}

// priorReexportOf is the newest replacement of a month's accepted file, along the files' replaces chain, that can stand
// as that month's books: its reading confirmed with no problem and balanced, as of the month-end, in the accepted file's
// currency, every amount exact, a workbook's formula results marked and none unattested, its scope attested complete on
// that reading in the accepted file's basis. Nil without one: a file that cannot establish the month's closing books is
// no evidence that they changed.
func priorReexportOf(ctx context.Context, q rowReader, org, name, period, accepted string) (*priorReexport, error) {
	var basis, currency string
	var raw []byte
	if err := q.QueryRowContext(ctx, `WITH RECURSIVE chain(id) AS (SELECT $1::bigint UNION SELECT i.id FROM books_imports i JOIN chain c ON i.replaces=c.id)
 SELECT COALESCE((SELECT y.basis FROM books_import_acceptances a JOIN books_import_scopes y ON y.id=a.scope_id WHERE a.import_id=$1::bigint ORDER BY a.id DESC LIMIT 1),''),
  COALESCE((SELECT upper(l.layout->>'currency') FROM books_import_acceptances a JOIN books_layouts l ON l.id=a.layout_id WHERE a.import_id=$1::bigint ORDER BY a.id DESC LIMIT 1),''),
  COALESCE((SELECT jsonb_agg(jsonb_build_object('id',i.id::text,'name',i.name,'format',i.format) ORDER BY i.id DESC) FROM books_imports i WHERE i.id IN (SELECT id FROM chain) AND i.id<>$1::bigint),'[]')`, accepted).
		Scan(&basis, &currency, &raw); err != nil {
		return nil, err
	}
	var candidates []struct{ ID, Name, Format string }
	if err := json.Unmarshal(raw, &candidates); err != nil {
		return nil, err
	}
	for _, c := range candidates {
		brief, err := confirmedBrief(ctx, q, org, name, c.ID)
		if err != nil {
			return nil, err
		}
		if brief == nil || !brief.Reading.Balanced || len(brief.Reading.Problems) > 0 || brief.Layout.AsOf != monthEndDay(period) ||
			strings.ToUpper(strings.TrimSpace(brief.Layout.Currency)) != currency || c.Format != "csv" && !brief.Reading.FormulasMarked {
			continue
		}
		scope, err := latestImportScope(ctx, q, org, name, c.ID)
		if err != nil {
			return nil, err
		}
		if scope == nil || scope.LayoutID != brief.ID || !scope.Complete || scope.Basis != basis {
			continue
		}
		var lines []bookfiles.Line
		if err = q.QueryRowContext(ctx, `SELECT COALESCE(reading->'lines','[]'::jsonb) FROM books_layouts WHERE id::text=$1`, brief.ID).Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &lines); err != nil {
			return nil, err
		}
		if slices.ContainsFunc(lines, func(l bookfiles.Line) bool {
			return l.Kind == bookfiles.Posting && (!l.Cents || monthEndFormula(l) && !scope.Recalculated)
		}) {
			continue
		}
		_, balances, _ := lineBalances(lines)
		return &priorReexport{c.Name, balances}, nil
	}
	return nil, nil
}

// lineBalances is a reading's lines as balances by account key, in the order read, with each account's name: an
// account on several rows (one per department, say) has the balance of their sum.
func lineBalances(lines []bookfiles.Line) ([]string, map[string]string, map[string]string) {
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
	return order, balances, names
}

// rollForward compares each account's opening balance with the prior accepted month's closing balance
// (docs/books-sources.md, Roll-forward and changed prior months): from the file's opening column, or else its month-end
// balance less its movement. In the fiscal year's first month the profit and loss accounts open at zero, their prior
// closing balances carried into retained earnings, as the year's closing entries do: modelled, not a difference. The
// check is "" when the file gives no openings.
func rollForward(s fileState) importCheck {
	// The file's confirmed columns say what it gives: a blank cell in one is zero, as the month's activity reads it.
	var openingColumn, movementColumn bool
	if s.layout != nil {
		for _, m := range s.layout.Layout.Columns {
			openingColumn, movementColumn = openingColumn || m == bookfiles.Opening, movementColumn || m == bookfiles.Movement
		}
	}
	if !openingColumn && !movementColumn {
		return importCheck{}
	}
	check := importCheck{Key: "roll_forward", Title: "Rolls forward from the prior month", Holds: "The accounts that do not roll, and the statements built on them."}
	// A formula's stored result is released by the attestation of a recalculated copy, as for the other amounts; an
	// amount the file does not give exactly is never taken as zero.
	recalculated := s.scope != nil && s.scope.Recalculated
	usable := func(value, held string) (*big.Rat, string) {
		if held == "formula" && recalculated {
			held = ""
		}
		v, ok := new(big.Rat).SetString(cmp.Or(value, "0"))
		switch {
		case held != "":
			return nil, held
		case !ok:
			return nil, "unreadable"
		}
		return v, ""
	}
	opening, differences, unread := map[string]*big.Rat{}, []string{}, ""
	for _, a := range s.reading.Accounts {
		name := strings.TrimSpace(a.Number + " " + a.Name)
		balance, why := usable(a.Balance, "")
		movement := new(big.Rat)
		if movementColumn && why == "" {
			movement, why = usable(a.Movement, a.MovementHeld)
		}
		if why != "" {
			unread = cmp.Or(unread, name+": "+why)
			continue
		}
		if !openingColumn {
			opening[a.Key] = new(big.Rat).Sub(balance, movement)
			continue
		}
		v, why := usable(a.Opening, a.OpeningHeld)
		if why != "" {
			unread = cmp.Or(unread, name+": "+why)
			continue
		}
		opening[a.Key] = v
		// With both columns, the opening and the month's movement make the month-end balance.
		if movementColumn {
			if sum := new(big.Rat).Add(v, movement); sum.Cmp(balance) != 0 {
				differences = append(differences, fmt.Sprintf("%s opens at %s and moves by %s, but closes at %s", name, v.FloatString(2), movement.FloatString(2), balance.FloatString(2)))
			}
		}
	}
	// An amount that cannot be read holds whatever needed it: the file's own arithmetic, with both columns, or the
	// comparison with the prior month.
	if unread != "" && (movementColumn && openingColumn || s.prior != "") {
		check.State, check.Detail = "holds", "An opening or movement cannot be read as exact, so the month's openings are not checked ("+unread+")."
		return check
	}
	// With no accepted prior month the openings are not compared; the file's own arithmetic still is.
	if s.prior == "" {
		check.State, check.Detail, check.Holds = "note", "No accepted books for "+monthTitle(previousMonth(s.reading.Period))+" to roll from: the openings are not checked.", ""
		if len(differences) > 0 {
			check.State, check.Holds = "holds", "The accounts whose figures do not agree, and the statements built on them."
			check.Detail = fmt.Sprintf("%d account%s' opening and movement %s not make %s balance: %s.", len(differences), suffix(len(differences)), map[bool]string{true: "does", false: "do"}[len(differences) == 1], map[bool]string{true: "its", false: "their"}[len(differences) == 1], strings.Join(differences, "; "))
		}
		return check
	}
	yearStart := s.fiscalStart > 0 && monthOf(s.reading.Period) == s.fiscalStart
	// The closing balances a month's openings come from: at the fiscal year's start, the profit and loss closed into the
	// one retained earnings account; "" or why the year's result has nowhere to go.
	expect := func(closing map[string]string) (map[string]*big.Rat, string) {
		expected := map[string]*big.Rat{}
		for key, b := range closing {
			expected[key], _ = new(big.Rat).SetString(b)
		}
		if !yearStart {
			return expected, ""
		}
		result := new(big.Rat)
		for key, b := range expected {
			if c := s.classes[key]; c == "Revenue" || c == "Expense" {
				result.Add(result, b)
				expected[key] = new(big.Rat)
			}
		}
		var retained []string
		for _, a := range s.reading.Accounts {
			if a.Mapped != nil && a.Mapped.Class == "Equity" && contraName.MatchString(a.Name) {
				retained = append(retained, a.Key)
			}
		}
		switch {
		case len(retained) == 1:
			expected[retained[0]] = new(big.Rat).Add(cmp.Or(expected[retained[0]], new(big.Rat)), result)
		case result.Sign() != 0:
			return nil, "The year's result of " + result.FloatString(2) + " closes into retained earnings, and the file has " + map[bool]string{true: "no retained earnings account", false: "more than one"}[len(retained) == 0] + " to carry it to."
		}
		return expected, ""
	}
	expected, why := expect(s.priorBalances)
	if why != "" {
		check.State, check.Detail = "holds", why
		return check
	}
	// A difference the prior month's re-export closes at is that month's change since it was accepted; any other is
	// unexplained.
	var again map[string]*big.Rat
	if s.reexport != nil {
		again, _ = expect(s.reexport.balances)
	}
	changed := []string{}
	for _, a := range s.reading.Accounts {
		e := cmp.Or(expected[a.Key], new(big.Rat))
		if o := opening[a.Key]; o.Cmp(e) != 0 {
			line := fmt.Sprintf("%s opens at %s, closed at %s (%s)", strings.TrimSpace(a.Number+" "+a.Name), o.FloatString(2), e.FloatString(2), new(big.Rat).Sub(o, e).FloatString(2))
			if again != nil && o.Cmp(cmp.Or(again[a.Key], new(big.Rat))) == 0 {
				changed = append(changed, line)
			} else {
				differences = append(differences, line)
			}
		}
	}
	prior := monthTitle(s.prior)
	if n := len(changed) + len(differences); n > 0 {
		parts := []string{}
		if len(changed) > 0 {
			parts = append(parts, prior+" changed after it was accepted, as its re-export "+s.reexport.name+" shows: "+strings.Join(changed, "; "))
		}
		if len(differences) > 0 {
			parts = append(parts, "unexplained: "+strings.Join(differences, "; "))
		}
		check.State, check.Detail = "holds", fmt.Sprintf("%d account%s %s not roll from %s's accepted books, %s.", n, suffix(n), map[bool]string{true: "does", false: "do"}[n == 1], prior, strings.Join(parts, "; "))
		// Only when the re-export explains every difference does accepting it release them.
		if len(changed) > 0 && len(differences) == 0 {
			check.Holds = "The accounts that do not roll, and the statements built on them, until " + prior + "'s re-export is accepted, or these accounts explained."
		}
		return check
	}
	check.State, check.Detail, check.Holds = "passes", "Every account opens at "+prior+"'s accepted closing balance", ""
	if yearStart {
		check.Detail += ", the year's result carried into retained earnings"
	}
	check.Detail += "."
	return check
}

// monthOf is a period's month number (2027-01 is 1).
func monthOf(period string) int {
	m, _ := strconv.Atoi(strings.TrimPrefix(period[min(5, len(period)):], "0"))
	return m
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
	// The month's activity, which the management pack and the budget comparison read.
	if activity, from, missing := monthActivity(s); missing != "" {
		add(importCheck{Key: "activity", Title: "The month's activity", State: "holds", Detail: "Unavailable: send " + missing + ". It is never estimated.",
			Holds: "The management pack's profit and loss and the budget comparison for the month."})
	} else {
		revenue, expense := new(big.Rat), new(big.Rat)
		for _, a := range s.reading.Accounts {
			if v, ok := activity[a.Key]; ok && a.Mapped.Class == "Revenue" {
				revenue.Add(revenue, v)
			} else if ok {
				expense.Add(expense, v)
			}
		}
		said := "From the file's movement column"
		if from == "year_to_date" {
			said = "From the year-to-date balances: the fiscal year's first month"
		}
		add(importCheck{Key: "activity", Title: "The month's activity", State: "passes", Detail: fmt.Sprintf("%s: revenue %s, expenses %s.", said, revenue.FloatString(2), expense.FloatString(2))})
	}
	// With openings in the file, each account rolls from the prior accepted month.
	if c := rollForward(s); c.Key != "" {
		add(c)
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
	case s.format != "csv" && !s.brief.Reading.FormulasMarked:
		add(importCheck{Key: "formulas", Title: "Amounts are stored values", State: "holds", Detail: "This workbook's reading was confirmed before the reader marked formula results: confirm its layout again to see which amounts are formulas.",
			Holds: "Its use as the month's books, until its layout is confirmed again."})
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
		if err == nil && found && accepted != nil && accepted.Current {
			if accepted.MapChanged, err = acceptedMapChanged(r.Context(), db, accepted.ID, state.reading.Accounts); accepted.MapChanged {
				accepted.Current = false
			}
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
		// A file for a month that accepted another says how the month changed since.
		if found {
			if out["accepted_books"], err = monthChanged(r.Context(), db, org, name, id, state); err != nil {
				problem(w, 503, "The file's checks are unavailable.")
				return
			}
		}
		// A file accepted for a month whose books come from a live ledger is a supplement, reconciled to those books.
		if found && accepted != nil && accepted.Current {
			if out["supplement"], err = fileSupplement(r.Context(), db, org, name, accepted.ID, id); err != nil {
				problem(w, 503, "The file's checks are unavailable.")
				return
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
		// For a month whose books are its files, a new acceptance changes them: a month signed off, or with approved work,
		// keeps its books until it or its work is reopened, and drafts still in review are asked for again.
		// The close is locked first, in a statement of its own, so a sign-off or an approval either waits for this
		// acceptance or is seen by the read after it.
		var monthClose sql.NullInt64
		var settled bool
		if err = tx.QueryRowContext(r.Context(), `SELECT c.id FROM client_closes c WHERE c.organization_id=$1 AND client_identity(c.organization_id,c.entity)=client_identity($1,$2) AND c.period=$3 AND books_source(c.organization_id,c.entity,c.period)='files'
 ORDER BY c.id DESC LIMIT 1 FOR UPDATE`, org, name, state.reading.Period).Scan(&monthClose); err != nil && err != sql.ErrNoRows {
			problem(w, 503, "The acceptance could not be saved.")
			return
		}
		if monthClose.Valid {
			if err = tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT decision FROM close_signoffs s WHERE s.close_id=$1 ORDER BY s.id DESC LIMIT 1),'')='signed_off'
  OR EXISTS(SELECT 1 FROM workpaper_review_state w WHERE w.close_id=$1 AND w.review_state='approved')`, monthClose.Int64).Scan(&settled); err != nil {
				problem(w, 503, "The acceptance could not be saved.")
				return
			}
		}
		if monthClose.Valid && settled {
			problem(w, 409, "This month's books are its files, and it is signed off or has approved work: reopen it, or its work, before accepting another reading.")
			return
		}
		if monthClose.Valid {
			if _, err = tx.ExecContext(r.Context(), `INSERT INTO workpaper_reviews(workpaper_id,decision,note,actor,paper_digest)
 SELECT w.id::bigint,'changes_requested',$2,$3,w.digest FROM workpaper_review_state w WHERE w.close_id=$1 AND w.review_state='pending'
  AND NOT EXISTS(SELECT 1 FROM workpapers n WHERE n.close_id=w.close_id AND n.paper->>'revises'=w.id::text)`,
				monthClose.Int64, "The month's books are now another accepted reading of its files: prepare this from those books.", reviewerName(r)); err != nil {
				problem(w, 503, "The acceptance could not be saved.")
				return
			}
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
		// A month that reads its files has the books of this acceptance written at once.
		writeFileVersionsNow(r.Context(), db, org, name, state.reading.Period)
		jsonReply(w, 200, map[string]any{"id": accepted, "checks": checks, "holds": holding(checks)})
	})
}
