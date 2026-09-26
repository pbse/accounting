package transport

import (
	"database/sql"
	"net/http"
	"slices"
	"strings"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/quickbooks"
	"close.local/backend/internal/xero"
)

// plainPermissions says what each permission the application checks lets a member do.
var plainPermissions = []struct{ name, plain string }{
	{"clients:all", "See every client in the firm"},
	{"workpapers:review", "Review and approve drafts, and start or stop preparation"},
	{"procedures:manage", "Approve the firm's and clients' accounting procedures"},
	{"requests:send", "Send client requests"},
	{"close:signoff", "Sign off a close"},
	{"integrations:manage", "Connect QuickBooks, Xero, Stripe and email"},
	{"posting:approve", "Approve journals and corrections for posting"},
	{"posting:execute", "Post approved journals and corrections"},
	{"members:manage", "Enroll or remove members and assign clients"},
	{"data:manage", "Set retention and delete closes"},
	{"operations:read", "See firm operations"},
	{"org:manage", "Suspend the firm"},
}

type setupStep struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	Done   bool   `json:"done"`
	Detail string `json:"detail"`
	Href   string `json:"href"`
}

type setupReconnect struct {
	Provider string `json:"provider"`
	Client   string `json:"client"`
	Error    string `json:"error"`
	Href     string `json:"href"`
}

// registerSetup serves the first-client journey. Progress is read from the firm's own records, so it is saved by
// doing the work; nothing here is stored.
func registerSetup(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/setup", func(w http.ResponseWriter, r *http.Request) {
		org, me, all := organization(r), subject(r), firmAccess(r)
		// access is the member's client scope for a row with organization_id and client entity columns.
		access := func(entity string) string {
			return `($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=$1 AND g.subject=$3 AND lower(g.client_entity)=lower(` + entity + `) AND g.granted))`
		}
		tx, err := db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
		if err != nil {
			problem(w, 503, "Setup is unavailable.")
			return
		}
		defer tx.Rollback()
		var firm, closeID, entity, period, paper, paperClose string
		var teammates, closes int
		var ledger bool
		err = tx.QueryRowContext(r.Context(), `SELECT o.name,(SELECT count(*) FROM current_organization_members m WHERE m.organization_id=o.id AND m.granted AND m.subject<>$3),
 (SELECT count(*) FROM client_closes c WHERE c.organization_id=o.id AND `+access("c.entity")+`)
 FROM organizations o WHERE o.id=$1`, org, all, me).Scan(&firm, &teammates, &closes)
		if err == nil {
			if err = tx.QueryRowContext(r.Context(), `SELECT id::text,entity,period FROM client_closes c WHERE c.organization_id=$1 AND `+access("c.entity")+` ORDER BY period DESC,c.id DESC LIMIT 1`, org, all, me).Scan(&closeID, &entity, &period); err == sql.ErrNoRows {
				err = nil
			}
		}
		if err == nil {
			// QuickBooks counts only through the connection its captures read, which prefers production.
			err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM quickbooks_connections WHERE id=`+quickbooks.ReadingConnection("NULLIF($3,'')::bigint")+` AND state='active') OR EXISTS(SELECT 1 FROM xero_connections WHERE organization_id=$1 AND lower(client_entity)=lower($2) AND state='active')`, org, entity, closeID).Scan(&ledger)
		}
		if err == nil {
			if err = tx.QueryRowContext(r.Context(), `SELECT w.id::text,w.close_id::text FROM workpapers w JOIN client_closes c ON c.id=w.close_id WHERE c.organization_id=$1 AND `+access("c.entity")+` ORDER BY w.id DESC LIMIT 1`, org, all, me).Scan(&paper, &paperClose); err == sql.ErrNoRows {
				err = nil
			}
		}
		rules := []string{}
		for _, kind := range []string{"prepaid", "accrual", "reconciliation", "loan", "basis", "owner"} {
			if closeID == "" || err != nil {
				break
			}
			var p preparation.Procedure
			if p, err = preparation.CurrentProcedure(r.Context(), tx, closeID, kind); err == sql.ErrNoRows {
				err = nil
			} else if err == nil && p.Enabled {
				rules = append(rules, kind)
			}
		}
		reconnect := []setupReconnect{}
		if err == nil {
			var rows *sql.Rows
			rows, err = tx.QueryContext(r.Context(), `SELECT provider,environment,client_entity,error,COALESCE((SELECT id::text FROM client_closes c WHERE c.organization_id=$1 AND lower(c.entity)=lower(x.client_entity) ORDER BY period DESC,c.id DESC LIMIT 1),'') FROM (
 SELECT 'QuickBooks' provider,environment,client_entity,error FROM quickbooks_connections WHERE organization_id=$1 AND state='reconnect_required'
 UNION ALL SELECT 'Xero','',client_entity,error FROM xero_connections WHERE organization_id=$1 AND state='reconnect_required') x
 WHERE `+access("x.client_entity")+` ORDER BY client_entity LIMIT 50`, org, all, me)
			for err == nil && rows.Next() {
				var item setupReconnect
				var environment, close string
				if err = rows.Scan(&item.Provider, &environment, &item.Client, &item.Error, &close); err != nil {
					break
				}
				// Production QuickBooks is authorized on the close's Settings tab; sandbox and Xero on Evidence.
				item.Href = "/dashboard/closes"
				if close != "" {
					item.Href = "/dashboard/closes/" + close + map[string]string{"production": "?tab=settings#quickbooks-production", "sandbox": "?tab=evidence#quickbooks-ledger", "": "?tab=evidence#xero"}[environment]
				}
				reconnect = append(reconnect, item)
			}
			if rows != nil {
				if err == nil {
					err = rows.Err()
				}
				rows.Close()
			}
		}
		if err != nil {
			problem(w, 503, "Setup is unavailable.")
			return
		}
		label := entity + " · " + period
		base := "/dashboard/closes/" + closeID
		steps := []setupStep{
			{"firm", "Enroll the firm", true, firm, "/dashboard/firm"},
			{"team", "Add a team member", teammates > 0, plural(teammates, "teammate", "teammates") + " besides you. A firm administrator invites members by email under Firm operations and membership.", "/dashboard/firm"},
			{"client", "Open a client close", closes > 0, plural(closes, "client close", "client closes") + " you can open.", "/dashboard/closes"},
		}
		switch {
		case closeID == "":
			steps = append(steps, setupStep{"ledger", "Connect the client's books", false, "Open a client close first.", "/dashboard/closes"}, setupStep{"rules", "Confirm the period and rules", false, "Open a client close first.", "/dashboard/closes"})
		default:
			books := "Connect QuickBooks (production on the close's Settings tab, sandbox on Evidence) or Xero (Evidence) for " + entity + "."
			if ledger {
				books = entity + "'s books are connected."
			} else if !quickbooks.OAuthConfiguredFor("production") && !quickbooks.OAuthConfigured() && !xero.Configured() {
				books = "QuickBooks and Xero are not configured on this server; upload " + entity + "'s ledger reports as evidence instead."
			}
			applied := "No approved procedure applies to " + entity + " yet; approve one or apply the firm's."
			if len(rules) > 0 {
				applied = "Approved procedures apply to " + entity + " for " + strings.Join(rules, ", ") + "."
			}
			steps = append(steps, setupStep{"ledger", "Connect the client's books", ledger, books, base + "?tab=evidence"}, setupStep{"rules", "Confirm the period and rules", len(rules) > 0, label + ". " + applied, base + "?tab=settings"})
		}
		switch {
		case paper != "":
			steps = append(steps, setupStep{"prepared", "Receive the first prepared draft", true, "Draft #" + paper + " is ready for review.", "/dashboard/closes/" + paperClose + "/workpapers/" + paper})
		case closeID != "":
			steps = append(steps, setupStep{"prepared", "Receive the first prepared draft", false, "Upload evidence to " + label + ", then confirm the proposed facts in Work.", base + "?tab=evidence"})
		default:
			steps = append(steps, setupStep{"prepared", "Receive the first prepared draft", false, "Open a client close first.", "/dashboard/closes"})
		}
		var granted []string
		if a := auth.Actor(r.Context()); a != nil && a.Organization != "" {
			granted = a.Permissions
		}
		you := []string{}
		for _, p := range plainPermissions {
			if a := auth.Actor(r.Context()); a == nil || a.Organization == "" || slices.Contains(granted, p.name) {
				you = append(you, p.plain)
			} else if p.name == "clients:all" {
				you = append(you, "See only the clients assigned to you")
			}
		}
		jsonReply(w, 200, map[string]any{"steps": steps, "permissions": you, "reconnect": reconnect, "can_reconnect": slices.Contains(granted, "integrations:manage")})
	})
}
