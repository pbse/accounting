package transport

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/quickbooks"
	"close.local/backend/internal/xero"
)

// capabilityEvidence is each connector operation's latest verified write or successful read in this firm, split by
// whether it reached a sandbox or a live company, with the close that holds it. A QuickBooks correction, supplier credit
// or credit application is live when it was made under a grant; Xero does not mark demo companies, so every Xero record
// counts as live.
const capabilityEvidence = `SELECT COALESCE(jsonb_agg(x),'[]'::jsonb) FROM (SELECT provider,kind,max(at) FILTER (WHERE NOT live) AS sandbox,max(at) FILTER (WHERE live) AS live,
 (array_agg(close_id ORDER BY at DESC) FILTER (WHERE NOT live))[1] AS sandbox_close,(array_agg(close_id ORDER BY at DESC) FILTER (WHERE live))[1] AS live_close FROM (
 SELECT 'QuickBooks' AS provider,w.kind,false AS live,p.verified_at AS at,p.close_id FROM sandbox_postings p JOIN workpapers w ON w.id=p.workpaper_id JOIN client_closes c ON c.id=p.close_id WHERE c.organization_id=$1 AND p.state='verified'
 UNION ALL SELECT 'QuickBooks',w.kind,true,p.verified_at,p.close_id FROM production_postings p JOIN workpapers w ON w.id=p.workpaper_id JOIN client_closes c ON c.id=p.close_id WHERE c.organization_id=$1 AND p.state='verified'
 UNION ALL SELECT 'QuickBooks',CASE b.entity WHEN 'Purchase' THEN 'purchase_correction' WHEN 'Deposit' THEN 'deposit_correction' ELSE 'bill_correction' END,b.grant_id IS NOT NULL,b.verified_at,b.close_id FROM bill_corrections b JOIN client_closes c ON c.id=b.close_id WHERE c.organization_id=$1 AND b.state='verified'
 UNION ALL SELECT 'QuickBooks','vendor_credit',v.grant_id IS NOT NULL,v.verified_at,v.close_id FROM vendor_credit_postings v JOIN client_closes c ON c.id=v.close_id WHERE c.organization_id=$1 AND v.state='verified'
 UNION ALL SELECT 'QuickBooks',CASE a.kind WHEN 'payment' THEN 'payment_application' ELSE 'credit_application' END,a.grant_id IS NOT NULL,a.verified_at,a.close_id FROM credit_applications a JOIN client_closes c ON c.id=a.close_id WHERE c.organization_id=$1 AND a.state='verified'
 UNION ALL SELECT 'Xero',w.kind,true,p.verified_at,p.close_id FROM xero_postings p JOIN workpapers w ON w.id=p.workpaper_id JOIN client_closes c ON c.id=p.close_id WHERE c.organization_id=$1 AND p.state='verified'
 UNION ALL SELECT 'Xero','bill_correction',true,x.verified_at,x.close_id FROM xero_corrections x JOIN client_closes c ON c.id=x.close_id WHERE c.organization_id=$1 AND x.state='verified'
 UNION ALL SELECT 'QuickBooks','read',q.environment='production',s.captured_at,s.close_id FROM quickbooks_snapshots s JOIN quickbooks_connections q ON q.id=s.connection_id WHERE q.organization_id=$1
 UNION ALL SELECT 'QuickBooks','read',q.environment='production',s.finished_at,s.close_id FROM quickbooks_entity_syncs s JOIN quickbooks_connections q ON q.id=s.connection_id WHERE q.organization_id=$1 AND s.state IN ('completed','unchanged')
 UNION ALL SELECT 'Xero','read',true,s.captured_at,s.close_id FROM xero_snapshots s JOIN xero_connections x ON x.id=s.connection_id WHERE x.organization_id=$1
 UNION ALL SELECT 'Stripe','read',k.livemode,r.finished_at,r.close_id FROM stripe_report_runs r JOIN stripe_connections k ON k.id=r.connection_id WHERE k.organization_id=$1 AND r.state='saved'
) e GROUP BY provider,kind) x`

const capabilityAcceptances = `SELECT COALESCE(jsonb_agg(x),'[]'::jsonb) FROM (SELECT DISTINCT ON (provider,kind,stage) provider,kind,stage,accepted,evidence,actor,created_at AS at
 FROM operation_acceptances WHERE organization_id=$1 ORDER BY provider,kind,stage,id DESC) x`

// capability is one connector operation: a posting kind a grant can cover, or a provider's reads ("read").
type capability struct{ provider, kind, operation, limitation string }

var capabilities = []capability{
	{"QuickBooks", "read", "Reports and transactions", "A report is QuickBooks' cut when it was read, not an atomic copy of the ledger."},
	{"QuickBooks", "prepaid", "Journal: prepaid adjustment", "One entry dated at the close month-end; account balances are read before and after."},
	{"QuickBooks", "accrual", "Journal: accrual", "Only the approved accrual's lines; a reversal is its own approved entry."},
	{"QuickBooks", "loan", "Journal: loan interest and current portion", "From a schedule checked against the lender statement."},
	{"QuickBooks", "owner", "Journal: owner item", "Only under the client's approved owner rule; tax treatment is never decided."},
	{"QuickBooks", "payroll", "Journal: payroll from the provider's summary", "Only a summary tied to the clearing account's debits; payroll is never run."},
	{"QuickBooks", "settlement", "Journal: Stripe fees", "For payouts deposited in the month when the client books deposits net."},
	{"QuickBooks", "bill_correction", "Bill line recode", "One line of an unpaid Bill; both accounts must be in the grant."},
	{"QuickBooks", "purchase_correction", "Expense line recode", "Up to ten simple expense lines, no tax, nothing linked."},
	{"QuickBooks", "deposit_correction", "Income deposit line recode", "One deposit line, under the accountant's cited coding rule."},
	{"QuickBooks", "vendor_credit", "Supplier credit", "A reviewed current-period credit with the exact vendor and accounts."},
	{"QuickBooks", "payment_application", "Apply a customer payment", "Only a pairing the latest capture supports exactly."},
	{"QuickBooks", "credit_application", "Apply a vendor credit", "Only a pairing the latest capture supports exactly."},
	{"Xero", "read", "Trial Balance and transactions", "Xero's reconciled flags are read beside workpapers, never written."},
	{"Xero", "prepaid", "Manual journal: prepaid adjustment", "Dated after the organisation's lock dates, to accounts the grant lists."},
	{"Xero", "accrual", "Manual journal: accrual", "Dated after the organisation's lock dates, to accounts the grant lists."},
	{"Xero", "loan", "Manual journal: loan", "Dated after the organisation's lock dates, to accounts the grant lists."},
	{"Xero", "owner", "Manual journal: owner item", "Dated after the organisation's lock dates, to accounts the grant lists."},
	{"Xero", "payroll", "Manual journal: payroll", "Dated after the organisation's lock dates, to accounts the grant lists."},
	{"Xero", "settlement", "Manual journal: Stripe fees", "Dated after the organisation's lock dates, to accounts the grant lists."},
	{"Xero", "bill_correction", "Bill line recode", "One bill line moved to another expense account the grant lists."},
	{"Stripe", "read", "Payout and balance reports", "The balance roll-forward is computed, not tied to Stripe's own balances."},
}

// capabilityRegister says how far each connector operation is proven and enabled here, in order: documented, sandbox
// tested (a verified sandbox record exists), production approved (this server allows live use), or enabled for the
// clients whose grant in force (writes) or active connection (reads) covers it. Journal kinds are checked in a
// QuickBooks sandbox, and Xero operations on a demo company, outside the app, so no sandbox date is recorded for them.
func capabilityRegister(evidence, grants, services, acceptances json.RawMessage) ([]map[string]any, error) {
	var found []struct {
		Provider, Kind string
		Sandbox, Live  *time.Time
		SandboxClose   *int64 `json:"sandbox_close"`
		LiveClose      *int64 `json:"live_close"`
	}
	var granted []struct {
		Provider, Client string
		Kinds            []string
	}
	var connected []struct{ Provider, Client, Mode, State string }
	var recorded []struct {
		Provider, Kind, Stage, Evidence, Actor string
		Accepted                               bool
		At                                     time.Time
	}
	if err := json.Unmarshal(evidence, &found); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(acceptances, &recorded); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(grants, &granted); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(services, &connected); err != nil {
		return nil, err
	}
	rows := []map[string]any{}
	for _, c := range capabilities {
		var sandbox, live *time.Time
		var sandboxClose, liveClose *int64
		for _, f := range found {
			if f.Provider == c.provider && f.Kind == c.kind {
				sandbox, live, sandboxClose, liveClose = f.Sandbox, f.Live, f.SandboxClose, f.LiveClose
			}
		}
		row := map[string]any{"provider": c.provider, "kind": c.kind, "operation": c.operation, "limitation": c.limitation, "sandbox_verified_at": sandbox, "live_verified_at": live, "sandbox_close_id": sandboxClose, "live_close_id": liveClose,
			"sandbox_checked": nil, "accountant_accepted": nil}
		for _, a := range recorded {
			if a.Provider == c.provider && a.Kind == c.kind {
				row[a.Stage] = map[string]any{"accepted": a.Accepted, "evidence": a.Evidence, "by": a.Actor, "at": a.At}
			}
		}
		clients := []string{}
		add := func(client string) {
			if !slices.ContainsFunc(clients, func(k string) bool { return strings.EqualFold(k, client) }) {
				clients = append(clients, client)
			}
		}
		var approved bool
		if c.kind == "read" {
			approved = c.provider == "Stripe" || (c.provider == "Xero" && xero.Configured()) || (c.provider == "QuickBooks" && quickbooks.OAuthConfiguredFor("production"))
			row["permission"] = "An active, confirmed connection for the client"
			if c.provider == "Xero" {
				row["permission"] = "An active, confirmed connection for the client; transactions also need the transaction scopes, which a connection made before they were requested lacks until it reconnects"
			}
			row["owner"] = map[string]string{"QuickBooks": "Server operator: the production QuickBooks app", "Xero": "Server operator: the Xero app", "Stripe": "The firm: a restricted key per client"}[c.provider]
			row["fallback"] = "Upload the " + map[string]string{"QuickBooks": "ledger reports", "Xero": "Trial Balance and reports", "Stripe": "itemized Stripe reports"}[c.provider] + " as evidence"
			for _, s := range connected {
				if s.Provider == c.provider && s.State == "active" && (c.provider == "Xero" || s.Mode == map[string]string{"QuickBooks": "production", "Stripe": "live"}[c.provider]) {
					add(s.Client)
				}
			}
		} else {
			kinds, setting := postingKinds(), "PRODUCTION_POSTING_KINDS"
			if c.provider == "Xero" {
				kinds, setting = xeroPostingKinds(), "XERO_POSTING_KINDS"
			}
			approved = slices.Contains(kinds, c.kind)
			row["permission"] = "A posting grant for the client that covers " + c.kind
			if c.provider == "Xero" {
				scope := "post journals"
				if c.kind == "bill_correction" {
					scope = "write invoices"
				}
				row["permission"] = "A posting grant for the client that covers " + c.kind + ", on a connection authorized to " + scope + " (one made before that scope was requested must reconnect)"
			}
			row["owner"] = "Server operator: " + setting
			row["fallback"] = "A person books the approved draft in " + c.provider
			for _, g := range granted {
				if g.Provider == c.provider && slices.Contains(g.Kinds, c.kind) {
					add(g.Client)
				}
			}
		}
		row["sandbox_recorded"] = c.provider != "Xero" && !slices.Contains([]string{"accrual", "loan", "owner", "payroll", "settlement"}, c.kind)
		status := "documented"
		switch {
		case approved && len(clients) > 0:
			status = "enabled"
		case approved:
			status = "production approved"
		case sandbox != nil:
			status = "sandbox tested"
		}
		if !approved {
			clients = []string{}
		}
		slices.Sort(clients)
		row["status"], row["clients"] = status, clients
		rows = append(rows, row)
	}
	return rows, nil
}
