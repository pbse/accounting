package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// Content grants in the staff console (docs/admin-panel.md, phase A4): a staff member with platform:content opens
// time-boxed access to one firm's client content, with a reason, instead of giving one for every reveal. While it lasts,
// reveals on that firm's screens need no reason; each is still recorded, naming the grant. Screens that show several
// firms at once (the audit search, plans and people) still ask every time.
func registerAdminGrants(mux *http.ServeMux, db *sql.DB, token string) {
	content := func(next func(http.ResponseWriter, *http.Request, *sql.DB)) http.HandlerFunc {
		return adminGuard(token, db, platformContent, func(w http.ResponseWriter, r *http.Request) { next(w, r, db) })
	}
	mux.HandleFunc("GET /v1/admin/grants", content(adminGrants))
	mux.HandleFunc("POST /v1/admin/firms/{id}/grant", content(adminOpenGrant))
	mux.HandleFunc("POST /v1/admin/grants/{grant}/end", content(adminEndGrant))
}

// grantMinutes are the lengths a grant can have.
var grantMinutes = []int{15, 30, 60, 120, 240, 480}

// activeGrantReason is the reason a reveal records under the staff member's open grant for the firm, or "" without one.
func activeGrantReason(ctx context.Context, db *sql.DB, staff, firm string) string {
	var id int64
	var reason string
	err := db.QueryRowContext(context.WithValue(ctx, systemKey{}, true), `SELECT id,reason FROM content_grants WHERE staff_user=$1 AND organization_id=$2 AND ended_at IS NULL AND expires_at>now()
 ORDER BY expires_at DESC LIMIT 1`, staff, firm).Scan(&id, &reason)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("Under content grant #%d: %s", id, reason)
}

// adminGrants is the staff member's own open grants.
func adminGrants(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	var raw []byte
	err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',g.id,'organization',g.organization_id,'firm_name',o.name,'reason',g.reason,'expires_at',g.expires_at) ORDER BY g.expires_at),'[]'::jsonb)
 FROM content_grants g JOIN organizations o ON o.id=g.organization_id WHERE g.staff_user=$1 AND g.ended_at IS NULL AND g.expires_at>now()`, staffOf(ctx).Subject).Scan(&raw)
	})
	if err != nil {
		adminFirmProblem(w, err, "grants read")
		return
	}
	jsonReply(w, 200, map[string]any{"grants": json.RawMessage(raw)})
}

// adminOpenGrant opens a grant for the firm, replacing the staff member's open one for it.
func adminOpenGrant(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	firm := r.PathValue("id")
	var in struct {
		Minutes int    `json:"minutes"`
		Reason  string `json:"reason"`
	}
	if !adminReason(w, r, &in, func() string { return in.Reason }) {
		return
	}
	if !slices.Contains(grantMinutes, in.Minutes) {
		problem(w, 422, "A grant lasts 15, 30, 60, 120, 240 or 480 minutes.")
		return
	}
	if err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error { return adminFirmExists(ctx, tx, firm) }); err != nil {
		adminFirmProblem(w, err, "firm read")
		return
	}
	staff, reason := staffOf(r.Context()).Subject, strings.TrimSpace(in.Reason)
	request := map[string]any{"minutes": in.Minutes}
	var id int64
	var expires string
	err := adminAct(r.Context(), db, adminAction{Firm: firm, Action: "content.grant", TargetType: "firm", TargetID: firm, Reason: reason, Request: request},
		func(ctx context.Context, tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `UPDATE content_grants SET ended_at=now() WHERE staff_user=$1 AND organization_id=$2 AND ended_at IS NULL`, staff, firm); err != nil {
				return err
			}
			err := tx.QueryRowContext(ctx, `INSERT INTO content_grants(staff_user,organization_id,reason,expires_at) VALUES($1,$2,$3,now()+make_interval(mins=>$4)) RETURNING id,expires_at::text`,
				staff, firm, reason, in.Minutes).Scan(&id, &expires)
			request["grant_id"] = id
			return err
		})
	if err != nil {
		adminFirmProblem(w, err, "grant")
		return
	}
	jsonReply(w, 200, map[string]any{"grant_id": id, "expires_at": expires})
}

var errNoOpenGrant = errors.New("no such open grant of yours")

// adminEndGrant ends one of the staff member's open grants early.
func adminEndGrant(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	id, err := strconv.ParseInt(r.PathValue("grant"), 10, 64)
	if err != nil {
		problem(w, 404, "Grant not found.")
		return
	}
	staff := staffOf(r.Context()).Subject
	var firm string
	err = adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT organization_id FROM content_grants WHERE id=$1 AND staff_user=$2 AND ended_at IS NULL AND expires_at>now()`, id, staff).Scan(&firm)
	})
	if errors.Is(err, sql.ErrNoRows) {
		problem(w, 404, "You hold no such open grant.")
		return
	}
	if err != nil {
		adminFirmProblem(w, err, "grant read")
		return
	}
	err = adminAct(r.Context(), db, adminAction{Firm: firm, Action: "content.grant_end", TargetType: "grant", TargetID: strconv.FormatInt(id, 10), Reason: "Ended early"},
		func(ctx context.Context, tx *sql.Tx) error {
			result, err := tx.ExecContext(ctx, `UPDATE content_grants SET ended_at=now() WHERE id=$1 AND staff_user=$2 AND ended_at IS NULL`, id, staff)
			if err != nil {
				return err
			}
			if n, _ := result.RowsAffected(); n != 1 {
				return errNoOpenGrant
			}
			return nil
		})
	if errors.Is(err, errNoOpenGrant) {
		problem(w, 404, "You hold no such open grant.")
		return
	}
	if err != nil {
		adminFirmProblem(w, err, "grant end")
		return
	}
	jsonReply(w, 200, map[string]bool{"ended": true})
}
