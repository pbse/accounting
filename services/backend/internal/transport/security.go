package transport

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"close.local/backend/internal/auth"
)

// securityPeople is each member's latest decision, their recorded client assignments, their latest agent access decision and,
// separately, the agent scope in force (a later request or decline never changes an approval already in force).
// Assignments take effect only while the membership is granted; firm-wide access comes from the WorkOS role.
const securityPeople = `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',m.id::text,'subject',m.subject,'granted',m.granted,'decided_by',m.actor,'decided_at',m.created_at,'reason',m.reason,
 'assignments',COALESCE((SELECT jsonb_agg(g.client_entity ORDER BY lower(g.client_entity)) FROM current_client_access g WHERE g.organization_id=m.organization_id AND g.subject=m.subject AND g.granted),'[]'::jsonb),
 'agent',(SELECT jsonb_build_object('state',a.state,'scope',a.scope,'decided_by',a.actor,'decided_at',a.created_at) FROM current_agent_access a WHERE a.organization_id=m.organization_id AND a.subject=m.subject),
 'agent_scope_in_force',(SELECT e.scope FROM effective_agent_access e WHERE e.organization_id=m.organization_id AND e.subject=m.subject)) ORDER BY m.granted DESC,m.subject),'[]'::jsonb)
 FROM current_organization_members m WHERE m.organization_id=$1`

const securityInvitations = `SELECT COALESCE(jsonb_agg(jsonb_build_object('email',email,'role',role_name,'invited_by',invited_by,'expires_at',expires_at,'created_at',created_at) ORDER BY id DESC),'[]'::jsonb)
 FROM member_invitations WHERE organization_id=$1 AND state IN ('sending','pending')`

// securityServices lists connections that are not disconnected; credentials, OAuth digests and secrets are never selected.
const securityServices = `SELECT COALESCE(jsonb_agg(x ORDER BY x.connected_at DESC),'[]'::jsonb) FROM (
 SELECT 'QuickBooks' AS provider,c.close_id::text AS close_id,c.client_entity AS client,COALESCE(c.company_name,'') AS account,c.environment AS mode,c.state,c.actor AS connected_by,c.created_at AS connected_at,c.confirmed_by,c.confirmed_at FROM quickbooks_connections c WHERE c.organization_id=$1 AND c.state<>'disconnected'
 UNION ALL SELECT 'Xero',c.close_id::text,c.client_entity,COALESCE(c.tenant_name,''),'',c.state,c.actor,c.created_at,c.confirmed_by,c.confirmed_at FROM xero_connections c WHERE c.organization_id=$1 AND c.state<>'disconnected'
 UNION ALL SELECT 'Stripe',c.close_id::text,c.client_entity,COALESCE(c.account_name,''),CASE WHEN c.livemode THEN 'live' ELSE 'test' END,c.state,c.actor,c.created_at,c.confirmed_by,c.confirmed_at FROM stripe_connections c WHERE c.organization_id=$1 AND c.state<>'disconnected'
) x`

const securityEmail = `SELECT COALESCE((SELECT jsonb_build_object('sender',sender,'enabled',enabled,'verified_at',verified_at,'connected_by',actor,'connected_at',created_at) FROM email_connections WHERE organization_id=$1 ORDER BY id DESC LIMIT 1),'null'::jsonb)`

// securityGrants is posting authority posting would honour now: unrevoked, unexpired, granted by a current member, on an
// active connection to the granted company, with where each is revoked (the close that holds that connection).
const securityGrants = `SELECT COALESCE(jsonb_agg(x ORDER BY x.granted_at DESC),'[]'::jsonb) FROM (
 SELECT 'QuickBooks' AS provider,g.client_entity AS client,c.close_id::text AS close_id,g.realm AS company,g.account_ids,g.kinds,g.max_amount::text AS max_amount,g.expires_at,g.reason,g.granted_by,g.granted_at
  FROM production_posting_grants g JOIN LATERAL (SELECT close_id FROM quickbooks_connections c WHERE c.organization_id=g.organization_id AND lower(c.client_entity)=lower(g.client_entity) AND c.environment='production' AND c.state='active'
   AND c.realm=g.realm AND c.company_name=g.client_entity AND (g.connection_id IS NULL OR c.id=g.connection_id) ORDER BY c.id DESC LIMIT 1) c ON true
  WHERE g.organization_id=$1 AND g.revoked_at IS NULL AND g.expires_at>now() AND EXISTS(SELECT 1 FROM current_organization_members m WHERE m.organization_id=g.organization_id AND m.subject=g.granted_by AND m.granted)
 UNION ALL SELECT 'Xero',g.client_entity,c.close_id::text,g.tenant_id,g.account_ids,g.kinds,g.max_amount::text,g.expires_at,g.reason,g.granted_by,g.granted_at
  FROM xero_posting_grants g JOIN xero_connections c ON c.id=g.connection_id AND c.state='active' AND c.tenant_id=g.tenant_id
  WHERE g.organization_id=$1 AND g.revoked_at IS NULL AND g.expires_at>now() AND EXISTS(SELECT 1 FROM current_organization_members m WHERE m.organization_id=g.organization_id AND m.subject=g.granted_by AND m.granted)
) x`

// activityEvents is the firm's ($1) recorded access decisions, connection and grant changes, and records agents made for
// members, as (at, actor, kind, text). It is derived from those durable histories; it is not a complete audit trail. The text
// names clients and quotes reasons, so the staff console masks it until revealed.
const activityEvents = `
 SELECT created_at AS at,actor,CASE WHEN granted THEN 'member.enrolled' ELSE 'member.revoked' END AS kind,CASE WHEN granted THEN 'Enrolled ' ELSE 'Revoked ' END||subject||': '||reason AS text FROM organization_member_access WHERE organization_id=$1
 UNION ALL SELECT created_at,actor,CASE WHEN granted THEN 'client.assigned' ELSE 'client.unassigned' END,CASE WHEN granted THEN 'Assigned ' ELSE 'Unassigned ' END||subject||' to '||client_entity||': '||reason FROM client_access WHERE organization_id=$1
 UNION ALL SELECT created_at,actor,'agent_access.'||state,'Agent access '||state||' ('||scope||') for '||subject||': '||reason FROM agent_access WHERE organization_id=$1
 UNION ALL SELECT created_at,invited_by,'invitation.sent','Invited '||email||' as '||role_name FROM member_invitations WHERE organization_id=$1
 UNION ALL SELECT decided_at,COALESCE(subject,''),'invitation.'||state,'Invitation for '||email||' '||state FROM member_invitations WHERE organization_id=$1 AND decided_at IS NOT NULL
 UNION ALL SELECT created_at,actor,'quickbooks.connected','Connected QuickBooks ('||environment||') for '||client_entity FROM quickbooks_connections WHERE organization_id=$1
 UNION ALL SELECT confirmed_at,confirmed_by,'quickbooks.confirmed','Confirmed QuickBooks for '||client_entity FROM quickbooks_connections WHERE organization_id=$1 AND confirmed_at IS NOT NULL
 UNION ALL SELECT created_at,actor,'xero.connected','Connected Xero for '||client_entity FROM xero_connections WHERE organization_id=$1
 UNION ALL SELECT confirmed_at,confirmed_by,'xero.confirmed','Confirmed Xero for '||client_entity FROM xero_connections WHERE organization_id=$1 AND confirmed_at IS NOT NULL
 UNION ALL SELECT created_at,actor,'stripe.connected','Connected Stripe ('||CASE WHEN livemode THEN 'live' ELSE 'test' END||') for '||client_entity FROM stripe_connections WHERE organization_id=$1
 UNION ALL SELECT created_at,actor,'email.sender','Set email sending from '||sender FROM email_connections WHERE organization_id=$1
 UNION ALL SELECT granted_at,granted_by,'posting.quickbooks_granted','Granted QuickBooks posting for '||client_entity||': '||reason FROM production_posting_grants WHERE organization_id=$1
 UNION ALL SELECT revoked_at,revoked_by,'posting.quickbooks_revoked','Revoked QuickBooks posting for '||client_entity FROM production_posting_grants WHERE organization_id=$1 AND revoked_at IS NOT NULL
 UNION ALL SELECT granted_at,granted_by,'posting.xero_granted','Granted Xero posting for '||client_entity||': '||reason FROM xero_posting_grants WHERE organization_id=$1
 UNION ALL SELECT revoked_at,revoked_by,'posting.xero_revoked','Revoked Xero posting for '||client_entity FROM xero_posting_grants WHERE organization_id=$1 AND revoked_at IS NOT NULL
 UNION ALL SELECT e.created_at,e.created_by,'agent.evidence','Agent '||e.agent||' added evidence '||e.name||' to '||c.entity||' '||c.period FROM evidence e JOIN client_closes c ON c.id=e.close_id WHERE c.organization_id=$1 AND e.agent IS NOT NULL
 UNION ALL SELECT j.created_at,j.created_by,'agent.preparation','Agent '||j.agent||' started preparation #'||j.id||' in '||c.entity||' '||c.period FROM preparation_jobs j JOIN client_closes c ON c.id=j.close_id WHERE c.organization_id=$1 AND j.agent IS NOT NULL
 UNION ALL SELECT p.created_at,p.actor,'agent.prepared','Agent '||p.agent||' prepared '||c.entity||' '||c.period FROM close_preparations p JOIN client_closes c ON c.id=p.close_id WHERE c.organization_id=$1 AND p.agent IS NOT NULL
 UNION ALL SELECT d.created_at,d.created_by,'agent.request_draft','Agent '||d.agent||' drafted a client request in '||c.entity||' '||c.period FROM request_drafts d JOIN client_closes c ON c.id=d.close_id WHERE c.organization_id=$1 AND d.agent IS NOT NULL
 UNION ALL SELECT a.created_at,a.requested_by,'agent.approval_request','Agent '||a.agent||' asked for approval of draft #'||a.workpaper_id||' in '||c.entity||' '||c.period FROM approval_requests a JOIN client_closes c ON c.id=a.close_id WHERE c.organization_id=$1 AND a.agent IS NOT NULL`

// securityActivity is the newest hundred activityEvents for the Security center.
const securityActivity = `SELECT COALESCE(jsonb_agg(x ORDER BY x.at DESC),'[]'::jsonb) FROM (SELECT at,actor,text FROM (` + activityEvents + `
) events ORDER BY at DESC LIMIT 100) x`

func registerSecurity(mux *http.ServeMux, db *sql.DB) {
	// One read of who can do what, which services and agents act for the firm, and what changed; firm administrators only.
	mux.HandleFunc("GET /v1/closes/security", func(w http.ResponseWriter, r *http.Request) {
		if !canManageMembers(r) {
			problem(w, 403, "The security center requires clients:all and members:manage.")
			return
		}
		tx, err := db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			problem(w, 503, "Security center unavailable.")
			return
		}
		defer tx.Rollback()
		org := organization(r)
		out := map[string]any{"organization_id": org}
		for key, query := range map[string]string{"people": securityPeople, "invitations": securityInvitations, "services": securityServices, "email": securityEmail, "posting_grants": securityGrants, "activity": securityActivity,
			"clients": `SELECT COALESCE(jsonb_agg(DISTINCT entity),'[]'::jsonb) FROM client_closes WHERE organization_id=$1`} {
			var raw []byte
			if err = tx.QueryRowContext(r.Context(), query, org).Scan(&raw); err != nil {
				problem(w, 503, "Security center unavailable.")
				return
			}
			out[key] = json.RawMessage(raw)
		}
		var calls, alerts, email int
		var written, firmWritten, investigations bool
		var connected map[string]int
		var raw []byte
		err = tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT sum(calls) FROM model_calls WHERE organization_id=$1 AND day>(now() AT TIME ZONE 'UTC')::date-30),0),
 (SELECT assistant_model_answers FROM organizations WHERE id=$1),
 (SELECT assistant_firm_answers FROM organizations WHERE id=$1),
 (SELECT assistant_investigations FROM organizations WHERE id=$1),
 (SELECT count(*) FROM operations_alert_deliveries WHERE organization_id=$1 AND status='accepted' AND finished_at>now()-interval '30 days'),
 (SELECT count(*) FROM email_connections WHERE organization_id=$1 AND enabled),
 jsonb_build_object('QuickBooks',(SELECT count(*) FROM quickbooks_connections WHERE organization_id=$1 AND state='active'),'Xero',(SELECT count(*) FROM xero_connections WHERE organization_id=$1 AND state='active'),'Stripe',(SELECT count(*) FROM stripe_connections WHERE organization_id=$1 AND state='active'))`, org).Scan(&calls, &written, &firmWritten, &investigations, &alerts, &email, &raw)
		if err != nil || json.Unmarshal(raw, &connected) != nil {
			problem(w, 503, "Security center unavailable.")
			return
		}
		set := func(key string) bool { return os.Getenv(key) != "" }
		answers := "Written assistant answers are off."
		if written {
			answers = "Written assistant answers are on: a close question none of the assistant's fixed reads matches is sent with that close's saved status, sign-off blockers, tie-out, changes and exceptions, and an open draft."
		}
		if investigations {
			answers += " Investigations are on: before writing a close answer the model may read that close's own records through the assistant's read-only tools (documents' text, drafts, tie-out, changes, exceptions, search), each read made with the member's permissions and listed under the answer."
		}
		if firmWritten {
			answers += " Firm-wide written answers are on: a firm-wide question none of the fixed reads matches is sent with the member's lists of what needs a person today, the review queue, blocked work and work handed to the agent, with client names, across the closes the member can open."
		}
		// What each outside service receives, from the code that calls it, and what shows it is in use here.
		out["providers"] = []map[string]any{
			{"provider": "OpenAI", "receives": "Extracted text of the documents the agent reads (their names first, then up to 20 it selects), PDF statement text for row proposals, policy text or reviewer edits for procedure drafts, a Work command with the visible client names, and, when platform staff draft a workflow plan for a close while written answers are on, their instruction with the close's client, month, sign-off state and counts of its preparation work. " + answers + " Card, IBAN and Social Security numbers, labelled bank account numbers and sign-in details are removed from document text and those questions and reads first; file names and Work commands are sent as written. Each request asks OpenAI not to store it.", "evidence": plural(calls, "model call", "model calls") + " reserved in the last 30 days (a budget count, not confirmed delivery)", "in_use": calls > 0},
			{"provider": "Resend", "receives": "Emails a person approved, sent through the firm's own Resend account: client request emails (the recipient, a standard notice from the firm's sender and the private reply link; never the request's questions or the client's figures) and collection reminders to the client's customers (the recipient and the approved subject and message, which by default name the invoice, its amount, its due date and the client, and carry whatever the accountant wrote).", "evidence": map[bool]string{true: "An enabled sending connection", false: "No enabled sending connection"}[email > 0], "in_use": email > 0},
			{"provider": "QuickBooks", "receives": "Read requests for the connected company's ledger; postings only under an approved grant.", "evidence": plural(connected["QuickBooks"], "active connection", "active connections"), "in_use": connected["QuickBooks"] > 0},
			{"provider": "Xero", "receives": "Read requests for the connected organisation's ledger; corrections only under an approved grant.", "evidence": plural(connected["Xero"], "active connection", "active connections"), "in_use": connected["Xero"] > 0},
			{"provider": "Stripe", "receives": "Requests for payout reports of the connected account.", "evidence": plural(connected["Stripe"], "active connection", "active connections"), "in_use": connected["Stripe"] > 0},
			{"provider": "Cloudflare", "receives": "Every request to the hosted site and application, which it runs, including uploaded files. It scans client PDF uploads for malware only once Cloudflare's malware detection and this server's scan settings are configured.", "evidence": map[bool]string{true: "Hosts the application; scan key set on this server", false: "Hosts the application; scan key not set on this server"}[set("UPLOAD_SCAN_KEY")], "in_use": true},
			{"provider": "Text scanner", "receives": "Client TXT and CSV uploads, to scan them for malware.", "evidence": map[bool]string{true: "Scanner address set on this server", false: "Scanner address not set on this server"}[set("TEXT_SCANNER_URL")], "in_use": set("TEXT_SCANNER_URL")},
			{"provider": "WorkOS", "receives": "Sign-in identity and roles. It also holds each person's Google Drive grant, which Close uses to download the files a person imports directly from Google; those grants are managed in WorkOS.", "evidence": map[bool]string{true: "Sign-in configured on this server", false: "Sign-in not configured on this server"}[set("WORKOS_CLIENT_ID")], "in_use": set("WORKOS_CLIENT_ID")},
			{"provider": "Operations webhook", "receives": "Organization, close and item IDs, state and age of stuck work; no client names or amounts. Set on the worker.", "evidence": plural(alerts, "alert", "alerts") + " accepted in the last 30 days", "in_use": alerts > 0},
		}
		var evidence, acceptances []byte
		if err = tx.QueryRowContext(r.Context(), capabilityEvidence, org).Scan(&evidence); err == nil {
			err = tx.QueryRowContext(r.Context(), capabilityAcceptances, org).Scan(&acceptances)
		}
		if err != nil {
			problem(w, 503, "Security center unavailable.")
			return
		}
		if out["operations"], err = capabilityRegister(evidence, out["posting_grants"].(json.RawMessage), out["services"].(json.RawMessage), acceptances); err != nil {
			problem(w, 503, "Security center unavailable.")
			return
		}
		jsonReply(w, 200, out)
	})
	mux.HandleFunc("POST /v1/closes/security/operations", func(w http.ResponseWriter, r *http.Request) {
		if !canManageMembers(r) {
			problem(w, 403, "Recording connector checks requires clients:all and members:manage.")
			return
		}
		var in struct {
			Provider string `json:"provider"`
			Kind     string `json:"kind"`
			Stage    string `json:"stage"`
			Accepted bool   `json:"accepted"`
			Evidence string `json:"evidence"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Evidence = strings.TrimSpace(in.Evidence)
		switch {
		case !slices.ContainsFunc(capabilities, func(c capability) bool { return c.provider == in.Provider && c.kind == in.Kind }):
			problem(w, 422, "Choose a listed connector operation.")
			return
		case in.Stage != "sandbox_checked" && in.Stage != "accountant_accepted":
			problem(w, 422, "Record a sandbox check or the accountant's acceptance.")
			return
		case utf8.RuneCountInString(in.Evidence) < 5 || utf8.RuneCountInString(in.Evidence) > 2000:
			problem(w, 422, "Give the evidence: what was checked, where, and its result or reference (5–2,000 characters).")
			return
		case in.Stage == "accountant_accepted" && !slices.Contains(auth.Actor(r.Context()).Permissions, "workpapers:review"):
			problem(w, 403, "Only an accountant who reviews workpapers can accept an operation.")
			return
		}
		if _, err := db.ExecContext(r.Context(), `INSERT INTO operation_acceptances(organization_id,provider,kind,stage,accepted,evidence,actor) VALUES($1,$2,$3,$4,$5,$6,$7)`, organization(r), in.Provider, in.Kind, in.Stage, in.Accepted, in.Evidence, subject(r)); err != nil {
			problem(w, 503, "The record could not be saved.")
			return
		}
		jsonReply(w, 200, map[string]bool{"saved": true})
	})
}
