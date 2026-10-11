package transport

import (
	"cmp"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"maps"
	"math/big"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"close.local/backend/internal/preparation"
)

// budgetLine is one imported budget row: monthly amounts as the Profit and Loss shows them (income and expenses both
// positive), and the ledger account it is mapped to ("" while unmapped).
type budgetLine struct {
	Account   string            `json:"account"`
	AccountID string            `json:"account_id"`
	Months    map[string]string `json:"months"`
}

var budgetAmount = regexp.MustCompile(`^-?[0-9]{1,16}(\.[0-9]{1,2})?$`)

// parseBudget reads a CSV whose header is "Account" and then months as YYYY-MM; a blank cell is zero.
func parseBudget(text string) ([]budgetLine, error) {
	rows, err := csv.NewReader(strings.NewReader(text)).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("the file is not a readable CSV: %v", err)
	}
	if len(rows) < 2 || len(rows) > 501 || len(rows[0]) < 2 || len(rows[0]) > 25 || !strings.EqualFold(strings.TrimPrefix(strings.TrimSpace(rows[0][0]), "\ufeff"), "account") {
		return nil, fmt.Errorf("the first row must be Account and then 1 to 24 months as YYYY-MM, followed by 1 to 500 budget lines")
	}
	months := []string{}
	for _, m := range rows[0][1:] {
		m = strings.TrimSpace(m)
		if _, err := time.Parse("2006-01", m); err != nil || slices.Contains(months, m) {
			return nil, fmt.Errorf("%q is not a distinct month as YYYY-MM", m)
		}
		months = append(months, m)
	}
	lines, seen := []budgetLine{}, map[string]bool{}
	for i, row := range rows[1:] {
		name := strings.TrimSpace(row[0])
		if name == "" || len(name) > 200 || seen[strings.ToLower(name)] {
			return nil, fmt.Errorf("line %d needs a distinct account name of up to 200 characters", i+2)
		}
		seen[strings.ToLower(name)] = true
		line := budgetLine{Account: name, Months: map[string]string{}}
		for j, month := range months {
			cell := ""
			if j+1 < len(row) {
				cell = strings.NewReplacer(",", "", "$", "", " ", "").Replace(row[j+1])
			}
			if cell == "" {
				cell = "0"
			}
			if !budgetAmount.MatchString(cell) {
				return nil, fmt.Errorf("line %d, %s: %q is not an amount in cents", i+2, month, row[j+1])
			}
			value, _ := new(big.Rat).SetString(cell)
			line.Months[month] = value.FloatString(2)
		}
		lines = append(lines, line)
	}
	return lines, nil
}

// mapBudget maps each line to the one Profit and Loss account whose name matches exactly (ignoring case), keeping a
// mapping the line already has when that account is still in the chart.
func mapBudget(lines []budgetLine, accounts map[string]packAccount) {
	byName := map[string][]string{}
	for id, a := range accounts {
		if a.Class == "Revenue" || a.Class == "Expense" {
			byName[strings.ToLower(a.Name)] = append(byName[strings.ToLower(a.Name)], id)
		}
	}
	for i := range lines {
		if a, ok := accounts[lines[i].AccountID]; ok && (a.Class == "Revenue" || a.Class == "Expense") {
			continue
		}
		lines[i].AccountID = ""
		if ids := byName[strings.ToLower(lines[i].Account)]; len(ids) == 1 {
			lines[i].AccountID = ids[0]
		}
	}
}

// budgetProblems says why a version cannot be the approved baseline against a chart: an unmapped line, a mapping to an
// account the chart no longer has as income or expense, or two lines on one account.
func budgetProblems(lines []budgetLine, accounts map[string]packAccount) []string {
	problems, used := []string{}, map[string]string{}
	for _, l := range lines {
		a, known := accounts[l.AccountID]
		switch {
		case l.AccountID == "":
			problems = append(problems, l.Account+" is not mapped to a ledger account.")
		case accounts != nil && (!known || (a.Class != "Revenue" && a.Class != "Expense")):
			problems = append(problems, l.Account+" is mapped to an account that is no longer an income or expense account in the ledger.")
		case used[l.AccountID] != "":
			problems = append(problems, l.Account+" and "+used[l.AccountID]+" are mapped to the same account.")
		default:
			used[l.AccountID] = l.Account
		}
	}
	return problems
}

type budgetFigure struct {
	Actual    string  `json:"actual"`
	Budget    string  `json:"budget"`
	Variance  string  `json:"variance"`
	Percent   *string `json:"percent"`
	Favorable bool    `json:"favorable"`
	Material  bool    `json:"material"`
}

type budgetRow struct {
	AccountID  string       `json:"account_id"`
	Name       string       `json:"name"`
	Class      string       `json:"class"`
	Month      budgetFigure `json:"month"`
	YTD        budgetFigure `json:"ytd"`
	Prior      *string      `json:"prior_month"`
	Unbudgeted bool         `json:"unbudgeted"`
}

// figure compares actual with budget for an income (or expense) line: variance is actual minus budget, favorable when
// income is at or above budget or expenses at or below it; a percentage only against a positive budget.
func figure(actual, budget *big.Rat, income bool, materiality *big.Rat) budgetFigure {
	variance := new(big.Rat).Sub(actual, budget)
	f := budgetFigure{Actual: actual.FloatString(2), Budget: budget.FloatString(2), Variance: variance.FloatString(2), Favorable: (variance.Sign() >= 0) == income || variance.Sign() == 0}
	if budget.Sign() > 0 {
		p := new(big.Rat).Mul(new(big.Rat).Quo(variance, budget), big.NewRat(100, 1)).FloatString(1)
		f.Percent = &p
	}
	f.Material = materiality != nil && new(big.Rat).Abs(variance).Cmp(materiality) >= 0
	return f
}

// compareBudget sets the month's and year-to-date actuals against an approved budget, by mapped account. actuals holds
// each month's Profit and Loss by account; ytd lists the fiscal months through the close month. Year to date compares
// only months that both have books and are budgeted, on both sides; the others are returned as missing (no books) and
// unbudgeted (no budget column). Accounts with activity but no budget line are shown as unbudgeted; nothing is netted
// away.
func compareBudget(lines []budgetLine, actuals map[string]map[string]*big.Rat, accounts map[string]packAccount, period, prior string, ytd []string, materiality *big.Rat) (rows []budgetRow, totals map[string]budgetFigure, missing, unbudgeted []string) {
	budgetOf, covered := map[string]budgetLine{}, map[string]bool{}
	for _, l := range lines {
		if l.AccountID != "" {
			budgetOf[l.AccountID] = l
		}
		for month := range l.Months {
			covered[month] = true
		}
	}
	compared, missing, unbudgeted := []string{}, []string{}, []string{}
	for _, month := range ytd {
		switch {
		case actuals[month] == nil:
			missing = append(missing, month)
		case !covered[month]:
			unbudgeted = append(unbudgeted, month)
		default:
			compared = append(compared, month)
		}
	}
	ids := map[string]bool{}
	for id := range budgetOf {
		ids[id] = true
	}
	for _, month := range append([]string{period, prior}, compared...) {
		for id, v := range actuals[month] {
			if v.Sign() != 0 {
				ids[id] = true
			}
		}
	}
	sums := map[string][4]*big.Rat{}
	for _, class := range []string{"Revenue", "Expense"} {
		sums[class] = [4]*big.Rat{new(big.Rat), new(big.Rat), new(big.Rat), new(big.Rat)}
	}
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		class := accounts[id].Class
		if class != "Revenue" && class != "Expense" {
			continue
		}
		line, budgeted := budgetOf[id]
		monthBudget, ytdBudget, ytdActual := new(big.Rat), new(big.Rat), new(big.Rat)
		if budgeted {
			monthBudget.SetString(cmp.Or(line.Months[period], "0"))
		}
		for _, month := range compared {
			ytdActual.Add(ytdActual, orZero(actuals[month], id))
			if budgeted {
				v, _ := new(big.Rat).SetString(cmp.Or(line.Months[month], "0"))
				ytdBudget.Add(ytdBudget, v)
			}
		}
		income := class == "Revenue"
		row := budgetRow{AccountID: id, Name: cmp.Or(accounts[id].Name, line.Account, id), Class: class, Unbudgeted: !budgeted,
			Month: figure(orZero(actuals[period], id), monthBudget, income, materiality), YTD: figure(ytdActual, ytdBudget, income, materiality)}
		if actuals[prior] != nil {
			p := orZero(actuals[prior], id).FloatString(2)
			row.Prior = &p
		}
		rows = append(rows, row)
		s := sums[class]
		s[0].Add(s[0], orZero(actuals[period], id))
		s[1].Add(s[1], monthBudget)
		s[2].Add(s[2], ytdActual)
		s[3].Add(s[3], ytdBudget)
	}
	net := func(i int) *big.Rat { return new(big.Rat).Sub(sums["Revenue"][i], sums["Expense"][i]) }
	totals = map[string]budgetFigure{
		"income_month": figure(sums["Revenue"][0], sums["Revenue"][1], true, nil), "income_ytd": figure(sums["Revenue"][2], sums["Revenue"][3], true, nil),
		"expense_month": figure(sums["Expense"][0], sums["Expense"][1], false, nil), "expense_ytd": figure(sums["Expense"][2], sums["Expense"][3], false, nil),
		"net_month": figure(net(0), net(1), true, materiality), "net_ytd": figure(net(2), net(3), true, materiality),
	}
	return rows, totals, missing, unbudgeted
}

// budgetTotals are a version's monthly income, expense and net totals by the chart's classes, with unmapped or
// unclassified lines apart, so a person sees the plan's figures before approving it.
func budgetTotals(lines []budgetLine, accounts map[string]packAccount) map[string]map[string]string {
	out := map[string]map[string]string{}
	if len(lines) == 0 {
		return out
	}
	for month := range lines[0].Months {
		income, expense, other := new(big.Rat), new(big.Rat), new(big.Rat)
		for _, l := range lines {
			v, _ := new(big.Rat).SetString(cmp.Or(l.Months[month], "0"))
			switch accounts[l.AccountID].Class {
			case "Revenue":
				income.Add(income, v)
			case "Expense":
				expense.Add(expense, v)
			default:
				other.Add(other, v)
			}
		}
		out[month] = map[string]string{"income": income.FloatString(2), "expense": expense.FloatString(2), "net": new(big.Rat).Sub(income, expense).FloatString(2), "unmapped": other.FloatString(2)}
	}
	return out
}

type budgetVersion struct {
	ID         string                       `json:"id"`
	Name       string                       `json:"name"`
	Company    string                       `json:"company"`
	Currency   string                       `json:"currency"`
	Lines      []budgetLine                 `json:"lines"`
	Source     string                       `json:"source"`
	BasedOn    *string                      `json:"based_on"`
	CreatedBy  string                       `json:"created_by"`
	CreatedAt  time.Time                    `json:"created_at"`
	ApprovedBy string                       `json:"approved_by"`
	ApprovedAt *time.Time                   `json:"approved_at"`
	Problems   []string                     `json:"problems"`
	Totals     map[string]map[string]string `json:"totals"`
}

func registerBudget(mux *http.ServeMux, db *sql.DB) {
	client := func(r *http.Request) (org, entity, period string, err error) {
		err = db.QueryRowContext(r.Context(), `SELECT organization_id,entity,period FROM client_closes WHERE id::text=$1`, r.PathValue("id")).Scan(&org, &entity, &period)
		return
	}
	// chart is a close's newest capture as books, with the ledger company it came from; nil when nothing is captured. A
	// month whose books are its files reads its accepted file's books (company "files:" and its basis, so months on
	// another basis are never compared or combined), none while the checks it was accepted with hold its statements.
	chart := func(r *http.Request, closeID, period string) (*books, string, error) {
		if source, err := closeBooksSource(r.Context(), db, closeID); err != nil {
			return nil, "", err
		} else if source == "files" {
			b, month, err := fileMonthBooks(r.Context(), db, closeID)
			if err != nil || b == nil || len(month.Holds) > 0 {
				return nil, "files", err
			}
			return b, "files:" + month.Basis, nil
		}
		m, err := periodBooks(r.Context(), db, closeID, period)
		if err != nil || m == nil {
			return nil, "", err
		}
		b, err := recordBooks(r.Context(), db, m)
		return b, m.Source + ":" + m.realm, err
	}
	versions := func(r *http.Request, org, entity string, accounts map[string]packAccount) ([]budgetVersion, error) {
		rows, err := db.QueryContext(r.Context(), `SELECT id::text,name,company,currency,lines,source,based_on::text,created_by,created_at,COALESCE(approved_by,''),approved_at FROM budget_versions WHERE organization_id=$1 AND lower(client_entity)=lower($2) ORDER BY budget_versions.id DESC`, org, entity)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []budgetVersion{}
		for rows.Next() {
			var v budgetVersion
			var raw []byte
			if err := rows.Scan(&v.ID, &v.Name, &v.Company, &v.Currency, &raw, &v.Source, &v.BasedOn, &v.CreatedBy, &v.CreatedAt, &v.ApprovedBy, &v.ApprovedAt); err != nil {
				return nil, err
			}
			if err := json.Unmarshal(raw, &v.Lines); err != nil {
				return nil, err
			}
			v.Problems, v.Totals = budgetProblems(v.Lines, accounts), budgetTotals(v.Lines, accounts)
			out = append(out, v)
		}
		return out, rows.Err()
	}
	mux.HandleFunc("GET /v1/closes/{id}/budget", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		org, entity, period, err := client(r)
		if err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		this, company, err := chart(r, r.PathValue("id"), period)
		if err != nil {
			problem(w, 503, "The month's books are unavailable.")
			return
		}
		var accounts map[string]packAccount
		choices := []map[string]string{}
		if this != nil {
			accounts = this.Accounts
			for id, a := range accounts {
				if a.Class == "Revenue" || a.Class == "Expense" {
					choices = append(choices, map[string]string{"id": id, "name": a.Name, "class": a.Class})
				}
			}
			slices.SortFunc(choices, func(a, b map[string]string) int { return strings.Compare(a["name"], b["name"]) })
		}
		list, err := versions(r, org, entity, accounts)
		if err != nil {
			problem(w, 503, "Budgets are unavailable.")
			return
		}
		out := map[string]any{"period": period, "versions": list, "accounts": choices}
		// An executive sees the comparison with the approved baseline, not draft or earlier budget versions.
		if executive(r) {
			out["versions"], out["accounts"] = []budgetVersion{}, []any{}
		}
		wait := func(reason string) {
			out["state"], out["reason"] = "waiting", reason
			jsonReply(w, 200, out)
		}
		if this == nil && company == "files" {
			// An accepted file whose checks hold its statements names them; otherwise a reading is still to be accepted.
			if _, month, err := fileMonthBooks(ctx, db, r.PathValue("id")); err == nil && month != nil && len(month.Holds) > 0 {
				wait("The accepted file " + month.File + " has checks that hold its statements: " + strings.Join(month.Holds, "; ") + ".")
				return
			}
			wait("This month's books are its files: accept a reading of the month's file first.")
			return
		}
		if this == nil {
			wait("Capture this month's QuickBooks period snapshot or Xero Trial Balance first.")
			return
		}
		// Without the month's activity there is no actual to compare: it is never taken as zero.
		if this.ActivityUnavailable != "" {
			wait("The month's activity is unavailable: " + this.ActivityUnavailable + ".")
			return
		}
		// The baseline is the newest approved version of these books that budgets this month.
		baseline := slices.IndexFunc(list, func(v budgetVersion) bool {
			_, covers := v.Lines[0].Months[period]
			return v.ApprovedAt != nil && v.Company == company && covers
		})
		if baseline < 0 {
			out["state"] = "no_budget"
			jsonReply(w, 200, out)
			return
		}
		version := list[baseline]
		if version.Currency != this.Currency {
			wait("The approved budget is in " + version.Currency + " but the books are in " + this.Currency + ".")
			return
		}
		profile, err := preparation.ClientProfile(ctx, db, r.PathValue("id"))
		if err != nil {
			problem(w, 503, "The client's fiscal year is unavailable.")
			return
		}
		if profile.Unsettled() {
			wait(profile.Question() + ", so year to date cannot be set.")
			return
		}
		month, _ := time.Parse("2006-01", period)
		start := time.Date(month.Year(), time.Month(profile.FiscalStart), 1, 0, 0, 0, 0, time.UTC)
		if start.After(month) {
			start = start.AddDate(-1, 0, 0)
		}
		ytd := []string{}
		for m := start; !m.After(month); m = m.AddDate(0, 1, 0) {
			ytd = append(ytd, m.Format("2006-01"))
		}
		prior := month.AddDate(0, -1, 0).Format("2006-01")
		// Each month's actuals are that month's close's newest capture of the same books, as the pack reads them.
		actuals, names := map[string]map[string]*big.Rat{period: this.Activity}, maps.Clone(this.Accounts)
		for _, m := range append(slices.Clone(ytd), prior) {
			if actuals[m] != nil {
				continue
			}
			var closeID string
			if err := db.QueryRowContext(ctx, `SELECT id::text FROM client_closes WHERE organization_id=$1 AND lower(entity)=lower($2) AND period=$3`, org, entity, m).Scan(&closeID); err != nil {
				continue
			}
			if b, c, err := chart(r, closeID, m); err == nil && b != nil && c == company && b.Currency == this.Currency && b.ActivityUnavailable == "" {
				actuals[m] = b.Activity
				for id, a := range b.Accounts {
					if _, ok := names[id]; !ok {
						names[id] = a
					}
				}
			}
		}
		var materiality *big.Rat
		var raw sql.NullString
		if db.QueryRowContext(ctx, `SELECT materiality_amount::text FROM organizations WHERE id=$1`, org).Scan(&raw) == nil && raw.Valid {
			materiality, _ = new(big.Rat).SetString(raw.String)
		}
		// A line counts only through an account the current chart still has as income or expense; older charts name
		// accounts for actuals, never revive a mapping.
		current, excluded := slices.Clone(version.Lines), []string{}
		for i, l := range current {
			if c := accounts[l.AccountID].Class; l.AccountID == "" || (c != "Revenue" && c != "Expense") {
				current[i].AccountID = ""
				excluded = append(excluded, l.Account)
			}
		}
		rows, totals, missing, unbudgeted := compareBudget(current, actuals, names, period, prior, ytd, materiality)
		out["state"], out["baseline"], out["rows"], out["totals"] = "ready", map[string]any{"id": version.ID, "name": version.Name, "approved_by": version.ApprovedBy, "approved_at": version.ApprovedAt}, rows, totals
		out["currency"], out["ytd_from"], out["ytd_missing"], out["ytd_unbudgeted"], out["prior_month"], out["unmapped"] = this.Currency, ytd[0], missing, unbudgeted, prior, excluded
		out["materiality"] = raw.String
		out["note"] = "Actuals are each month's books as captured; a corrected actual changes the comparison on the next capture and never the budget. Amounts are as the Profit and Loss shows them: income and expenses both positive."
		jsonReply(w, 200, out)
	})
	mux.HandleFunc("POST /v1/closes/{id}/budget", func(w http.ResponseWriter, r *http.Request) {
		who, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var in struct {
			Action  string            `json:"action"`
			ID      string            `json:"id"`
			Name    string            `json:"name"`
			CSV     string            `json:"csv"`
			Source  string            `json:"source"`
			Mapping map[string]string `json:"mapping"`
		}
		if !decode(w, r, &in) {
			return
		}
		org, entity, period, err := client(r)
		if err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		this, company, err := chart(r, r.PathValue("id"), period)
		if err != nil {
			problem(w, 503, "The month's books are unavailable.")
			return
		}
		if this == nil {
			problem(w, 409, "Capture this month's books first: budget lines are mapped to the ledger's accounts.")
			return
		}
		save := func(name, source string, lines []budgetLine, basedOn any) {
			mapBudget(lines, this.Accounts)
			raw, _ := json.Marshal(lines)
			var id string
			if err := db.QueryRowContext(r.Context(), `INSERT INTO budget_versions(organization_id,client_entity,name,company,currency,lines,digest,source,based_on,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id::text`,
				org, entity, name, company, this.Currency, string(raw), preparation.Digest(raw), source, basedOn, who).Scan(&id); err != nil {
				problem(w, 503, "The budget could not be saved.")
				return
			}
			jsonReply(w, 200, map[string]any{"id": id, "problems": budgetProblems(lines, this.Accounts)})
		}
		// A version is revised or approved only against the books it was made for, in its currency.
		target := func() (budgetVersion, bool) {
			list, err := versions(r, org, entity, this.Accounts)
			if err != nil {
				problem(w, 503, "Budgets are unavailable.")
				return budgetVersion{}, false
			}
			i := slices.IndexFunc(list, func(v budgetVersion) bool { return v.ID == in.ID })
			switch {
			case i < 0:
				problem(w, 404, "Budget version not found.")
			case list[i].Company != company || list[i].Currency != this.Currency:
				problem(w, 409, "That budget was made for other books or another currency; import it again for these books.")
			default:
				return list[i], true
			}
			return budgetVersion{}, false
		}
		switch in.Action {
		case "import":
			in.Name, in.Source = strings.TrimSpace(in.Name), strings.TrimSpace(in.Source)
			if in.Name == "" || len(in.Name) > 200 || len(in.CSV) > 512*1024 || len(in.Source) > 300 {
				problem(w, 422, "Name the budget (up to 200 characters) and import a CSV of at most 512 KiB.")
				return
			}
			lines, err := parseBudget(in.CSV)
			if err != nil {
				problem(w, 422, "The budget could not be read: "+err.Error()+".")
				return
			}
			save(in.Name, cmp.Or(in.Source, "Imported CSV"), lines, nil)
		case "remap":
			version, ok := target()
			if !ok {
				return
			}
			lines := slices.Clone(version.Lines)
			for key, accountID := range in.Mapping {
				n, err := strconv.Atoi(key)
				a, known := this.Accounts[accountID]
				if err != nil || n < 0 || n >= len(lines) || !known || (a.Class != "Revenue" && a.Class != "Expense") {
					problem(w, 422, "Map each line to an income or expense account in the client's chart.")
					return
				}
				lines[n].AccountID = accountID
			}
			save(version.Name, "Remapped from version #"+version.ID, lines, version.ID)
		case "approve":
			version, ok := target()
			switch {
			case !ok:
			case version.ApprovedAt != nil:
				problem(w, 409, "That version is already approved.")
			case len(version.Problems) > 0:
				problem(w, 409, "Resolve the mapping first: "+strings.Join(version.Problems, " "))
			default:
				if _, err := db.ExecContext(r.Context(), `UPDATE budget_versions SET approved_by=$2,approved_at=now() WHERE id::text=$1 AND approved_at IS NULL`, in.ID, who); err != nil {
					problem(w, 503, "The approval could not be saved.")
					return
				}
				jsonReply(w, 200, map[string]bool{"approved": true})
			}
		default:
			problem(w, 422, "Import, remap or approve a budget.")
		}
	})
}
