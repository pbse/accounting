package transport

import (
	"context"
	"database/sql"
	"net/http"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
)

type retentionStatus struct {
	RetainUntil string   `json:"retain_until"`
	LegalHold   bool     `json:"legal_hold"`
	Version     int      `json:"version"`
	Blockers    []string `json:"deletion_blockers"`
}

func canManageData(r *http.Request) bool {
	a := auth.Actor(r.Context())
	return firmAccess(r) && a != nil && (a.Organization == "" || slices.Contains(a.Permissions, "data:manage"))
}

func readRetention(ctx context.Context, tx *sql.Tx, closeID, org string) (retentionStatus, error) {
	out := retentionStatus{Blockers: []string{}}
	var due, jobs, syncs, postings, mail, links, attachments, forwarding, connection, grants, shared, xeroConnection, xeroShared, stripeConnection, stripeShared, unmapped bool
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(c.retain_until::text,''),c.legal_hold,c.retention_version,COALESCE(c.retain_until<=CURRENT_DATE,false),
 EXISTS(SELECT 1 FROM preparation_jobs j WHERE j.close_id=c.id AND j.state IN ('queued','waiting_evidence','interpreting','needs_confirmation','waiting_client')),
 EXISTS(SELECT 1 FROM quickbooks_entity_syncs s WHERE s.close_id=c.id AND s.state IN ('queued','running')),
 EXISTS(SELECT 1 FROM sandbox_postings p WHERE p.close_id=c.id AND p.state IN ('executing','unknown')) OR EXISTS(SELECT 1 FROM vendor_credit_postings p WHERE p.close_id=c.id AND p.state IN ('executing','unknown')) OR EXISTS(SELECT 1 FROM credit_applications p WHERE p.close_id=c.id AND p.state IN ('executing','unknown')) OR EXISTS(SELECT 1 FROM bill_corrections p WHERE p.close_id=c.id AND p.state IN ('executing','unknown')) OR EXISTS(SELECT 1 FROM xero_corrections p WHERE p.close_id=c.id AND p.state IN ('executing','unknown')) OR EXISTS(SELECT 1 FROM production_postings p WHERE p.close_id=c.id AND p.state IN ('executing','unknown')) OR EXISTS(SELECT 1 FROM xero_postings p WHERE p.close_id=c.id AND p.state IN ('executing','unknown')),
 EXISTS(SELECT 1 FROM email_outbox m LEFT JOIN work_requests q ON q.id=m.request_id LEFT JOIN request_drafts d ON d.id=q.draft_id LEFT JOIN collection_messages cm ON cm.id=m.collection_id WHERE COALESCE(d.close_id,cm.close_id)=c.id AND m.state IN ('queued','sending','unknown')),
 EXISTS(SELECT 1 FROM client_request_links l JOIN work_requests q ON q.id=l.request_id JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id=c.id AND l.used_at IS NULL AND l.revoked_at IS NULL AND l.expires_at>now()),
 EXISTS(SELECT 1 FROM client_attachments a WHERE a.close_id=c.id AND a.state NOT IN ('released','rejected')),
 EXISTS(SELECT 1 FROM forwarding_links l WHERE l.close_id=c.id AND l.revoked_at IS NULL AND l.expires_at>now()) OR EXISTS(SELECT 1 FROM forwarded_files f WHERE f.close_id=c.id AND f.state NOT IN ('released','rejected')),
 EXISTS(SELECT 1 FROM quickbooks_connections x WHERE x.close_id=c.id AND x.state<>'disconnected'),
 EXISTS(SELECT 1 FROM production_posting_grants g JOIN quickbooks_connections x ON x.id=g.connection_id WHERE x.close_id=c.id AND g.revoked_at IS NULL) OR EXISTS(SELECT 1 FROM xero_posting_grants g JOIN xero_connections x ON x.id=g.connection_id WHERE x.close_id=c.id AND g.revoked_at IS NULL),
 EXISTS(SELECT 1 FROM quickbooks_connections x WHERE x.close_id=c.id AND (EXISTS(SELECT 1 FROM quickbooks_snapshots s WHERE s.connection_id=x.id AND s.close_id<>c.id) OR EXISTS(SELECT 1 FROM quickbooks_entity_syncs q WHERE q.connection_id=x.id AND q.close_id<>c.id) OR EXISTS(SELECT 1 FROM production_postings p WHERE p.connection_id=x.id AND p.close_id<>c.id))),
 EXISTS(SELECT 1 FROM xero_connections x WHERE x.close_id=c.id AND x.state<>'disconnected'),
 EXISTS(SELECT 1 FROM xero_connections x WHERE x.close_id=c.id AND (EXISTS(SELECT 1 FROM xero_snapshots s WHERE s.connection_id=x.id AND s.close_id<>c.id) OR EXISTS(SELECT 1 FROM xero_postings p WHERE p.connection_id=x.id AND p.close_id<>c.id) OR EXISTS(SELECT 1 FROM xero_corrections p WHERE p.connection_id=x.id AND p.close_id<>c.id))),
 EXISTS(SELECT 1 FROM stripe_connections x WHERE x.close_id=c.id AND x.state<>'disconnected') OR EXISTS(SELECT 1 FROM stripe_report_runs s WHERE s.close_id=c.id AND s.state IN ('requesting','pending')),
 EXISTS(SELECT 1 FROM stripe_connections x JOIN stripe_report_runs s ON s.connection_id=x.id WHERE x.close_id=c.id AND s.close_id<>c.id),
 EXISTS(SELECT 1 FROM email_delivery_events e JOIN email_outbox m ON m.connection_id=e.connection_id AND m.provider_id=e.provider_id
  LEFT JOIN work_requests q ON q.id=m.request_id LEFT JOIN request_drafts d ON d.id=q.draft_id LEFT JOIN collection_messages cm ON cm.id=m.collection_id
  WHERE lower(COALESCE(q.recipient,cm.recipient))=lower(e.recipient) AND COALESCE(d.close_id,cm.close_id)=c.id AND e.close_id IS NULL)
 FROM client_closes c WHERE c.id::text=$1 AND c.organization_id=$2`, closeID, org).Scan(&out.RetainUntil, &out.LegalHold, &out.Version, &due, &jobs, &syncs, &postings, &mail, &links, &attachments, &forwarding, &connection, &grants, &shared, &xeroConnection, &xeroShared, &stripeConnection, &stripeShared, &unmapped)
	if err != nil {
		return out, err
	}
	var activeRule bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM procedure_drafts d JOIN procedure_versions p ON p.id=d.activated_version_id
 WHERE d.organization_id=$2 AND p.enabled AND p.id=(
  SELECT max(v.id) FROM procedure_versions v
  WHERE v.organization_id=p.organization_id AND lower(v.client_entity)=lower(p.client_entity) AND v.kind=p.kind
 ) AND (
  EXISTS(SELECT 1 FROM evidence e WHERE e.close_id::text=$1 AND e.id=d.source_evidence_id)
  OR EXISTS(SELECT 1 FROM procedure_example_approvals a JOIN workpapers w ON w.id=a.workpaper_id WHERE a.draft_id=d.id AND w.close_id::text=$1)
  OR EXISTS(SELECT 1 FROM workpapers w WHERE w.close_id::text=$1 AND d.source_draft_ids ? w.id::text)
 ))`, closeID, org).Scan(&activeRule)
	if err != nil {
		return out, err
	}
	// An opening month whose later months still wait for its baseline or inherited cleanup keeps that record.
	var opening bool
	if err = tx.QueryRowContext(ctx, `SELECT c.takeover AND EXISTS(SELECT 1 FROM client_closes d WHERE d.organization_id=c.organization_id AND d.period>c.period AND client_identity(d.organization_id,d.entity)=client_identity(c.organization_id,c.entity))
 AND (NOT EXISTS(SELECT 1 FROM opening_baselines b WHERE b.close_id=c.id) OR `+openCleanup+`>0) FROM client_closes c WHERE c.id::text=$1`, closeID).Scan(&opening); err != nil {
		return out, err
	}
	if opening {
		out.Blockers = append(out.Blockers, "Later months of this client wait for its opening baseline or inherited cleanup here; review and resolve it before deleting this close.")
	}
	// A connection made on the client's page belongs to no month. While the client keeps a roster entry or another month
	// its page stays reachable; deleting the last of them would leave such a connection active with no way to it.
	var stranded bool
	if err = tx.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM client_closes d WHERE d.organization_id=c.organization_id AND d.id<>c.id AND client_identity(d.organization_id,d.entity)=client_identity(c.organization_id,c.entity))
 AND NOT EXISTS(SELECT 1 FROM current_client_roster r WHERE r.organization_id=c.organization_id AND client_identity(r.organization_id,r.client_entity)=client_identity(c.organization_id,c.entity))
 AND (EXISTS(SELECT 1 FROM quickbooks_connections x WHERE x.organization_id=c.organization_id AND x.close_id IS NULL AND client_identity(x.organization_id,x.client_entity)=client_identity(c.organization_id,c.entity)
   AND (x.state<>'disconnected' OR EXISTS(SELECT 1 FROM production_posting_grants g WHERE g.connection_id=x.id AND g.revoked_at IS NULL)))
  OR EXISTS(SELECT 1 FROM xero_connections x WHERE x.organization_id=c.organization_id AND x.close_id IS NULL AND client_identity(x.organization_id,x.client_entity)=client_identity(c.organization_id,c.entity)
   AND (x.state<>'disconnected' OR EXISTS(SELECT 1 FROM xero_posting_grants g WHERE g.connection_id=x.id AND g.revoked_at IS NULL))))
 FROM client_closes c WHERE c.id::text=$1`, closeID).Scan(&stranded); err != nil {
		return out, err
	}
	// Files sent for the client's other months would be reachable only through this, its last month, too.
	var strandedFiles bool
	if err = tx.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM client_closes d WHERE d.organization_id=c.organization_id AND d.id<>c.id AND client_identity(d.organization_id,d.entity)=client_identity(c.organization_id,c.entity))
 AND NOT EXISTS(SELECT 1 FROM current_client_roster r WHERE r.organization_id=c.organization_id AND client_identity(r.organization_id,r.client_entity)=client_identity(c.organization_id,c.entity))
 AND EXISTS(SELECT 1 FROM books_imports i WHERE i.organization_id=c.organization_id AND client_identity(i.organization_id,i.client_entity)=client_identity(c.organization_id,c.entity) AND i.period<>c.period)
 FROM client_closes c WHERE c.id::text=$1`, closeID).Scan(&strandedFiles); err != nil {
		return out, err
	}
	if stranded {
		out.Blockers = append(out.Blockers, "This is the client's last month and it is not on the roster: disconnect its ledgers and revoke their posting grants on the client's page before deleting it.")
	}
	if strandedFiles {
		out.Blockers = append(out.Blockers, "This is the client's last month and it is not on the roster: delete the files it sent for other months on the client's page before deleting it.")
	}
	if out.RetainUntil == "" {
		out.Blockers = append(out.Blockers, "Set an approved retention date for this close.")
	} else if !due {
		out.Blockers = append(out.Blockers, "The approved retention date has not arrived.")
	}
	if out.LegalHold {
		out.Blockers = append(out.Blockers, "A legal hold protects this close.")
	}
	for _, condition := range []struct {
		active  bool
		message string
	}{
		{jobs, "Preparation or client follow-up work remains active."},
		{syncs, "A QuickBooks transaction refresh remains active."},
		{postings, "A posting attempt needs canonical resolution."},
		{mail, "Email delivery has an in-flight or unknown outcome."},
		{links, "An active client request link must expire or be revoked."},
		{attachments, "A client attachment still needs scanning or disposition."},
		{forwarding, "A forwarded evidence address or attachment remains active."},
		{connection, "Disconnect QuickBooks connections opened from this close."},
		{grants, "Revoke production posting grants opened from this close."},
		{shared, "A QuickBooks connection opened here is used by another close."},
		{xeroConnection, "Disconnect the Xero connection opened from this close."},
		{xeroShared, "A Xero connection opened here is used by another close."},
		{stripeConnection, "Disconnect the Stripe connection opened from this close, and let any report in progress finish."},
		{stripeShared, "A Stripe connection opened here is used by another close."},
		{unmapped, "Historical email delivery events need close attribution before any close in this firm can be deleted."},
		{activeRule, "An active procedure rule depends on this close. Publish a disabled version after accountant review before deleting its source."},
	} {
		if condition.active {
			out.Blockers = append(out.Blockers, condition.message)
		}
	}
	return out, nil
}

func registerRetention(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/{id}/retention", func(w http.ResponseWriter, r *http.Request) {
		if !canManageData(r) {
			problem(w, 403, "Firm data administration requires clients:all and data:manage.")
			return
		}
		tx, err := db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			problem(w, 503, "Retention state unavailable.")
			return
		}
		defer tx.Rollback()
		status, err := readRetention(r.Context(), tx, r.PathValue("id"), organization(r))
		if err != nil {
			problem(w, 503, "Retention state unavailable.")
			return
		}
		jsonReply(w, 200, status)
	})
	mux.HandleFunc("POST /v1/closes/{id}/retention", func(w http.ResponseWriter, r *http.Request) {
		if !canManageData(r) {
			problem(w, 403, "Firm data administration requires clients:all and data:manage.")
			return
		}
		var in struct {
			RetainUntil     string `json:"retain_until"`
			LegalHold       bool   `json:"legal_hold"`
			ExpectedVersion int    `json:"expected_version"`
			Reason          string `json:"reason"`
			Confirmed       bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Reason = strings.TrimSpace(in.Reason)
		if in.RetainUntil != "" {
			date, err := time.Parse("2006-01-02", in.RetainUntil)
			if err != nil || date.Format("2006-01-02") != in.RetainUntil || date.Year() < 2000 || date.Year() > 2200 {
				problem(w, 422, "Use an exact YYYY-MM-DD retention date approved by the firm.")
				return
			}
		}
		if !in.Confirmed || in.ExpectedVersion < 0 || len(in.Reason) < 10 || len(in.Reason) > 2000 {
			problem(w, 422, "Confirm the approved retention or hold decision and record its authority.")
			return
		}
		var version int
		err := db.QueryRowContext(r.Context(), `UPDATE client_closes SET retain_until=NULLIF($3,'')::date,legal_hold=$4,retention_reason=$5,retention_actor=$6,retention_changed_at=now(),retention_version=retention_version+1 WHERE id::text=$1 AND organization_id=$2 AND retention_version=$7 RETURNING retention_version`, r.PathValue("id"), organization(r), in.RetainUntil, in.LegalHold, in.Reason, subject(r), in.ExpectedVersion).Scan(&version)
		if err != nil {
			problem(w, 409, "Retention changed or could not be confirmed. Refresh before deciding.")
			return
		}
		jsonReply(w, 200, map[string]int{"version": version})
	})
	mux.HandleFunc("POST /v1/closes/{id}/delete", func(w http.ResponseWriter, r *http.Request) {
		if !canManageData(r) {
			problem(w, 403, "Firm data deletion requires clients:all and data:manage.")
			return
		}
		var in struct {
			ExpectedVersion int    `json:"expected_version"`
			Entity          string `json:"entity"`
			Period          string `json:"period"`
			ReasonCode      string `json:"reason_code"`
			Confirmed       bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !in.Confirmed || !slices.Contains([]string{"retention_expired", "client_request", "contract_termination"}, in.ReasonCode) {
			problem(w, 422, "Confirm the exact close and an approved deletion reason.")
			return
		}
		tx, err := db.BeginTx(r.Context(), &sql.TxOptions{Isolation: sql.LevelSerializable})
		if err != nil {
			problem(w, 503, "Deletion storage unavailable.")
			return
		}
		defer tx.Rollback()
		// The firm, then the close: the order opening-baseline reviews and close creation use.
		var enabled bool
		if err = tx.QueryRowContext(r.Context(), `SELECT enabled FROM organizations WHERE id=$1 FOR UPDATE`, organization(r)).Scan(&enabled); err != nil || !enabled {
			problem(w, 403, "Firm access changed. Refresh before deleting.")
			return
		}
		var id, entity, period string
		err = tx.QueryRowContext(r.Context(), `SELECT id::text,entity,period FROM client_closes WHERE id::text=$1 AND organization_id=$2 FOR UPDATE`, r.PathValue("id"), organization(r)).Scan(&id, &entity, &period)
		if err != nil || entity != in.Entity || period != in.Period {
			problem(w, 409, "Close identity changed. Refresh before deleting.")
			return
		}
		if organization(r) != "local-development" {
			var member bool
			if err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=$1 AND subject=$2 AND granted)`, organization(r), subject(r)).Scan(&member); err != nil || !member {
				problem(w, 403, "Firm membership changed. No data was deleted.")
				return
			}
		}
		status, err := readRetention(r.Context(), tx, id, organization(r))
		if err != nil || status.Version != in.ExpectedVersion || len(status.Blockers) != 0 {
			problem(w, 409, "Deletion is blocked. Refresh retention and resolve every listed dependency.")
			return
		}
		var productionCount int
		if err = tx.QueryRowContext(r.Context(), `SELECT (SELECT count(*) FROM production_postings WHERE close_id=$1)+(SELECT count(*) FROM xero_postings WHERE close_id=$1)`, id).Scan(&productionCount); err != nil {
			problem(w, 503, "Deletion scope could not be confirmed.")
			return
		}
		// Child records are removed in foreign-key order. Shared firm procedures,
		// member access and email sender connections stay in place.
		for _, query := range []string{
			`INSERT INTO collection_contact_preferences(organization_id,client_entity,recipient,do_not_contact,reason,recorded_by)
 SELECT DISTINCT b.organization_id,b.client_entity,b.recipient,true,'An earlier collection email to this address bounced, was marked as spam or was suppressed; kept when its close was deleted.','retention'
 FROM collection_messages c JOIN collection_bounces b ON b.organization_id=c.organization_id AND b.client_entity=lower(c.client_entity) AND b.recipient=lower(c.recipient)
 WHERE c.close_id=$1 AND NOT COALESCE((SELECT p.do_not_contact FROM collection_contact_preferences p WHERE p.organization_id=b.organization_id AND lower(p.client_entity)=b.client_entity AND lower(p.recipient)=b.recipient ORDER BY p.id DESC LIMIT 1),false)`,
			`DELETE FROM work_requests WHERE draft_id IN (SELECT id FROM request_drafts WHERE close_id=$1)`,
			`DELETE FROM request_drafts WHERE close_id=$1`,
			`DELETE FROM preparation_jobs WHERE close_id=$1`,
			`DELETE FROM production_postings WHERE close_id=$1`,
			`DELETE FROM xero_postings WHERE close_id=$1`,
			`DELETE FROM xero_corrections WHERE close_id=$1`,
			`DELETE FROM vendor_credit_postings WHERE close_id=$1`,
			`DELETE FROM bill_corrections WHERE close_id=$1`,
			`DELETE FROM credit_applications WHERE close_id=$1`,
			`DELETE FROM sandbox_postings WHERE close_id=$1`,
			`DELETE FROM procedure_example_approvals WHERE workpaper_id IN (SELECT id FROM workpapers WHERE close_id=$1)`,
			`DELETE FROM approval_requests WHERE close_id=$1`,
			`DELETE FROM chat_messages WHERE close_id=$1 OR target_close_id=$1 OR $1=ANY(closes)`,
			`DELETE FROM chat_sessions WHERE close_id=$1 OR (organization_id=(SELECT organization_id FROM client_closes WHERE id=$1) AND NOT EXISTS(SELECT 1 FROM chat_messages m WHERE m.session_id=chat_sessions.id))`,
			// A surviving chat's activity and title follow its remaining turns, so nothing of the deleted close lingers.
			`UPDATE chat_sessions s SET last_at=latest.at FROM (SELECT session_id,max(created_at) AS at FROM chat_messages GROUP BY session_id) latest WHERE latest.session_id=s.id AND s.organization_id=(SELECT organization_id FROM client_closes WHERE id=$1) AND s.last_at>latest.at`,
			`UPDATE chat_sessions s SET title=t.fresh FROM (SELECT s2.id,COALESCE(NULLIF(left(regexp_replace((SELECT m.body FROM chat_messages m WHERE m.session_id=s2.id AND m.role='user' ORDER BY m.id LIMIT 1),'\s+',' ','g'),80),''),'Conversation') AS fresh FROM chat_sessions s2 WHERE NOT s2.renamed AND s2.organization_id=(SELECT organization_id FROM client_closes WHERE id=$1)) t WHERE t.id=s.id AND s.title<>t.fresh`,
			`DELETE FROM portfolio_items WHERE close_id=$1`,
			`DELETE FROM statement_extractions WHERE close_id=$1`,
			`DELETE FROM workpapers WHERE close_id=$1`,
			`DELETE FROM daily_reviews WHERE close_id=$1`,
			`DELETE FROM quickbooks_entity_syncs WHERE close_id=$1`,
			`DELETE FROM quickbooks_snapshots WHERE close_id=$1`,
			// The client's books sent as files for the close's month go with it: their readings, then the files.
			`DELETE FROM books_layouts WHERE import_id IN (SELECT i.id FROM books_imports i JOIN client_closes c ON c.id=$1 WHERE i.organization_id=c.organization_id AND client_identity(i.organization_id,i.client_entity)=client_identity(c.organization_id,c.entity) AND i.period=c.period)`,
			`DELETE FROM books_imports WHERE id IN (SELECT i.id FROM books_imports i JOIN client_closes c ON c.id=$1 WHERE i.organization_id=c.organization_id AND client_identity(i.organization_id,i.client_entity)=client_identity(c.organization_id,c.entity) AND i.period=c.period)`,
			`DELETE FROM forwarded_files WHERE close_id=$1`,
			`DELETE FROM forwarding_links WHERE close_id=$1`,
			`DELETE FROM production_posting_grants WHERE connection_id IN (SELECT id FROM quickbooks_connections WHERE close_id=$1)`,
			`DELETE FROM quickbooks_connections WHERE close_id=$1`,
			`DELETE FROM xero_snapshots WHERE close_id=$1`,
			`DELETE FROM xero_posting_grants WHERE connection_id IN (SELECT id FROM xero_connections WHERE close_id=$1)`,
			`DELETE FROM xero_connections WHERE close_id=$1`,
			`DELETE FROM stripe_report_runs WHERE close_id=$1`,
			`DELETE FROM stripe_connections WHERE close_id=$1`,
			`DELETE FROM client_attachments WHERE close_id=$1`,
			`UPDATE procedure_drafts SET source_evidence_id=NULL,source_digest='',source_quote='' WHERE source_evidence_id IN (SELECT id FROM evidence WHERE close_id=$1)`,
			`DELETE FROM evidence WHERE close_id=$1`,
			`DELETE FROM preparation_plans WHERE close_id=$1`,
			`DELETE FROM close_preparations WHERE close_id=$1`,
			`DELETE FROM close_signoffs WHERE close_id=$1`,
			`DELETE FROM close_effort WHERE close_id=$1`,
			`DELETE FROM close_effort_entries WHERE close_id=$1`,
			`DELETE FROM close_scopes WHERE close_id=$1`,
		} {
			if _, err = tx.ExecContext(r.Context(), query, id); err != nil {
				problem(w, 503, "Deletion stopped without committing. Inspect related records and retry only after a fresh preview.")
				return
			}
		}
		// The client the close belongs to, read before the close is deleted or any of the client's other names forgotten.
		// The receipt keeps digests of it, never the name: as it is, and in the form aliases are kept in (case and
		// punctuation set aside), so a month deleted under any of the client's names, or under a name later confirmed as
		// another's alias, stays done on the roster.
		var client, plainClient string
		if err = tx.QueryRowContext(r.Context(), `SELECT client_identity($1,$2),trim(regexp_replace(client_identity($1,$2),'[^[:alnum:]]+',' ','g'))`, organization(r), entity).Scan(&client, &plainClient); err != nil {
			problem(w, 503, "Deletion stopped without committing.")
			return
		}
		if _, err = tx.ExecContext(r.Context(), `DELETE FROM client_closes WHERE id::text=$1 AND organization_id=$2`, id, organization(r)); err != nil {
			problem(w, 503, "Deletion stopped without committing.")
			return
		}
		// A name keeps its own client while it has a close from before it was confirmed as another client's alias. A
		// deletion never changes which client a name belongs to: when this close was the last such, the alias that would
		// now take effect is retired, so the client's roster entry, hand-overs and months stay its own.
		var after string
		if err = tx.QueryRowContext(r.Context(), `SELECT client_identity($1,$2)`, organization(r), entity).Scan(&after); err == nil && after != client {
			_, err = tx.ExecContext(r.Context(), `UPDATE client_aliases SET retired_by=$3,retired_at=now(),retire_reason=$4 WHERE organization_id=$1 AND retired_at IS NULL AND alias=trim(regexp_replace(lower($2),'[^[:alnum:]]+',' ','g'))`,
				organization(r), entity, subject(r), "Retired when the last close that kept this name its own client was deleted, so the name stays its own client")
		}
		if err != nil {
			problem(w, 503, "Deletion stopped without committing.")
			return
		}
		// Ending the relationship or the client's request ends the client's monthly hand-over and forgets the other
		// names its documents use.
		if in.ReasonCode == "contract_termination" || in.ReasonCode == "client_request" {
			if _, err = tx.ExecContext(r.Context(), `UPDATE close_routines SET stopped_at=now(),stopped_by=$3,stop_reason=$4 WHERE organization_id=$1 AND client_identity(organization_id,client_entity)=client_identity($1,$2) AND stopped_at IS NULL`, organization(r), entity, subject(r), "Stopped when "+entity+" "+period+" was deleted ("+strings.ReplaceAll(in.ReasonCode, "_", " ")+")."); err != nil {
				problem(w, 503, "Deletion stopped without committing.")
				return
			}
			// The client leaves the firm's roster, so no later month is reported as owed and none can be opened: every name it
			// goes by is left inactive — its roster entries', its closes', its confirmed aliases' and its own — before those
			// aliases are forgotten below, after which each is a name of its own.
			if _, err = tx.ExecContext(r.Context(), `INSERT INTO client_roster(organization_id,client_entity,active,since,reason,actor)
 SELECT $1,n.name,false,COALESCE(n.since,$5),$3,$4 FROM (SELECT DISTINCT ON (lower(k.name)) k.name,l.since,l.active
  FROM (SELECT r.client_entity AS name,1 AS rank FROM client_roster r WHERE r.organization_id=$1 AND client_identity(r.organization_id,r.client_entity)=$6
   UNION ALL SELECT c.entity,2 FROM client_closes c WHERE c.organization_id=$1 AND client_identity(c.organization_id,c.entity)=$6
   UNION ALL SELECT $2::text,2
   UNION ALL SELECT a.alias,3 FROM client_aliases a WHERE a.organization_id=$1 AND a.retired_at IS NULL AND lower(a.client_entity)=$6
   UNION ALL SELECT $6::text,4) k
  LEFT JOIN LATERAL (SELECT r.since,r.active FROM client_roster r WHERE r.organization_id=$1 AND lower(r.client_entity)=lower(k.name) ORDER BY r.id DESC LIMIT 1) l ON true
  ORDER BY lower(k.name),k.rank) n WHERE n.active IS DISTINCT FROM false`,
				organization(r), entity, "Left the roster when "+entity+"'s close was deleted ("+strings.ReplaceAll(in.ReasonCode, "_", " ")+")", subject(r), period, client); err != nil {
				problem(w, 503, "Deletion stopped without committing.")
				return
			}
			if _, err = tx.ExecContext(r.Context(), `DELETE FROM client_aliases WHERE organization_id=$1 AND lower(client_entity)=lower($2)`, organization(r), entity); err != nil {
				problem(w, 503, "Deletion stopped without committing.")
				return
			}
		}
		var receipt string
		identity := preparation.Digest([]byte(organization(r) + "\x00" + strings.ToLower(entity) + "\x00" + period))
		err = tx.QueryRowContext(r.Context(), `INSERT INTO close_deletion_receipts(organization_id,close_id,entity_digest,period,production_postings,actor,reason_code,client_digest,plain_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id::text`, organization(r), id, identity, period, productionCount, subject(r), in.ReasonCode,
			preparation.Digest([]byte(organization(r)+"\x00"+client+"\x00"+period)), preparation.Digest([]byte(organization(r)+"\x00"+plainClient+"\x00"+period))).Scan(&receipt)
		if err != nil {
			problem(w, 503, "Deletion stopped without committing. Refresh before retrying.")
			return
		}
		if tx.Commit() != nil {
			problem(w, 503, "Deletion outcome is uncertain. Inspect the deletion receipt before retrying.")
			return
		}
		jsonReply(w, 200, map[string]string{"deletion_receipt_id": receipt})
	})
}
