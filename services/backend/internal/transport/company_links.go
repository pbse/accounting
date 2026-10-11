package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Cross-source identity (docs/books-sources.md, History and takeover; delivery step 7): a client whose months come from
// its files and from a live ledger company is compared across them only once a reviewer confirms both are the same
// company, mapping each of the file's accounts to the ledger's.

// companyLink is the newest confirmation for one ledger company: each file account key to the ledger's account id.
type companyLink struct {
	ID       string            `json:"id"`
	Accounts map[string]string `json:"accounts"`
	Reason   string            `json:"reason"`
	Actor    string            `json:"actor"`
	At       time.Time         `json:"at"`
}

// latestCompanyLink is client name's newest confirmation for ledger company, or nil.
func latestCompanyLink(ctx context.Context, q rowReader, org, name, ledger, company string) (*companyLink, error) {
	var l companyLink
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT id::text,accounts,reason,actor,created_at FROM books_company_links
 WHERE organization_id=$1 AND client_identity(organization_id,client_entity)=client_identity($1,$2) AND ledger=$3 AND company=$4 ORDER BY books_company_links.id DESC LIMIT 1`, org, name, ledger, company).
		Scan(&l.ID, &raw, &l.Reason, &l.Actor, &l.At)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	l.At = l.At.UTC()
	return &l, json.Unmarshal(raw, &l.Accounts)
}

// ledgerCompany is a ledger company a client's months were captured from, with the accounts of its newest month's
// books as they are read (nil when that month's capture cannot be read).
type ledgerCompany struct {
	Ledger  string `json:"ledger"`
	Company string `json:"company"`
	Name    string `json:"name"`
	Period  string `json:"period"`
	closeID string
	books   *books
}

// ledgerCompanies are the ledger companies client name's months were captured from, newest month first, each read
// from its newest month whose books are that company's.
func ledgerCompanies(ctx context.Context, q rowReader, org, name string) ([]ledgerCompany, error) {
	var raw []byte
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('ledger',u.ledger,'company',u.company,'name',u.name,'period',u.period,'close',u.close) ORDER BY u.period DESC,u.ledger,u.company),'[]') FROM (
  SELECT 'quickbooks' AS ledger,s.realm AS company,max(x.company_name) AS name,max(c.period) AS period,(array_agg(c.id::text ORDER BY c.period DESC,c.id DESC))[1] AS close
   FROM quickbooks_snapshots s JOIN client_closes c ON c.id=s.close_id JOIN quickbooks_connections x ON x.id=s.connection_id
   WHERE c.organization_id=$1 AND client_identity(c.organization_id,c.entity)=client_identity($1,$2) AND s.payload ? 'period' GROUP BY s.realm
  UNION ALL SELECT 'xero',s.tenant_id,max(x.tenant_name),max(c.period),(array_agg(c.id::text ORDER BY c.period DESC,c.id DESC))[1]
   FROM xero_snapshots s JOIN client_closes c ON c.id=s.close_id JOIN xero_connections x ON x.id=s.connection_id
   WHERE c.organization_id=$1 AND client_identity(c.organization_id,c.entity)=client_identity($1,$2) GROUP BY s.tenant_id) u`, org, name).Scan(&raw); err != nil {
		return nil, err
	}
	var found []struct{ Ledger, Company, Name, Period, Close string }
	if err := json.Unmarshal(raw, &found); err != nil {
		return nil, err
	}
	companies := []ledgerCompany{}
	for _, f := range found {
		c := ledgerCompany{Ledger: f.Ledger, Company: f.Company, Name: f.Name, Period: f.Period, closeID: f.Close}
		// The month's books as the readers take them, when they are this company's.
		if m, err := periodBooks(ctx, q, f.Close, f.Period); err != nil {
			return nil, err
		} else if m != nil && m.Source == f.Ledger && m.realm == f.Company {
			if c.books, err = recordBooks(ctx, q, m); err != nil {
				c.books = nil
			}
		}
		companies = append(companies, c)
	}
	return companies, nil
}

func registerCompanyLinks(mux *http.ServeMux, db *sql.DB) {
	// The client's ledger companies, each with its confirmed link to the client's files, if any, and a proposal by name.
	mux.HandleFunc("GET /v1/closes/client/company-links", func(w http.ResponseWriter, r *http.Request) {
		name, ok := importOwner(w, r, db)
		if !ok {
			return
		}
		org := organization(r)
		current, err := latestAccountMap(r.Context(), db, org, name)
		var companies []ledgerCompany
		if err == nil {
			companies, err = ledgerCompanies(r.Context(), db, org, name)
		}
		if err != nil {
			problem(w, 503, "The client's companies are unavailable.")
			return
		}
		fileAccounts, names := []map[string]string{}, map[string]string{}
		if current != nil {
			for _, key := range slices.Sorted(maps.Keys(current.Accounts)) {
				a := current.Accounts[key]
				names[key] = strings.TrimSpace(a.Number + " " + a.Name)
				fileAccounts = append(fileAccounts, map[string]string{"key": key, "name": names[key], "class": a.Class})
			}
		}
		out := []map[string]any{}
		for _, c := range companies {
			link, err := latestCompanyLink(r.Context(), db, org, name, c.Ledger, c.Company)
			if err != nil {
				problem(w, 503, "The client's companies are unavailable.")
				return
			}
			entry := map[string]any{"ledger": c.Ledger, "company": c.Company, "name": c.Name, "period": c.Period, "link": link, "accounts": []any{}, "proposal": map[string]string{}}
			if c.books != nil {
				accounts := []map[string]string{}
				for _, id := range slices.Sorted(maps.Keys(c.books.Accounts)) {
					accounts = append(accounts, map[string]string{"id": id, "name": c.books.Accounts[id].Name, "class": c.books.Accounts[id].Class})
				}
				entry["accounts"], entry["proposal"] = accounts, accountsByName(names, c.books.Accounts)
			} else {
				entry["unreadable"] = "The newest month captured from this company cannot be read, so its accounts cannot be mapped yet."
			}
			out = append(out, entry)
		}
		jsonReply(w, 200, map[string]any{"file_accounts": fileAccounts, "companies": out, "can_confirm": canReview(r)})
	})
	// A reviewer confirms a ledger company is the client's files' company, mapping the file accounts the map holds to the
	// company's, each at most once. An account left unmapped (one the ledger no longer has, say) is not compared.
	mux.HandleFunc("POST /v1/closes/client/company-links", func(w http.ResponseWriter, r *http.Request) {
		name, ok := importClient(w, r, db)
		if !ok {
			return
		}
		if !canReview(r) {
			problem(w, 403, "Confirming a company across sources requires workpapers:review.")
			return
		}
		var in struct {
			Ledger   string            `json:"ledger"`
			Company  string            `json:"company"`
			Accounts map[string]string `json:"accounts"`
			Reason   string            `json:"reason"`
			// BasedOn is the link the reviewer saw ("" for none): a confirmation over one made since is refused.
			BasedOn string `json:"based_on"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Reason = strings.TrimSpace(in.Reason); in.Reason == "" || utf8.RuneCountInString(in.Reason) > 500 {
			problem(w, 422, "Say why (1–500 characters).")
			return
		}
		org := organization(r)
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "The link is unavailable.")
			return
		}
		defer tx.Rollback()
		// One confirmation at a time for a client, under the lock its account map is confirmed under, so the map's
		// accounts cannot change beneath it.
		if _, err = tx.ExecContext(r.Context(), `SELECT pg_advisory_xact_lock(hashtext('books_account_maps:'||$1||':'||client_identity($1,$2)))`, org, name); err != nil {
			problem(w, 503, "The link is unavailable.")
			return
		}
		current, err := latestAccountMap(r.Context(), tx, org, name)
		var companies []ledgerCompany
		var link *companyLink
		if err == nil {
			companies, err = ledgerCompanies(r.Context(), tx, org, name)
		}
		if err == nil {
			link, err = latestCompanyLink(r.Context(), tx, org, name, in.Ledger, in.Company)
		}
		if err != nil {
			problem(w, 503, "The link is unavailable.")
			return
		}
		if link == nil && in.BasedOn != "" || link != nil && link.ID != in.BasedOn {
			problem(w, 409, "The company was confirmed again since you opened it: reopen it to see what changed.")
			return
		}
		at := slices.IndexFunc(companies, func(c ledgerCompany) bool { return c.Ledger == in.Ledger && c.Company == in.Company })
		switch {
		case at < 0:
			problem(w, 422, "Choose a company this client's months were captured from.")
			return
		case current == nil:
			problem(w, 409, "Map the client's file accounts first: the link maps them to the ledger's.")
			return
		case companies[at].books == nil:
			problem(w, 409, "The newest month captured from this company cannot be read, so its accounts cannot be mapped yet.")
			return
		}
		used := map[string]bool{}
		for key, id := range in.Accounts {
			if _, ok := current.Accounts[key]; !ok {
				problem(w, 422, "Map only the accounts the client's account map holds.")
				return
			}
			if _, ok := companies[at].books.Accounts[id]; !ok || used[id] {
				problem(w, 422, "Map each account to one of the company's accounts, each at most once.")
				return
			}
			used[id] = true
		}
		accounts, _ := json.Marshal(in.Accounts)
		var id string
		if err = tx.QueryRowContext(r.Context(), `INSERT INTO books_company_links(organization_id,client_entity,ledger,company,accounts,reason,actor) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id::text`,
			org, name, in.Ledger, in.Company, string(accounts), in.Reason, reviewerName(r)).Scan(&id); err != nil || tx.Commit() != nil {
			problem(w, 503, "The link could not be saved.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": id})
	})
}
