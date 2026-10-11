package transport

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"close.local/backend/internal/preparation"
)

// sameClient matches closes c of the client named $2 in organization $1, through current confirmed aliases.
const sameClient = `c.organization_id=$1 AND client_identity(c.organization_id,c.entity)=client_identity($1,$2)`

// openCleanup counts the inherited cleanup items of every baseline of opening close c that have no resolution.
const openCleanup = `(SELECT count(*) FROM opening_baselines b CROSS JOIN LATERAL jsonb_object_keys(b.notes) k WHERE b.close_id=c.id AND NOT EXISTS(SELECT 1 FROM opening_cleanup_resolutions r WHERE r.baseline_id=b.id AND r.item=k))`

// openingItem is an account or finding of the opening tie-out that needs a note; noted, it is inherited cleanup.
type openingItem struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// openingContent is what a reviewer of the opening baseline sees: the items that need a note, and a digest over the
// tie-out without its capture's identity or time, so a recapture with the same balances and support stays reviewed.
func openingContent(tie tieOutcome) (string, []openingItem, error) {
	stale, _ := tie.Snapshot["stale"].(bool)
	switch {
	case tie.Snapshot == nil:
		return "", nil, tieRefusal{409, "Capture this month's Trial Balance first: the opening baseline is the tie-out at this month-end."}
	case tie.Error != "":
		return "", nil, tieRefusal{409, "The Trial Balance could not be read: " + tie.Error}
	case stale:
		return "", nil, tieRefusal{409, "Capture the Trial Balance again: this capture is no longer current."}
	}
	items := []openingItem{}
	for _, row := range tie.Rows {
		switch row.Status {
		case "posting_unknown":
			return "", nil, tieRefusal{409, "Resolve the uncertain posting on " + row.Account + " before reviewing the opening baseline."}
		case "ties", "within_materiality":
			continue
		}
		label := row.Account + ": " + row.Balance + " in the Trial Balance, with no supporting schedule"
		if row.Expected != nil {
			label = fmt.Sprintf("%s: %s in the Trial Balance, %s supported, a difference of %s (%s)", row.Account, row.Balance, *row.Expected, *row.Difference, strings.ReplaceAll(row.Status, "_", " "))
		}
		items = append(items, openingItem{"row:" + row.AccountID, label})
	}
	for i, u := range tie.Unmatched {
		items = append(items, openingItem{fmt.Sprintf("unmatched:%d", i), cmp.Or(u["account"], "Draft #"+u["paper_id"]) + ": " + u["reason"]})
	}
	for _, u := range tie.Unclassified {
		items = append(items, openingItem{"unclassified:" + u["account_id"], u["account"] + ": " + u["balance"] + " in an account with no balance-sheet class"})
	}
	reviewed := tie
	reviewed.Snapshot = map[string]any{"source": tie.Snapshot["source"]}
	raw, _ := json.Marshal(reviewed)
	return preparation.Digest(raw), items, nil
}

// openingBlockers holds back sign-off until a client's opening baseline is reviewed and its inherited cleanup resolved.
// On the opening close it also returns the accounts the current baseline noted, whose differences no longer block it.
func openingBlockers(ctx context.Context, tx *sql.Tx, closeID string, tie tieOutcome) ([]string, map[string]bool, error) {
	var org, entity, period string
	var takeover bool
	if err := tx.QueryRowContext(ctx, `SELECT organization_id,entity,period,takeover FROM client_closes WHERE id::text=$1`, closeID).Scan(&org, &entity, &period, &takeover); err != nil {
		return nil, nil, err
	}
	if takeover {
		var blockers []string
		var confirmed, sameCompanies bool
		var setupProfile []byte
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM takeover_setups WHERE close_id::text=$1),
 COALESCE((SELECT content->'profile' FROM takeover_setups WHERE close_id::text=$1 ORDER BY id DESC LIMIT 1),'null'::jsonb),
 COALESCE((SELECT t.content->'companies' IS NULL OR (SELECT jsonb_agg(jsonb_build_object('provider',e->>'provider','company',e->>'company') ORDER BY e->>'provider',e->>'company') FROM jsonb_array_elements(t.content->'companies') e) IS NOT DISTINCT FROM (SELECT jsonb_agg(jsonb_build_object('provider',e->>'provider','company',e->>'company') ORDER BY e->>'provider',e->>'company') FROM jsonb_array_elements(`+takeoverCompanies+`) e)
  FROM takeover_setups t WHERE t.close_id=c.id ORDER BY t.id DESC LIMIT 1),true)
 FROM client_closes c WHERE c.id::text=$1`, closeID).Scan(&confirmed, &setupProfile, &sameCompanies); err != nil {
			return nil, nil, err
		}
		if !confirmed {
			blockers = append(blockers, "Confirm the takeover setup: the company, basis, procedures, access, account scope and timezone this client is taken over with.")
		} else {
			// The setup confirmed the client's currency and fiscal year as they were then: one confirmed or corrected since
			// is confirmed with the setup again.
			profile, err := preparation.ClientProfile(ctx, tx, closeID)
			if err != nil {
				return nil, nil, err
			}
			var then preparation.Profile
			json.Unmarshal(setupProfile, &then)
			if (profile.State == "confirmed" || profile.State == "differs") && (then.Currency != profile.Currency || then.FiscalStart != profile.FiscalStart) {
				blockers = append(blockers, "Confirm the takeover setup again: the client's currency or fiscal year was confirmed or corrected after it was confirmed.")
			}
			// The setup confirmed the company the books were read from: another provider or company since (not a change
			// of its connection's state) asks again.
			if !sameCompanies {
				blockers = append(blockers, "Confirm the takeover setup again: the company its books are read from changed after it was confirmed.")
			}
		}
		digest, _, refusal := openingContent(tie)
		var reviewed string
		var notes []byte
		err := tx.QueryRowContext(ctx, `SELECT digest,notes FROM opening_baselines WHERE close_id::text=$1 ORDER BY id DESC LIMIT 1`, closeID).Scan(&reviewed, &notes)
		if err != nil && err != sql.ErrNoRows {
			return nil, nil, err
		}
		if refusal != nil || err == sql.ErrNoRows || reviewed != digest {
			return append(blockers, "Review the opening baseline: this is the client's first close in the firm, so its balances are the starting point."), nil, nil
		}
		var keys map[string]string
		json.Unmarshal(notes, &keys)
		noted := map[string]bool{}
		for key := range keys {
			if id, ok := strings.CutPrefix(key, "row:"); ok {
				noted[id] = true
			} else if id, ok := strings.CutPrefix(key, "unclassified:"); ok {
				noted[id] = true
			}
		}
		return blockers, noted, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT c.entity,c.period,EXISTS(SELECT 1 FROM opening_baselines b WHERE b.close_id=c.id),`+openCleanup+`
 FROM client_closes c WHERE `+sameClient+` AND c.takeover AND c.period<$3 ORDER BY c.id`, org, entity, period)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var blockers []string
	for rows.Next() {
		var name, period string
		var reviewed bool
		var open int
		if err = rows.Scan(&name, &period, &reviewed, &open); err != nil {
			return nil, nil, err
		}
		if !reviewed {
			blockers = append(blockers, "Review the opening baseline on "+name+" "+period+" first: it is this client's first close in the firm.")
		} else if open > 0 {
			blockers = append(blockers, fmt.Sprintf("Resolve %d inherited cleanup item(s) from the opening baseline on %s %s.", open, name, period))
		}
	}
	return blockers, nil, rows.Err()
}

// registerOpeningBaseline reviews a client's opening month: its tie-out at the takeover date, with a note for each account
// or finding that does not tie, which becomes inherited cleanup resolved later with a reference.
func registerOpeningBaseline(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/{id}/opening-baseline", func(w http.ResponseWriter, r *http.Request) {
		tx, err := db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			problem(w, 503, "Could not open a consistent snapshot.")
			return
		}
		defer tx.Rollback()
		var takeover bool
		if err = tx.QueryRowContext(r.Context(), `SELECT takeover FROM client_closes WHERE id::text=$1`, r.PathValue("id")).Scan(&takeover); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		out := map[string]any{"takeover": takeover}
		if !takeover {
			jsonReply(w, 200, out)
			return
		}
		tie, err := closeTieOut(r.Context(), tx, r.PathValue("id"))
		if refused, ok := err.(tieRefusal); ok {
			out["refusal"] = refused.message
		} else if err != nil {
			problem(w, 503, "Tie-out unavailable.")
			return
		} else if digest, items, refusal := openingContent(tie); refusal != nil {
			out["refusal"] = refusal.Error()
		} else {
			out["digest"], out["items"] = digest, items
		}
		var baselines []byte
		if err = tx.QueryRowContext(r.Context(), `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',b.id::text,'digest',b.digest,'items',b.content->'items','notes',b.notes,'reviewed_by',b.reviewed_by,'reviewed_at',b.reviewed_at,
 'resolutions',COALESCE((SELECT jsonb_object_agg(r.item,jsonb_build_object('reference',r.reference,'resolved_by',r.resolved_by,'resolved_at',r.resolved_at)) FROM opening_cleanup_resolutions r WHERE r.baseline_id=b.id),'{}'::jsonb)) ORDER BY b.id DESC),'[]'::jsonb)
 FROM opening_baselines b WHERE b.close_id::text=$1`, r.PathValue("id")).Scan(&baselines); err != nil {
			problem(w, 503, "Opening baselines unavailable.")
			return
		}
		out["baselines"] = json.RawMessage(baselines)
		jsonReply(w, 200, out)
	})
	mux.HandleFunc("POST /v1/closes/{id}/opening-baseline", func(w http.ResponseWriter, r *http.Request) {
		who, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var in struct {
			Digest    string            `json:"digest"`
			Notes     map[string]string `json:"notes"`
			Confirmed bool              `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !in.Confirmed {
			problem(w, 422, "Confirm that these balances are the client's starting point.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Opening baselines unavailable.")
			return
		}
		defer tx.Rollback()
		// The firm, then the close: the order creating a close uses when it moves the opening month.
		var org string
		var takeover bool
		err = tx.QueryRowContext(r.Context(), `SELECT o.id FROM organizations o JOIN client_closes c ON c.organization_id=o.id WHERE c.id::text=$1 FOR SHARE OF o`, r.PathValue("id")).Scan(&org)
		if err == nil {
			err = tx.QueryRowContext(r.Context(), `SELECT takeover FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&takeover)
		}
		if err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		if !takeover {
			problem(w, 409, "Only a client's first close in the firm has an opening baseline.")
			return
		}
		tie, err := closeTieOut(r.Context(), tx, r.PathValue("id"))
		if refused, ok := err.(tieRefusal); ok {
			problem(w, refused.status, refused.message)
			return
		} else if err != nil {
			problem(w, 503, "Tie-out unavailable.")
			return
		}
		digest, items, err := openingContent(tie)
		if refused, ok := err.(tieRefusal); ok {
			problem(w, refused.status, refused.message)
			return
		}
		if in.Digest != digest {
			problem(w, 409, "The tie-out changed since you looked. Review the current balances again.")
			return
		}
		var newest string
		if err = tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT digest FROM opening_baselines WHERE close_id::text=$1 ORDER BY id DESC LIMIT 1),'')`, r.PathValue("id")).Scan(&newest); err != nil {
			problem(w, 503, "Opening baselines unavailable.")
			return
		}
		if newest == digest {
			problem(w, 409, "This opening baseline is already reviewed.")
			return
		}
		notes := map[string]string{}
		for _, item := range items {
			note := strings.TrimSpace(in.Notes[item.Key])
			if n := utf8.RuneCountInString(note); n < 5 || n > 2000 {
				problem(w, 422, "Note how each account or finding that does not tie will be cleaned up (5–2,000 characters): "+item.Label)
				return
			}
			notes[item.Key] = note
		}
		if len(in.Notes) != len(notes) {
			problem(w, 422, "Note only the accounts and findings listed in this baseline.")
			return
		}
		content, _ := json.Marshal(map[string]any{"items": items, "tie_out": tie})
		saved, _ := json.Marshal(notes)
		var id string
		if err = tx.QueryRowContext(r.Context(), `INSERT INTO opening_baselines(organization_id,close_id,digest,content,notes,reviewed_by) VALUES($1,$2::bigint,$3,$4,$5,$6) RETURNING id::text`, org, r.PathValue("id"), digest, string(content), string(saved), who).Scan(&id); err != nil || tx.Commit() != nil {
			problem(w, 503, "The opening baseline could not be saved.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": id})
	})
	mux.HandleFunc("POST /v1/closes/{id}/opening-baseline/resolve", func(w http.ResponseWriter, r *http.Request) {
		who, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var in struct {
			BaselineID string `json:"baseline_id"`
			Item       string `json:"item"`
			Reference  string `json:"reference"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Reference = strings.TrimSpace(in.Reference); utf8.RuneCountInString(in.Reference) < 5 || utf8.RuneCountInString(in.Reference) > 2000 {
			problem(w, 422, "Say how the item was resolved, for example the correcting entry or the client's confirmation (5–2,000 characters).")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Opening baselines unavailable.")
			return
		}
		defer tx.Rollback()
		var org string
		var notes []byte
		err = tx.QueryRowContext(r.Context(), `SELECT o.id FROM organizations o JOIN client_closes c ON c.organization_id=o.id WHERE c.id::text=$1 FOR SHARE OF o`, r.PathValue("id")).Scan(&org)
		if err == nil {
			err = tx.QueryRowContext(r.Context(), `SELECT b.notes FROM opening_baselines b JOIN client_closes c ON c.id=b.close_id WHERE b.id::text=$1 AND c.id::text=$2 FOR UPDATE OF c`, in.BaselineID, r.PathValue("id")).Scan(&notes)
		}
		if err != nil {
			problem(w, 404, "Opening baseline not found.")
			return
		}
		var noted map[string]string
		if json.Unmarshal(notes, &noted); noted[in.Item] == "" {
			problem(w, 422, "That item is not inherited cleanup of this baseline.")
			return
		}
		result, err := tx.ExecContext(r.Context(), `INSERT INTO opening_cleanup_resolutions(organization_id,baseline_id,item,reference,resolved_by) VALUES($1,$2::bigint,$3,$4,$5) ON CONFLICT (baseline_id,item) DO NOTHING`, org, in.BaselineID, in.Item, in.Reference, who)
		if err != nil {
			problem(w, 503, "The resolution could not be saved.")
			return
		}
		if n, _ := result.RowsAffected(); n == 0 {
			problem(w, 409, "That item is already resolved.")
			return
		}
		if tx.Commit() != nil {
			problem(w, 503, "The resolution could not be saved.")
			return
		}
		jsonReply(w, 200, map[string]bool{"resolved": true})
	})
}
