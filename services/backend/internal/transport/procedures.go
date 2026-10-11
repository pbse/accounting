package transport

import (
	"close.local/backend/internal/loans"
	"close.local/backend/internal/owners"
	"close.local/backend/internal/payroll"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
)

type procedureImpact struct {
	VersionID    string          `json:"version_id"`
	SourceID     string          `json:"source_id"`
	Affected     json.RawMessage `json:"affected_drafts"`
	ImpactDigest string          `json:"impact_digest"`
}

type procedureScopeReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// procedureScopeDigest covers the closes a new version in client's (or the firm's) scope would apply to: from month from
// on, when it takes effect from one.
func procedureScopeDigest(ctx context.Context, q procedureScopeReader, org, client, kind, previous, from string) (string, int, error) {
	var total int
	var scopeHash string
	err := q.QueryRowContext(ctx, `SELECT count(*),COALESCE(md5(string_agg(c.id::text||':'||COALESCE(w.last_id::text,'')||':'||COALESCE(e.source_id::text,''),',' ORDER BY c.id)),'') FROM client_closes c LEFT JOIN LATERAL (SELECT max(id) last_id FROM workpapers WHERE close_id=c.id AND kind=$3) w ON true LEFT JOIN LATERAL (SELECT max(e.id) source_id FROM evidence e JOIN procedure_versions p ON p.id=e.procedure_id WHERE e.close_id=c.id AND p.kind=$3 AND e.superseded_by IS NULL) e ON true WHERE c.organization_id=$1 AND (($2<>'' AND lower(c.entity)=lower($2)) OR ($2='' AND NOT EXISTS(SELECT 1 FROM procedure_versions p WHERE p.organization_id=c.organization_id AND lower(p.client_entity)=lower(c.entity) AND p.kind=$3 AND (p.effective_from IS NULL OR p.effective_from<=c.period)))) AND ($4='' OR c.period>=$4)`, org, client, kind, from).Scan(&total, &scopeHash)
	return preparation.Digest([]byte(kind + "/" + strings.ToLower(client) + "/" + previous + "/" + from + "/" + scopeHash)), total, err
}

func readProcedureImpact(ctx context.Context, tx *sql.Tx, closeID, kind string) (procedureImpact, error) {
	var out procedureImpact
	p, err := preparation.CurrentProcedure(ctx, tx, closeID, kind)
	if err != nil {
		return out, err
	}
	out.VersionID = p.ID
	err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT e.id::text FROM evidence e JOIN procedure_versions p ON p.id=e.procedure_id WHERE e.close_id::text=$1 AND p.kind=$2 AND e.superseded_by IS NULL ORDER BY e.id DESC LIMIT 1),'')`, closeID, kind).Scan(&out.SourceID)
	if err != nil {
		return out, err
	}
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',w.id::text,'kind',w.kind,'review_state',w.review_state,'digest',w.digest) ORDER BY w.id),'[]'::jsonb) FROM workpaper_review_state w WHERE w.close_id::text=$1 AND w.kind=$2 AND $3<>'' AND w.paper->'sources' @> jsonb_build_array(jsonb_build_object('id',$3::text))`, closeID, kind, out.SourceID).Scan(&out.Affected)
	if err != nil {
		return out, err
	}
	out.ImpactDigest = preparation.Digest([]byte(out.VersionID + "/" + out.SourceID + "/" + string(out.Affected)))
	return out, nil
}

// Kinds whose procedure names its reviewed method directly; other methods come from procedure learning.
var fixedMethods = map[string][]string{"loan": {loans.Method, loans.VariableMethod, loans.CostsMethod}, "payroll": {payroll.Method}, "basis": {"cash", "modified_cash", "accrual"}}

func registerProcedures(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/{id}/procedures/suggestion", func(w http.ResponseWriter, r *http.Request) {
		kind := r.URL.Query().Get("kind")
		if kind != "prepaid" && kind != "accrual" && kind != "reconciliation" && kind != "loan" {
			problem(w, 422, "Choose one supported work type.")
			return
		}
		var entity string
		if err := db.QueryRowContext(r.Context(), `SELECT entity FROM client_closes WHERE id::text=$1 AND organization_id=$2`, r.PathValue("id"), organization(r)).Scan(&entity); err != nil {
			problem(w, 404, "Close unavailable.")
			return
		}
		rows, err := db.QueryContext(r.Context(), `SELECT w.id::text,w.paper,p.paper FROM workpapers w JOIN client_closes c ON c.id=w.close_id JOIN workpapers p ON p.id::text=w.paper->>'revises' AND p.close_id=w.close_id WHERE c.organization_id=$1 AND lower(c.entity)=lower($2) AND w.kind=$3 ORDER BY w.id DESC LIMIT 20`, organization(r), entity, kind)
		if err != nil {
			problem(w, 503, "Prior edit history unavailable.")
			return
		}
		defer rows.Close()
		fields := map[string]int{}
		papers := []string{}
		for rows.Next() {
			var id string
			var current, previous []byte
			if err = rows.Scan(&id, &current, &previous); err != nil {
				break
			}
			papers = append(papers, id)
			for _, change := range paperChanges(previous, current) {
				if strings.HasPrefix(change.Field, "input.") && (strings.Contains(change.Field, "method") || strings.Contains(change.Field, "account") || strings.Contains(change.Field, "booking") || strings.Contains(change.Field, "estimation") || strings.Contains(change.Field, "rule")) {
					fields[change.Field]++
				}
			}
		}
		if err == nil {
			err = rows.Err()
		}
		if err != nil {
			problem(w, 503, "Prior edit history unavailable.")
			return
		}
		names := []string{}
		for field := range fields {
			names = append(names, field)
		}
		sort.Slice(names, func(i, j int) bool {
			if fields[names[i]] == fields[names[j]] {
				return names[i] < names[j]
			}
			return fields[names[i]] > fields[names[j]]
		})
		if len(names) > 5 {
			names = names[:5]
		}
		instruction := ""
		if len(names) > 0 {
			instruction = "Review current source evidence and the client's approved policy for " + strings.Join(names, ", ") + " before preparing. Prior revisions identify review points, not an approved accounting rule or current account mapping."
		}
		jsonReply(w, 200, map[string]any{"client_entity": entity, "kind": kind, "draft_instructions": instruction, "changed_fields": names, "source_draft_ids": papers})
	})
	mux.HandleFunc("GET /v1/closes/{id}/procedures/impact", func(w http.ResponseWriter, r *http.Request) {
		kind := r.URL.Query().Get("kind")
		if kind != "prepaid" && kind != "reconciliation" && kind != "accrual" && kind != "loan" {
			problem(w, 422, "Choose supported work to preview.")
			return
		}
		tx, err := db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
		if err != nil {
			problem(w, 503, "Procedure impact unavailable.")
			return
		}
		defer tx.Rollback()
		impact, err := readProcedureImpact(r.Context(), tx, r.PathValue("id"), kind)
		if err == sql.ErrNoRows {
			problem(w, 404, "No approved procedure for this work.")
			return
		}
		if err != nil {
			problem(w, 503, "Procedure impact unavailable.")
			return
		}
		jsonReply(w, 200, impact)
	})
	mux.HandleFunc("GET /v1/closes/procedures", func(w http.ResponseWriter, r *http.Request) {
		if kind := r.URL.Query().Get("impact_kind"); kind != "" {
			client, from := strings.TrimSpace(r.URL.Query().Get("client_entity")), r.URL.Query().Get("effective_from")
			if (kind != "prepaid" && kind != "accrual" && kind != "reconciliation" && kind != "loan" && kind != "basis" && kind != "owner" && kind != "payroll") || len(client) > 200 || !effectiveMonth(from) {
				problem(w, 422, "Choose a supported procedure scope.")
				return
			}
			allowed, err := clientAccess(db, r, client)
			if err != nil || !allowed {
				problem(w, 403, "Client access is required to preview procedure impact.")
				return
			}
			var previous string
			err = db.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT id::text FROM procedure_versions WHERE organization_id=$1 AND lower(client_entity)=lower($2) AND kind=$3 ORDER BY procedure_versions.id DESC LIMIT 1),'')`, organization(r), client, kind).Scan(&previous)
			if err != nil {
				problem(w, 503, "Procedure impact unavailable.")
				return
			}
			digest, total, err := procedureScopeDigest(r.Context(), db, organization(r), client, kind, previous, from)
			if err != nil {
				problem(w, 503, "Procedure impact unavailable.")
				return
			}
			rows, err := db.QueryContext(r.Context(), `SELECT c.id::text,c.entity,c.period,COALESCE((SELECT e.id::text FROM evidence e JOIN procedure_versions p ON p.id=e.procedure_id WHERE e.close_id=c.id AND p.kind=$3 AND e.superseded_by IS NULL ORDER BY e.id DESC LIMIT 1),''),(SELECT count(*) FROM workpapers w WHERE w.close_id=c.id AND w.kind=$3) FROM client_closes c WHERE c.organization_id=$1 AND (($2<>'' AND lower(c.entity)=lower($2)) OR ($2='' AND NOT EXISTS(SELECT 1 FROM procedure_versions p WHERE p.organization_id=c.organization_id AND lower(p.client_entity)=lower(c.entity) AND p.kind=$3 AND (p.effective_from IS NULL OR p.effective_from<=c.period)))) AND ($4='' OR c.period>=$4) ORDER BY c.id DESC LIMIT 100`, organization(r), client, kind, from)
			if err != nil {
				problem(w, 503, "Procedure impact unavailable.")
				return
			}
			defer rows.Close()
			items := []map[string]any{}
			for rows.Next() {
				var id, entity, period, source string
				var drafts int
				if rows.Scan(&id, &entity, &period, &source, &drafts) != nil {
					problem(w, 503, "Procedure impact unavailable.")
					return
				}
				items = append(items, map[string]any{"close_id": id, "entity": entity, "period": period, "pinned_source_id": source, "draft_count": drafts})
			}
			if rows.Err() != nil {
				problem(w, 503, "Procedure impact unavailable.")
				return
			}
			jsonReply(w, 200, map[string]any{"kind": kind, "client_entity": client, "previous_id": previous, "impact_digest": digest, "total_closes": total, "has_more": total > len(items), "closes": items})
			return
		}
		rows, err := db.QueryContext(r.Context(), `SELECT id::text,client_entity,kind,method,instructions,expense_account,prepaid_account,enabled,reason,actor,created_at::text,COALESCE(effective_from,'') FROM procedure_versions WHERE organization_id=$1 AND ($2 OR client_entity='' OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=procedure_versions.organization_id AND g.subject=$3 AND lower(g.client_entity)=lower(procedure_versions.client_entity) AND g.granted)) AND ($4='' OR procedure_versions.client_entity='' OR lower(procedure_versions.client_entity)=lower($4)) ORDER BY procedure_versions.id DESC LIMIT 201`, organization(r), firmAccess(r), subject(r), strings.TrimSpace(r.URL.Query().Get("client")))
		if err != nil {
			problem(w, 503, "Firm procedures unavailable.")
			return
		}
		defer rows.Close()
		items := []preparation.Procedure{}
		for rows.Next() {
			var p preparation.Procedure
			if err = rows.Scan(&p.ID, &p.ClientEntity, &p.Kind, &p.Method, &p.Instructions, &p.ExpenseAccount, &p.PrepaidAccount, &p.Enabled, &p.Reason, &p.Actor, &p.CreatedAt, &p.EffectiveFrom); err != nil {
				problem(w, 503, "Could not read procedures.")
				return
			}
			items = append(items, p)
		}
		if rows.Err() != nil {
			problem(w, 503, "Could not finish procedure history.")
			return
		}
		if len(items) > 200 {
			problem(w, 422, "Procedure history exceeds the 200-version view. Contact your operator for an export.")
			return
		}
		jsonReply(w, 200, map[string]any{"versions": items})
	})
	mux.HandleFunc("POST /v1/closes/procedures", func(w http.ResponseWriter, r *http.Request) {
		actor, ok := reviewActor(w, r)
		if !ok {
			return
		}
		if a := auth.Actor(r.Context()); a != nil && a.Organization != "" && !slices.Contains(a.Permissions, "procedures:manage") {
			problem(w, 403, "Your organization role lacks procedures:manage.")
			return
		}
		var in struct {
			preparation.Procedure
			PreviousID   string `json:"previous_id"`
			ImpactDigest string `json:"impact_digest"`
			Confirmed    bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.ClientEntity = strings.TrimSpace(in.ClientEntity)
		in.Instructions = strings.TrimSpace(in.Instructions)
		in.Reason = strings.TrimSpace(in.Reason)
		in.ExpenseAccount = strings.TrimSpace(in.ExpenseAccount)
		in.PrepaidAccount = strings.TrimSpace(in.PrepaidAccount)
		methodOK := slices.Contains(fixedMethods[in.Kind], in.Method) || (fixedMethods[in.Kind] == nil && in.Method == "")
		if in.Kind == "owner" {
			// Entity type is a fact about one client; the firm default carries only policy and account names.
			methodOK = (in.ClientEntity != "") == (in.Method != "") && (in.Method == "" || slices.Contains(owners.EntityTypes, in.Method))
		}
		if !in.Confirmed || !methodOK || (in.Kind == "basis" && !in.Enabled) || (in.Kind != "prepaid" && in.Kind != "reconciliation" && in.Kind != "accrual" && in.Kind != "loan" && in.Kind != "basis" && in.Kind != "owner" && in.Kind != "payroll") || len(in.ClientEntity) > 200 || len(in.Instructions) < 1 || len(in.Instructions) > 4000 || len(in.Reason) < 1 || len(in.Reason) > 2000 || len(in.ExpenseAccount) > 200 || len(in.PrepaidAccount) > 200 || strings.ContainsAny(in.ExpenseAccount+in.PrepaidAccount, "\r\n") || (in.Kind != "prepaid" && (in.ExpenseAccount != "" || in.PrepaidAccount != "")) || !effectiveMonth(in.EffectiveFrom) {
			problem(w, 422, "Confirm a supported procedure, bounded instructions, a reason and optional single-line prepaid account names.")
			return
		}
		allowed, e := clientAccess(db, r, in.ClientEntity)
		if e != nil {
			problem(w, 503, "Client access unavailable.")
			return
		}
		if !allowed {
			problem(w, 403, "Only assigned client overrides can be changed by this account.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Procedure storage unavailable.")
			return
		}
		defer tx.Rollback()
		var org string
		if err = tx.QueryRowContext(r.Context(), `SELECT id FROM organizations WHERE id=$1 AND enabled FOR UPDATE`, organization(r)).Scan(&org); err != nil {
			problem(w, 403, "Organization unavailable.")
			return
		}
		if in.ClientEntity != "" {
			var exists bool
			if err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM client_closes WHERE organization_id=$1 AND lower(entity)=lower($2))`, org, in.ClientEntity).Scan(&exists); err != nil || !exists {
				problem(w, 422, "Choose an existing client in this organization.")
				return
			}
		}
		var prior string
		if err = tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT id::text FROM procedure_versions WHERE organization_id=$1 AND lower(client_entity)=lower($2) AND kind=$3 ORDER BY procedure_versions.id DESC LIMIT 1),'')`, org, in.ClientEntity, in.Kind).Scan(&prior); err != nil {
			problem(w, 503, "Could not read current procedure.")
			return
		}
		if prior != in.PreviousID {
			problem(w, 409, "Procedure changed. Refresh and review the latest version first.")
			return
		}
		if in.Enabled && fixedMethods[in.Kind] == nil && in.Kind != "owner" {
			var learned bool
			err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM procedure_versions WHERE organization_id=$1 AND kind=$2 AND method<>'' AND (client_entity='' OR lower(client_entity)=lower($3)))`, org, in.Kind, in.ClientEntity).Scan(&learned)
			if err != nil || learned {
				problem(w, 409, "An active method rule requires a new reviewed draft and regression example. Use procedure learning.")
				return
			}
		}
		digest, _, impactErr := procedureScopeDigest(r.Context(), tx, org, in.ClientEntity, in.Kind, prior, in.EffectiveFrom)
		if impactErr != nil || in.ImpactDigest == "" || in.ImpactDigest != digest {
			problem(w, 409, "Affected closes changed. Refresh the procedure impact before publishing.")
			return
		}
		var id string
		err = tx.QueryRowContext(r.Context(), `INSERT INTO procedure_versions(organization_id,client_entity,kind,instructions,expense_account,prepaid_account,enabled,reason,actor,method,effective_from) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,'')) RETURNING id::text`, org, in.ClientEntity, in.Kind, in.Instructions, in.ExpenseAccount, in.PrepaidAccount, in.Enabled, in.Reason, actor, in.Method, in.EffectiveFrom).Scan(&id)
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "Procedure save could not be confirmed. Reload history before retrying.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": id})
	})
	mux.HandleFunc("POST /v1/closes/{id}/procedures/apply", func(w http.ResponseWriter, r *http.Request) {
		actor, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var in struct {
			Kind         string `json:"kind"`
			VersionID    string `json:"version_id"`
			ImpactDigest string `json:"impact_digest"`
			Confirmed    bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !in.Confirmed || (in.Kind != "prepaid" && in.Kind != "reconciliation" && in.Kind != "accrual" && in.Kind != "loan") {
			problem(w, 422, "Confirm a supported procedure and its impact on existing work.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Procedure storage unavailable.")
			return
		}
		defer tx.Rollback()
		var closeID string
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&closeID); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		p, err := preparation.CurrentProcedure(r.Context(), tx, closeID, in.Kind)
		if err != nil || !p.Enabled || p.ID != in.VersionID {
			problem(w, 409, "The effective procedure changed or is disabled. Refresh before applying it.")
			return
		}
		impact, err := readProcedureImpact(r.Context(), tx, closeID, in.Kind)
		if err != nil || in.ImpactDigest == "" || in.ImpactDigest != impact.ImpactDigest {
			problem(w, 409, "Affected work changed. Refresh the impact preview before applying this procedure.")
			return
		}
		source, err := preparation.AttachProcedure(r.Context(), tx, closeID, p, true, actor)
		if err != nil {
			problem(w, 409, err.Error())
			return
		}
		if tx.Commit() != nil {
			problem(w, 503, "Apply could not be confirmed. Inspect evidence before retrying.")
			return
		}
		jsonReply(w, 200, map[string]string{"source_id": source})
	})
}

// effectiveMonth says whether a version's first month is "" (every month) or a valid YYYY-MM.
func effectiveMonth(from string) bool {
	if from == "" {
		return true
	}
	_, err := time.Parse("2006-01", from)
	return err == nil && len(from) == 7
}
