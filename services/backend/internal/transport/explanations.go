package transport

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"close.local/backend/internal/ledger"
)

// lineExplanation is one profit and loss line's explanation in a management pack: drafted by the agent from the
// ledger as an internal draft, then reviewed, edited and published with the pack, frozen with its figures.
type lineExplanation struct {
	AccountID string `json:"account_id"`
	Name      string `json:"name"`
	Change    string `json:"change"`
	Material  bool   `json:"material"`
	Text      string `json:"text"`
}

// payrollLine is a line whose payees are people's pay: its explanation never names them.
var payrollLine = regexp.MustCompile(`(?i)payroll|wage|salar|bonus|commission|compensation|pension|401\(?k`)

// draftExplanations drafts an explanation for each line the close's pack explains (material movements first, as the
// commentary chooses them): the movement, then what the ledger attributes it to by payee, or plainly that the books do
// not show its cause. It states ledger facts only, never a cause the ledger does not record, and names no payee of a
// payroll line. The drafts are the firm's until a reviewer publishes them with the pack.
func draftExplanations(ctx context.Context, db *sql.DB, closeID string) (string, []lineExplanation, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return "", nil, err
	}
	defer tx.Rollback()
	pack, err := closeManagementPack(ctx, tx, closeID)
	if err != nil {
		return "", nil, err
	}
	if pack["state"] != "ready" {
		return "", []lineExplanation{}, nil
	}
	var materiality sql.NullString
	if err = tx.QueryRowContext(ctx, `SELECT o.materiality_amount::text FROM organizations o JOIN client_closes c ON c.organization_id=o.id WHERE c.id::text=$1`, closeID).Scan(&materiality); err != nil {
		return "", nil, err
	}
	tx.Rollback()
	pl, _ := pack["profit_and_loss"].(statement)
	currency, _ := pack["currency"].(string)
	lines, floor, _ := movedLines(pl, materiality.String)
	// The payees come from the same capture as the pack's figures, examined for every line it explains; drivers read from
	// another capture (one arriving meanwhile) are not used.
	drivers := map[string]map[string]any{}
	snapshot, _ := pack["snapshot_id"].(string)
	prior, _ := pack["prior"].(map[string]any)
	priorSnapshot, _ := prior["snapshot_id"].(string)
	if report, status, _ := fluxReport(ctx, db, closeID, max(8, len(lines))); status == 0 && report["state"] == "ready" && report["this"].(*fluxMonth).SnapshotID == snapshot && report["last"].(*fluxMonth).SnapshotID == priorSnapshot {
		changes, _ := report["changes"].([]map[string]any)
		for _, c := range changes {
			id, _ := c["account_id"].(string)
			drivers[id] = c
		}
	}
	out := []lineExplanation{}
	for _, l := range lines {
		item, found := drivers[l.AccountID]
		out = append(out, explainLine(l, item, found, currency, floor.Sign() > 0))
	}
	digest, _ := pack["digest"].(string)
	return digest, out, nil
}

func registerExplanations(mux *http.ServeMux, db *sql.DB) {
	// The drafted explanations of the pack's lines, for the reviewer's publication preview; the firm's alone.
	mux.HandleFunc("GET /v1/closes/{id}/management-pack/explanations", func(w http.ResponseWriter, r *http.Request) {
		if executive(r) {
			problem(w, 403, "Drafted explanations are the firm's until they are published.")
			return
		}
		digest, drafts, err := draftExplanations(r.Context(), db, r.PathValue("id"))
		if refused, ok := err.(tieRefusal); ok {
			problem(w, refused.status, refused.message)
			return
		}
		if err != nil {
			problem(w, 503, "Drafted explanations are unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"digest": digest, "explanations": drafts})
	})
}

// explainLine drafts one moved line's explanation: the movement, then what the ledger attributes it to by payee (item,
// when found, is the line's flux report entry), or plainly that the books do not show its cause; a payroll line names
// no payee.
func explainLine(l packLine, item map[string]any, found bool, currency string, material bool) lineExplanation {
	show := func(amount string) string { return ledger.Show(amount, currency) }
	change, _ := new(big.Rat).SetString(*l.Change)
	moved := fmt.Sprintf("up %s from %s", show(money(new(big.Rat).Abs(change))), show(*l.Prior))
	if change.Sign() < 0 {
		moved = fmt.Sprintf("down %s from %s", show(money(new(big.Rat).Abs(change))), show(*l.Prior))
	}
	e := lineExplanation{AccountID: l.AccountID, Name: l.Name, Change: *l.Change, Material: material}
	switch {
	case payrollLine.MatchString(l.Name):
		e.Text = fmt.Sprintf("%s %s. Payroll detail is not shown here.", l.Name, moved)
	case found && item["causes_unavailable"] == nil:
		parts := []string{}
		added, _ := item["new"].([]payeeAmount)
		changed, _ := item["changed"].([]payeeAmount)
		missing, _ := item["missing"].([]payeeAmount)
		for _, p := range added {
			parts = append(parts, fmt.Sprintf("%s %s, none last month", p.Name, show(p.ThisMonth)))
		}
		for _, p := range changed {
			parts = append(parts, fmt.Sprintf("%s %s against %s", p.Name, show(p.ThisMonth), show(p.LastMonth)))
		}
		for _, p := range missing {
			parts = append(parts, fmt.Sprintf("%s nothing this month against %s", p.Name, show(p.LastMonth)))
		}
		// What the named payees shown account for; the rest (transactions without a payee, payees not shown, or a ledger
		// that does not reconcile) is stated as the exact amount left.
		shown := new(big.Rat)
		for _, list := range [][]payeeAmount{added, changed, missing} {
			for _, p := range list {
				now, _ := new(big.Rat).SetString(p.ThisMonth)
				before, _ := new(big.Rat).SetString(p.LastMonth)
				shown.Add(shown, now.Sub(now, before))
			}
		}
		rest := new(big.Rat).Sub(change, shown)
		switch {
		case len(parts) == 0:
			e.Text = fmt.Sprintf("%s %s. The ledger does not attribute the movement to payees; the cause is not identified.", l.Name, moved)
		case rest.Sign() == 0 && item["reconciled"] == true:
			e.Text = fmt.Sprintf("%s %s. By payee in the ledger: %s.", l.Name, moved, strings.Join(parts, "; "))
		case rest.Sign() == 0:
			e.Text = fmt.Sprintf("%s %s. By payee in the ledger: %s; the ledger's payee totals do not tie to the reported movement.", l.Name, moved, strings.Join(parts, "; "))
		default:
			e.Text = fmt.Sprintf("%s %s. By payee in the ledger: %s; %s of the movement is not attributed to these payees.", l.Name, moved, strings.Join(parts, "; "), show(money(rest)))
		}
	default:
		e.Text = fmt.Sprintf("%s %s. The cause is not identified from the books.", l.Name, moved)
	}
	return e
}

// reviewedExplanations checks the reviewer's explanations against the pack's profit and loss: each is of a line that
// moved, once, in at most 600 characters, and every material movement (at or above the firm's materiality, when it
// sets one) is explained, plainly saying when its cause is not known. It returns them with each line's figures, ready
// to freeze, or why they cannot be published.
func reviewedExplanations(pl statement, materiality string, given []lineExplanation) ([]lineExplanation, string) {
	moving := map[string]packLine{}
	for _, s := range pl.Sections {
		for _, l := range s.Lines {
			if change, ok := new(big.Rat).SetString(cmp.Or(deref(l.Change), "0")); ok && change.Sign() != 0 {
				moving[l.AccountID] = l
			}
		}
	}
	explained := []lineExplanation{}
	for _, e := range given {
		l, ok := moving[e.AccountID]
		text := strings.TrimSpace(e.Text)
		if !ok || text == "" || utf8.RuneCountInString(text) > 600 || strings.Contains(text, draftWhy) || slices.ContainsFunc(explained, func(x lineExplanation) bool { return x.AccountID == e.AccountID }) {
			return nil, "Explain each line once, a line of the pack that moved, in up to 600 characters."
		}
		explained = append(explained, lineExplanation{AccountID: l.AccountID, Name: l.Name, Change: *l.Change, Text: text})
	}
	material, floor, _ := movedLines(pl, materiality)
	if floor.Sign() > 0 {
		for _, l := range material {
			i := slices.IndexFunc(explained, func(x lineExplanation) bool { return x.AccountID == l.AccountID })
			if i < 0 {
				return nil, "Explain every material movement before publishing, starting with " + l.Name + "; where its cause is not known, say so."
			}
			explained[i].Material = true
		}
	}
	return explained, ""
}
