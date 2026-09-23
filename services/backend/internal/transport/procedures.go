package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
)

type procedureImpact struct {
	VersionID    string          `json:"version_id"`
	SourceID     string          `json:"source_id"`
	Affected     json.RawMessage `json:"affected_drafts"`
	ImpactDigest string          `json:"impact_digest"`
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

func registerProcedures(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/{id}/procedures/impact", func(w http.ResponseWriter, r *http.Request) {
		kind := r.URL.Query().Get("kind")
		if kind != "prepaid" && kind != "reconciliation" && kind != "accrual" {
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
		rows, err := db.QueryContext(r.Context(), `SELECT id::text,client_entity,kind,instructions,expense_account,prepaid_account,enabled,reason,actor,created_at::text FROM procedure_versions WHERE organization_id=$1 AND ($2 OR client_entity='' OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=procedure_versions.organization_id AND g.subject=$3 AND lower(g.client_entity)=lower(procedure_versions.client_entity) AND g.granted)) ORDER BY id DESC LIMIT 201`, organization(r), firmAccess(r), subject(r))
		if err != nil {
			problem(w, 503, "Firm procedures unavailable.")
			return
		}
		defer rows.Close()
		items := []preparation.Procedure{}
		for rows.Next() {
			var p preparation.Procedure
			if err = rows.Scan(&p.ID, &p.ClientEntity, &p.Kind, &p.Instructions, &p.ExpenseAccount, &p.PrepaidAccount, &p.Enabled, &p.Reason, &p.Actor, &p.CreatedAt); err != nil {
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
			PreviousID string `json:"previous_id"`
			Confirmed  bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.ClientEntity = strings.TrimSpace(in.ClientEntity)
		in.Instructions = strings.TrimSpace(in.Instructions)
		in.Reason = strings.TrimSpace(in.Reason)
		in.ExpenseAccount = strings.TrimSpace(in.ExpenseAccount)
		in.PrepaidAccount = strings.TrimSpace(in.PrepaidAccount)
		if !in.Confirmed || (in.Kind != "prepaid" && in.Kind != "reconciliation" && in.Kind != "accrual") || len(in.ClientEntity) > 200 || len(in.Instructions) < 1 || len(in.Instructions) > 4000 || len(in.Reason) < 1 || len(in.Reason) > 2000 || len(in.ExpenseAccount) > 200 || len(in.PrepaidAccount) > 200 || strings.ContainsAny(in.ExpenseAccount+in.PrepaidAccount, "\r\n") || (in.Kind != "prepaid" && (in.ExpenseAccount != "" || in.PrepaidAccount != "")) {
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
		if err = tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT id::text FROM procedure_versions WHERE organization_id=$1 AND lower(client_entity)=lower($2) AND kind=$3 ORDER BY id DESC LIMIT 1),'')`, org, in.ClientEntity, in.Kind).Scan(&prior); err != nil {
			problem(w, 503, "Could not read current procedure.")
			return
		}
		if prior != in.PreviousID {
			problem(w, 409, "Procedure changed. Refresh and review the latest version first.")
			return
		}
		var id string
		err = tx.QueryRowContext(r.Context(), `INSERT INTO procedure_versions(organization_id,client_entity,kind,instructions,expense_account,prepaid_account,enabled,reason,actor) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id::text`, org, in.ClientEntity, in.Kind, in.Instructions, in.ExpenseAccount, in.PrepaidAccount, in.Enabled, in.Reason, actor).Scan(&id)
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
		if !in.Confirmed || (in.Kind != "prepaid" && in.Kind != "reconciliation" && in.Kind != "accrual") {
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
