package bookfiles

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
)

// Open items (docs/books-sources.md, Open items and cash dates): an export of a client's unpaid invoices or bills,
// each a document with its party, dates and open amount, true at its own as-of time. Its columns are proposed from
// their headings and confirmed by a person, as a Trial Balance's are; headings, subtotals and totals are left aside.

// Role is what a column of an open-items export holds.
type Role string

const (
	RoleParty    Role = "party"
	RoleDocument Role = "document"
	RoleDate     Role = "date"
	RoleDue      Role = "due"
	RoleOpen     Role = "open"
	RoleCurrency Role = "currency"
)

// Roles are the roles a column may be confirmed as.
var Roles = []Role{RoleParty, RoleDocument, RoleDate, RoleDue, RoleOpen, RoleCurrency}

// OpenItemsLayout is how an open-items export is read: its table, the row of its headings, each column's role, the
// order its dates written in numbers are in, and how its amounts are written.
type OpenItemsLayout struct {
	Table     string       `json:"table"`
	HeaderRow int          `json:"header_row"`
	Columns   map[int]Role `json:"columns"`
	DateOrder string       `json:"date_order,omitempty"`
	Style     NumberStyle  `json:"style"`
}

// OpenItem is one document an export holds open: its party, number, dates and open amount (negative for a credit),
// and the row it was read from.
type OpenItem struct {
	Row      int    `json:"row"`
	Party    string `json:"party"`
	Document string `json:"document"`
	Date     string `json:"date,omitempty"`
	Due      string `json:"due,omitempty"`
	Open     string `json:"open"`
	Currency string `json:"currency,omitempty"`
}

// Key is a stable name for the document within its side: its party, number and whether it is a credit, so a decision
// about it carries over to the next export.
func (i OpenItem) Key(side string) string {
	name := side + "|" + strings.ToLower(strings.Join(strings.Fields(i.Party), " ")) + "|" + map[bool]string{true: "credit", false: "document"}[strings.HasPrefix(i.Open, "-")] + "|" + strings.ToLower(strings.TrimSpace(i.Document))
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:12])
}

// roleOf is the role a column's heading names, or "amount" for a document's whole amount (an open amount only when no
// column names one), or "" for none.
func roleOf(heading string) Role {
	words := strings.Fields(strings.NewReplacer(".", " ", "#", " # ", "_", " ", "/", " ", "(", " ", ")", " ").Replace(strings.ToLower(heading)))
	has := func(names ...string) bool {
		return slices.ContainsFunc(words, func(w string) bool { return slices.Contains(names, w) })
	}
	switch {
	case len(words) == 0:
		return ""
	case has("currency", "ccy"):
		return RoleCurrency
	case has("due") && has("amount", "balance", "open", "total"):
		return RoleOpen
	case has("due"):
		return RoleDue
	case has("date"):
		return RoleDate
	case has("open", "balance", "outstanding", "remaining", "unpaid"):
		return RoleOpen
	case has("num", "number", "no", "#", "ref", "reference") || (len(words) == 1 && has("invoice", "bill", "document", "doc", "transaction")):
		return RoleDocument
	case has("customer", "vendor", "supplier", "name", "client", "contact", "payee", "debtor", "creditor"):
		return RoleParty
	case has("amount", "total"):
		return "amount"
	}
	return ""
}

// ProposeOpenItems proposes how to read an export of open items: the first row, among each table's first 30, whose
// headings name an amount and a document number or a party (whose number column a person then names), its columns' roles, its dates' order and its amounts' style. ok is false when no
// row does.
func ProposeOpenItems(f File) (l OpenItemsLayout, ok bool) {
	// Headings naming a number are looked for first, through every table, before ones naming only a party: a summary
	// section above a detailed one must not be taken for it.
	for _, numbered := range []bool{true, false} {
		if l, ok = proposeOpenItems(f, numbered); ok {
			return l, true
		}
	}
	return l, false
}

func proposeOpenItems(f File, numbered bool) (l OpenItemsLayout, ok bool) {
	for _, t := range f.Tables {
		for i, row := range t.Rows {
			if i >= 30 {
				break
			}
			columns := headingRoles(row)
			if !slices.Contains(mapValues(columns), RoleOpen) || !slices.Contains(mapValues(columns), RoleDocument) && (numbered || !slices.Contains(mapValues(columns), RoleParty)) {
				continue
			}
			return layoutAt(t, row, columns), true
		}
	}
	return l, false
}

// ProposeOpenItemsAt proposes how to read an export of open items whose headings a person said are row headerRow of
// table: each column's role as its heading names it, and the dates' order and amounts' style below. ok is false when
// the file has no such row.
func ProposeOpenItemsAt(f File, table string, headerRow int) (OpenItemsLayout, bool) {
	for _, t := range f.Tables {
		for _, row := range t.Rows {
			if t.Name == table && row.Number == headerRow {
				return layoutAt(t, row, headingRoles(row)), true
			}
		}
	}
	return OpenItemsLayout{}, false
}

// headingRoles is the role each heading of row names, each role once; a document's whole amount is its open amount
// when no heading names one.
func headingRoles(row Row) map[int]Role {
	columns, amount := map[int]Role{}, -1
	for _, c := range row.Cells {
		role := roleOf(c.Value)
		if role == "amount" {
			if amount < 0 {
				amount = c.Column
			}
			continue
		}
		if role != "" && !slices.Contains(mapValues(columns), role) {
			columns[c.Column] = role
		}
	}
	if !slices.Contains(mapValues(columns), RoleOpen) && amount >= 0 {
		columns[amount] = RoleOpen
	}
	return columns
}

// RestyleOpenItems is layout l with how its amounts mark decimals read again from the column it now gives the open
// amount, as a person may have named another: the dates' order and the amounts' scale are kept as given.
func RestyleOpenItems(f File, l OpenItemsLayout) OpenItemsLayout {
	for _, t := range f.Tables {
		for _, row := range t.Rows {
			if t.Name == l.Table && row.Number == l.HeaderRow {
				order, scale := l.DateOrder, l.Style.Scale
				l = layoutAt(t, row, l.Columns)
				l.DateOrder = order
				if scale != 0 {
					l.Style.Scale = scale
				}
				return l
			}
		}
	}
	return l
}

// layoutAt is the layout of table t headed by row, with columns' roles: the order its dates are in and how its amounts
// are written, as the rows below say.
func layoutAt(t Table, row Row, columns map[int]Role) OpenItemsLayout {
	l := OpenItemsLayout{Table: t.Name, HeaderRow: row.Number, Columns: columns}
	var dates, amounts []string
	for _, r := range t.Rows {
		if r.Number <= row.Number {
			continue
		}
		for _, c := range r.Cells {
			switch columns[c.Column] {
			case RoleDate, RoleDue:
				if c.Kind != Date {
					dates = append(dates, c.Value)
				}
			case RoleOpen:
				amounts = append(amounts, c.Value)
			}
		}
	}
	l.DateOrder = proposeDateOrder(dates)
	l.Style, _ = GuessNumberStyle(amounts)
	if l.Style.Scale == 0 {
		l.Style.Scale = 1
	}
	return l
}

// dateShaped is whether text begins as a date does, with a digit or a month's name, whether it reads or not: a row
// without a number but with one is reported, not passed over as a summary.
func dateShaped(text string) bool {
	fields := strings.Fields(strings.ToLower(text))
	if len(fields) == 0 {
		return false
	}
	if fields[0][0] >= '0' && fields[0][0] <= '9' {
		return true
	}
	_, month := monthsByName[fields[0][:min(3, len(fields[0]))]]
	return month && len(fields[0]) >= 3
}

func mapValues(m map[int]Role) []Role {
	out := make([]Role, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// ReadOpenItems reads the documents an export holds open by its confirmed layout, with the problems found. A row
// without an open amount is a heading: one holding only a name is the party of the documents below it that name none
// (as an aged report grouped by contact is laid out). An open item is followed up by its number, so a row without one
// is never counted: one with anything written as a date is a document left out, named as a problem; any other is a
// summary (a subtotal, a total), left aside. An export with none open is read as none, which a side all paid gives; a
// layout that cannot be read by (no table, a role given twice, no amount or number column) gives no list at all.
func ReadOpenItems(f File, l OpenItemsLayout) ([]OpenItem, []string) {
	var table *Table
	for i := range f.Tables {
		if f.Tables[i].Name == l.Table {
			table = &f.Tables[i]
		}
	}
	if table == nil {
		return nil, []string{"The file has no table " + l.Table + "."}
	}
	roles := map[Role]int{}
	for column, role := range l.Columns {
		if _, seen := roles[role]; seen {
			return nil, []string{"Each role is one column's: " + string(role) + " is given to two."}
		}
		roles[role] = column
	}
	if _, ok := roles[RoleOpen]; !ok {
		return nil, []string{"Say which column holds each document's open amount."}
	}
	if _, numbered := roles[RoleDocument]; !numbered {
		return nil, []string{"Say which column holds each document's number: each open item is followed up by it. Export the open items with their numbers."}
	}
	items, problems, heading := []OpenItem{}, []string{}, ""
	total := func(s string) bool { return strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), "total") }
	for _, row := range table.Rows {
		if row.Number <= l.HeaderRow {
			continue
		}
		cells := map[int]Cell{}
		var texts []string
		for _, c := range row.Cells {
			cells[c.Column] = c
			if strings.TrimSpace(c.Value) != "" {
				texts = append(texts, strings.TrimSpace(c.Value))
			}
		}
		text := func(role Role) string {
			column, ok := roles[role]
			if !ok {
				return ""
			}
			return strings.TrimSpace(cells[column].Value)
		}
		openCell := cells[roles[RoleOpen]]
		if strings.TrimSpace(openCell.Value) == "" {
			if len(texts) == 1 && !total(texts[0]) {
				heading = texts[0]
			}
			continue
		}
		party, document := text(RoleParty), text(RoleDocument)
		item := OpenItem{Row: row.Number, Party: party, Document: document, Currency: text(RoleCurrency)}
		var unread []string
		dateLike := false
		for _, d := range []struct {
			role Role
			to   *string
		}{{RoleDate, &item.Date}, {RoleDue, &item.Due}} {
			column, ok := roles[d.role]
			if !ok || strings.TrimSpace(cells[column].Value) == "" {
				continue
			}
			dateLike = dateLike || cells[column].Kind == Date || dateShaped(cells[column].Value)
			var err error
			if *d.to, err = readDate(cells[column], l.DateOrder); err != nil {
				unread = append(unread, fmt.Sprintf("Row %d: the %s %q cannot be read as a date.", row.Number, map[Role]string{RoleDate: "date", RoleDue: "due date"}[d.role], cells[column].Value))
			}
		}
		if document == "" {
			if dateLike {
				problems = append(problems, fmt.Sprintf("Row %d has no document number, so it is left out.", row.Number))
			}
			continue
		}
		if item.Party == "" {
			item.Party = heading
		}
		amount, err := cellAmount(openCell, l.Style)
		if err != nil {
			problems = append(problems, fmt.Sprintf("Row %d: the open amount %q cannot be read.", row.Number, openCell.Value))
			continue
		}
		item.Open = amount.Value.FloatString(2)
		problems = append(problems, unread...)
		items = append(items, item)
	}
	// Rows read as documents but none of them kept (their amounts unreadable, say) is not an export with none open.
	if len(items) == 0 && len(problems) > 0 {
		return nil, problems
	}
	return items, problems
}
