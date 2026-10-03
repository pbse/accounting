package transport

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"close.local/backend/internal/preparation"
)

// figuresDigest identifies what a pack reports, leaving out when it was read and its sign-off status.
func figuresDigest(pack map[string]any) string {
	prior, _ := pack["prior"].(map[string]any)
	figures := map[string]any{"entity": pack["entity"], "period": pack["period"], "currency": pack["currency"], "basis": pack["basis"],
		"profit_and_loss": pack["profit_and_loss"], "balance_sheet": pack["balance_sheet"], "working_capital": pack["working_capital"], "prior": prior["period"]}
	// A statement of cash flows is bound only when one is shown, so packs without one keep their digest.
	if flow, _ := pack["cash_flow"].(map[string]any); flow["sections"] != nil {
		figures["cash_flow"] = flow
	}
	raw, _ := json.Marshal(figures)
	return preparation.Digest(raw)
}

type publication struct {
	ID              string           `json:"id"`
	Version         int              `json:"version"`
	Digest          string           `json:"digest"`
	Commentary      string           `json:"commentary"`
	Supersedes      *string          `json:"supersedes"`
	Reason          string           `json:"reason"`
	PublishedBy     string           `json:"published_by"`
	PublishedAt     time.Time        `json:"published_at"`
	Acknowledgments []acknowledgment `json:"acknowledgments"`
	Pack            json.RawMessage  `json:"-"`
	State           string           `json:"state"`
}

type acknowledgment struct {
	Subject string    `json:"subject"`
	At      time.Time `json:"at"`
}

// publications lists a close's published packs, newest first; the newest is current unless the books changed since
// (or unverified while the live figures cannot be read), and every earlier one is superseded.
func publications(r *http.Request, tx *sql.Tx, closeID, current string) ([]publication, error) {
	rows, err := tx.QueryContext(r.Context(), `SELECT p.id::text,p.version,p.digest,p.commentary,p.supersedes::text,p.reason,p.published_by,p.published_at,p.pack,
 COALESCE((SELECT jsonb_agg(jsonb_build_object('subject',a.subject,'at',a.acknowledged_at) ORDER BY a.acknowledged_at) FROM management_acknowledgments a WHERE a.publication_id=p.id),'[]')
 FROM management_publications p WHERE p.close_id::text=$1 ORDER BY p.version DESC`, closeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []publication{}
	for rows.Next() {
		var p publication
		var acks []byte
		if err = rows.Scan(&p.ID, &p.Version, &p.Digest, &p.Commentary, &p.Supersedes, &p.Reason, &p.PublishedBy, &p.PublishedAt, &p.Pack, &acks); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(acks, &p.Acknowledgments); err != nil {
			return nil, err
		}
		switch {
		case len(out) > 0:
			p.State = "superseded"
		case current == "":
			p.State = "unverified"
		case current != p.Digest:
			p.State = "needs_correction"
		default:
			p.State = "current"
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func registerManagementPublications(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/{id}/management-pack/publications", func(w http.ResponseWriter, r *http.Request) {
		tx, err := db.BeginTx(r.Context(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			problem(w, 503, "Publications unavailable.")
			return
		}
		defer tx.Rollback()
		pack, err := closeManagementPack(r.Context(), tx, r.PathValue("id"))
		if refused, ok := err.(tieRefusal); ok {
			problem(w, refused.status, refused.message)
			return
		}
		if err != nil {
			problem(w, 503, "Publications unavailable.")
			return
		}
		current, _ := pack["digest"].(string)
		list, err := publications(r, tx, r.PathValue("id"), current)
		if err != nil {
			problem(w, 503, "Publications unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"publications": list})
	})
	// Publishing freezes the reviewed pack with its commentary; a correction is a new version naming the one it replaces.
	mux.HandleFunc("POST /v1/closes/{id}/management-pack/publications", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Action        string `json:"action"`
			Digest        string `json:"digest"`
			Commentary    string `json:"commentary"`
			Reason        string `json:"reason"`
			Confirmed     bool   `json:"confirmed"`
			PublicationID string `json:"publication_id"`
		}
		if !decode(w, r, &in) {
			return
		}
		closeID := r.PathValue("id")
		if executive(r) && in.Action != "acknowledge" {
			problem(w, 403, "Executive access is read-only; acknowledging a version is all it records.")
			return
		}
		if in.Action == "acknowledge" {
			result, err := db.ExecContext(r.Context(), `INSERT INTO management_acknowledgments(publication_id,subject) SELECT id,$3 FROM management_publications WHERE id::text=$1 AND close_id::text=$2 ON CONFLICT DO NOTHING`, in.PublicationID, closeID, subject(r))
			if err != nil {
				problem(w, 503, "The acknowledgment could not be recorded.")
				return
			}
			if n, _ := result.RowsAffected(); n == 0 {
				var exists bool
				if db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM management_publications WHERE id::text=$1 AND close_id::text=$2)`, in.PublicationID, closeID).Scan(&exists); !exists {
					problem(w, 404, "Publication not found.")
					return
				}
			}
			jsonReply(w, 200, map[string]any{"acknowledged": true})
			return
		}
		actor, ok := reviewActor(w, r)
		if !ok {
			return
		}
		in.Commentary, in.Reason = strings.TrimSpace(in.Commentary), strings.TrimSpace(in.Reason)
		if in.Action != "publish" || !in.Confirmed || in.Digest == "" || in.Commentary == "" || utf8.RuneCountInString(in.Commentary) > 4000 || utf8.RuneCountInString(in.Reason) > 2000 {
			problem(w, 422, "Confirm the reviewed pack and write its commentary (up to 4,000 characters).")
			return
		}
		// Like sign-off, lock first and read after: captures and sign-off decisions lock these closes, so the pack read
		// below is the latest for both months shown.
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Publishing is unavailable.")
			return
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(r.Context(), `SELECT p.id FROM client_closes c JOIN client_closes p ON p.id=c.id OR (p.organization_id=c.organization_id AND lower(p.entity)=lower(c.entity) AND p.period=to_char(to_date(c.period,'YYYY-MM')-interval '1 month','YYYY-MM'))
 WHERE c.id::text=$1 ORDER BY p.id FOR UPDATE OF p`, closeID); err != nil {
			problem(w, 503, "Publishing is unavailable.")
			return
		}
		var org string
		if err = tx.QueryRowContext(r.Context(), `SELECT organization_id FROM client_closes WHERE id::text=$1`, closeID).Scan(&org); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		pack, err := closeManagementPack(r.Context(), tx, closeID)
		if refused, ok := err.(tieRefusal); ok {
			problem(w, refused.status, refused.message)
			return
		}
		if err != nil {
			problem(w, 503, "Publishing is unavailable.")
			return
		}
		switch {
		case pack["state"] != "ready":
			problem(w, 409, "The pack is not ready: "+pack["reason"].(string))
			return
		case pack["digest"] != in.Digest:
			problem(w, 409, "The pack changed since you reviewed it. Review it again before publishing.")
			return
		case pack["overall"] != "signed_off" || pack["stale"] == true || pack["complete"] != true:
			problem(w, 409, "Only a complete pack from a current capture whose sign-off stands for every month shown can be published.")
			return
		}
		var latest sql.NullString
		var latestDigest string
		var version int
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text,version,digest FROM management_publications WHERE close_id::text=$1 ORDER BY version DESC LIMIT 1`, closeID).Scan(&latest, &version, &latestDigest); err != nil && err != sql.ErrNoRows {
			problem(w, 503, "Publishing is unavailable.")
			return
		}
		if latestDigest == in.Digest {
			problem(w, 409, "This pack is already published; nothing has changed since.")
			return
		}
		if latest.Valid && in.Reason == "" {
			problem(w, 422, "Say why this version corrects the published one.")
			return
		}
		frozen, _ := json.Marshal(pack)
		var id string
		if err = tx.QueryRowContext(r.Context(), `INSERT INTO management_publications(organization_id,close_id,version,pack,digest,commentary,supersedes,reason,published_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id::text`,
			org, closeID, version+1, frozen, in.Digest, in.Commentary, latest, in.Reason, actor).Scan(&id); err != nil || tx.Commit() != nil {
			problem(w, 503, "The pack could not be published.")
			return
		}
		jsonReply(w, 200, map[string]any{"id": id, "version": version + 1})
	})
	// An executive's overview: the closes of their assigned clients, and each close's newest published version with its
	// headline figures and whether they acknowledged it. Anyone else is told they are not an executive.
	mux.HandleFunc("GET /v1/closes/executive", func(w http.ResponseWriter, r *http.Request) {
		if !executive(r) {
			jsonReply(w, 200, map[string]any{"executive": false})
			return
		}
		const assigned = `c.organization_id=$1 AND EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=$2 AND lower(g.client_entity)=lower(c.entity) AND g.granted)`
		closes := []map[string]string{}
		rows, err := db.QueryContext(r.Context(), `SELECT c.id::text,c.entity,c.period FROM client_closes c WHERE `+assigned+` ORDER BY c.period DESC,c.entity`, organization(r), subject(r))
		if err != nil {
			problem(w, 503, "Your reports are unavailable.")
			return
		}
		for rows.Next() {
			var id, entity, period string
			if err = rows.Scan(&id, &entity, &period); err != nil {
				break
			}
			closes = append(closes, map[string]string{"id": id, "entity": entity, "period": period})
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		published := []map[string]any{}
		if err == nil {
			rows, err = db.QueryContext(r.Context(), `SELECT * FROM (SELECT DISTINCT ON (p.close_id) p.close_id::text,c.entity,c.period,p.id::text,p.version,p.published_at,p.pack,
 EXISTS(SELECT 1 FROM management_acknowledgments a WHERE a.publication_id=p.id AND a.subject=$2)
 FROM management_publications p JOIN client_closes c ON c.id=p.close_id WHERE `+assigned+` ORDER BY p.close_id,p.version DESC) newest ORDER BY period DESC,entity`, organization(r), subject(r))
		}
		if err != nil {
			problem(w, 503, "Your reports are unavailable.")
			return
		}
		defer rows.Close()
		for rows.Next() {
			var closeID, entity, period, id string
			var version int
			var at time.Time
			var raw []byte
			var acknowledged bool
			if err = rows.Scan(&closeID, &entity, &period, &id, &version, &at, &raw, &acknowledged); err != nil {
				problem(w, 503, "Your reports are unavailable.")
				return
			}
			var pack struct {
				Currency       string
				ProfitAndLoss  struct{ Measures []packLine } `json:"profit_and_loss"`
				WorkingCapital map[string]json.RawMessage    `json:"working_capital"`
			}
			json.Unmarshal(raw, &pack)
			figure := func(key string) string {
				var l packLine
				json.Unmarshal(pack.WorkingCapital[key], &l)
				return l.Amount
			}
			net := ""
			if n := len(pack.ProfitAndLoss.Measures); n > 0 {
				net = pack.ProfitAndLoss.Measures[n-1].Amount
			}
			published = append(published, map[string]any{"close_id": closeID, "entity": entity, "period": period, "publication_id": id, "version": version, "published_at": at,
				"acknowledged": acknowledged, "currency": pack.Currency, "net_income": net, "book_cash": figure("book_cash"), "working_capital": figure("working_capital")})
		}
		if rows.Err() != nil {
			problem(w, 503, "Your reports are unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"executive": true, "closes": closes, "published": published})
	})
	// One published version as it was frozen, to read in the page.
	mux.HandleFunc("GET /v1/closes/{id}/management-pack/publications/{publication}", func(w http.ResponseWriter, r *http.Request) {
		var p publication
		var acks []byte
		err := db.QueryRowContext(r.Context(), `SELECT p.id::text,p.version,p.digest,p.commentary,p.supersedes::text,p.reason,p.published_by,p.published_at,p.pack,
 COALESCE((SELECT jsonb_agg(jsonb_build_object('subject',a.subject,'at',a.acknowledged_at) ORDER BY a.acknowledged_at) FROM management_acknowledgments a WHERE a.publication_id=p.id),'[]')
 FROM management_publications p WHERE p.id::text=$1 AND p.close_id::text=$2`, r.PathValue("publication"), r.PathValue("id")).Scan(&p.ID, &p.Version, &p.Digest, &p.Commentary, &p.Supersedes, &p.Reason, &p.PublishedBy, &p.PublishedAt, &p.Pack, &acks)
		if err != nil || json.Unmarshal(acks, &p.Acknowledgments) != nil {
			problem(w, 404, "Publication not found.")
			return
		}
		jsonReply(w, 200, map[string]any{"publication": p, "pack": p.Pack})
	})
	// Exports are rendered from the frozen version, so they always agree with what was published.
	mux.HandleFunc("GET /v1/closes/{id}/management-pack/publications/{publication}/export", func(w http.ResponseWriter, r *http.Request) {
		var p publication
		var entity, period string
		err := db.QueryRowContext(r.Context(), `SELECT p.version,p.commentary,p.published_by,p.published_at,p.pack,c.entity,c.period FROM management_publications p JOIN client_closes c ON c.id=p.close_id WHERE p.id::text=$1 AND p.close_id::text=$2`,
			r.PathValue("publication"), r.PathValue("id")).Scan(&p.Version, &p.Commentary, &p.PublishedBy, &p.PublishedAt, &p.Pack, &entity, &period)
		if err != nil {
			problem(w, 404, "Publication not found.")
			return
		}
		rows, err := publicationRows(p)
		if err != nil {
			problem(w, 500, "The published pack could not be read.")
			return
		}
		name := strings.Map(func(c rune) rune {
			if c == '"' || c == '\\' || c < ' ' {
				return '-'
			}
			return c
		}, entity) + " " + period + " management pack v" + strconv.Itoa(p.Version)
		var body []byte
		switch r.URL.Query().Get("format") {
		case "csv":
			var buf bytes.Buffer
			out := csv.NewWriter(&buf)
			for _, row := range rows {
				for i := range row {
					// An amount, negative or not, stays a number; only text is guarded against formulas.
					if _, err := strconv.ParseFloat(row[i], 64); err != nil {
						row[i] = csvText(row[i])
					}
				}
				out.Write(row)
			}
			out.Flush()
			body, name = buf.Bytes(), name+".csv"
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		case "pdf":
			lines := make([]string, 0, len(rows))
			for _, row := range rows {
				cells := slices.DeleteFunc(slices.Clone(row), func(c string) bool { return c == "" })
				lines = append(lines, strings.Split(strings.ReplaceAll(strings.Join(cells, "  "), "\r", ""), "\n")...)
			}
			if body, err = linesPDF([][]string{lines}); err != nil {
				problem(w, 500, "The PDF could not be produced.")
				return
			}
			name += ".pdf"
			w.Header().Set("Content-Type", "application/pdf")
		default:
			problem(w, 422, "Choose csv or pdf.")
			return
		}
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		w.Write(body)
	})
}

// publicationRows lays a frozen pack out as statement rows: a heading, then each line with its prior and change.
func publicationRows(p publication) ([][]string, error) {
	var pack struct {
		Entity, Period, Currency, Basis, Source, Overall string
		AsOf                                             time.Time `json:"as_of"`
		Prior                                            *struct {
			Period string
			AsOf   time.Time `json:"as_of"`
		}
		ProfitAndLoss struct {
			Sections []packSection
			Measures []packLine
		} `json:"profit_and_loss"`
		BalanceSheet struct {
			Sections         []packSection
			Earnings         packLine
			TotalAssets      packLine `json:"total_assets"`
			TotalLiabilities packLine `json:"total_liabilities"`
			TotalEquity      packLine `json:"total_equity"`
		} `json:"balance_sheet"`
		WorkingCapital map[string]json.RawMessage `json:"working_capital"`
		CashFlow       struct {
			Sections    []packSection
			NetChange   packLine `json:"net_change"`
			Beginning   packLine
			Ending      packLine
			BookCash    string `json:"book_cash"`
			Reconciled  bool
			Differences []string
		} `json:"cash_flow"`
		Note string
	}
	if err := json.Unmarshal(p.Pack, &pack); err != nil {
		return nil, err
	}
	stamp := func(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }
	prior, priorCaptured := "", "no comparison"
	if pack.Prior != nil {
		prior, priorCaptured = pack.Prior.Period, pack.Prior.Period+" captured "+stamp(pack.Prior.AsOf)
	}
	rows := [][]string{
		{"Management pack", pack.Entity, pack.Period, pack.Basis + " basis", pack.Currency, "Version " + strconv.Itoa(p.Version)},
		{"Figures", "captured from " + pack.Source + " " + stamp(pack.AsOf), priorCaptured, "status " + pack.Overall},
		{"Published", stamp(p.PublishedAt), "by " + p.PublishedBy},
		{"Commentary", p.Commentary},
		{"Note", pack.Note},
		{"Statement", "Section", "Line", pack.Period, prior, "Change"},
	}
	optional := func(v *string) string {
		if v == nil {
			return ""
		}
		return *v
	}
	add := func(statement, section string, lines ...packLine) {
		for _, l := range lines {
			rows = append(rows, []string{statement, section, l.Name, l.Amount, optional(l.Prior), optional(l.Change)})
		}
	}
	for _, s := range pack.ProfitAndLoss.Sections {
		add("Profit and loss", s.Name, append(s.Lines, s.Total)...)
	}
	add("Profit and loss", "", pack.ProfitAndLoss.Measures...)
	for _, s := range pack.BalanceSheet.Sections {
		add("Balance sheet", s.Name, append(s.Lines, s.Total)...)
	}
	bs := pack.BalanceSheet
	add("Balance sheet", "", bs.Earnings, bs.TotalAssets, bs.TotalLiabilities, bs.TotalEquity)
	for _, key := range []string{"book_cash", "receivables", "payables", "current_assets", "current_liabilities", "working_capital"} {
		var l packLine
		if json.Unmarshal(pack.WorkingCapital[key], &l) == nil && l.Name != "" {
			add("Working capital", "", l)
		}
	}
	for _, s := range pack.CashFlow.Sections {
		add("Cash flow", s.Name, append(s.Lines, s.Total)...)
	}
	if pack.CashFlow.NetChange.Name != "" {
		add("Cash flow", "", pack.CashFlow.NetChange, pack.CashFlow.Beginning, pack.CashFlow.Ending)
		tie := "Ties to the books: ending cash is the month-end book cash of " + pack.CashFlow.BookCash + "."
		if !pack.CashFlow.Reconciled {
			tie = "Does not tie to the books. " + strings.Join(pack.CashFlow.Differences, " ")
		}
		rows = append(rows, []string{"Cash flow", "Reconciliation", tie})
	}
	return rows, nil
}
