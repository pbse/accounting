package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

// The platform staff console (docs/admin-panel.md) lives under /v1/admin. Staff sign in as members
// of the WorkOS organization PLATFORM_ORGANIZATION_ID, which is never enrolled as a firm: its
// sessions cannot use firm routes, and firm sessions fail this verifier's pinned organization.
const (
	platformRead    = "platform:read"
	platformOperate = "platform:operate"
	platformContent = "platform:content"
	platformPlans   = "platform:plans"
)

var platformPermissions = []string{platformRead, platformOperate, platformContent, platformPlans}

type staffKey struct{}

// staffMember is who a staff request comes from.
type staffMember struct {
	Subject     string   `json:"subject"`
	Permissions []string `json:"permissions"`
	Mode        string   `json:"mode"` // "workos", or "local-development" without WorkOS
}

func staffOf(ctx context.Context) *staffMember {
	m, _ := ctx.Value(staffKey{}).(*staffMember)
	return m
}

func staffVerifier() *auth.Verifier {
	return &auth.Verifier{ClientID: os.Getenv("WORKOS_CLIENT_ID"), Issuer: os.Getenv("WORKOS_ISSUER"), Organization: os.Getenv("PLATFORM_ORGANIZATION_ID")}
}

// staffOnly lets a request through only from platform staff holding permission. With WorkOS the
// token must belong to the staff organization; without it (local development only) the workspace
// token stands in for a staff member with every platform permission.
func staffOnly(token, permission string, next http.HandlerFunc) http.HandlerFunc {
	verifier := staffVerifier()
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var member staffMember
		switch {
		case verifier.ClientID != "" && verifier.Organization == "":
			problem(w, 503, "Staff sign-in is not configured: set PLATFORM_ORGANIZATION_ID.")
			return
		case verifier.ClientID != "":
			claims, err := verifier.Authenticate(r, "")
			if err != nil {
				problem(w, 401, "Sign in as platform staff.")
				return
			}
			var granted []string
			for _, p := range claims.Permissions {
				if slices.Contains(platformPermissions, p) {
					granted = append(granted, p)
				}
			}
			member = staffMember{Subject: claims.Subject, Permissions: granted, Mode: "workos"}
		default:
			if _, err := verifier.Authenticate(r, token); err != nil {
				problem(w, 401, "Unlock local development access.")
				return
			}
			member = staffMember{Subject: "local-development", Permissions: platformPermissions, Mode: "local-development"}
		}
		if !slices.Contains(member.Permissions, permission) {
			problem(w, 403, "Your staff role does not include "+permission+".")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), staffKey{}, &member)))
	}
}

// adminRead runs fn as system work (every firm's rows) inside a read-only transaction, so a screen
// that reads cannot write, even by mistake.
func adminRead(ctx context.Context, db *sql.DB, fn func(context.Context, *sql.Tx) error) error {
	ctx = context.WithValue(ctx, systemKey{}, true)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// adminAction describes a staff write for the audit log.
type adminAction struct {
	Firm       string // the firm affected; "" for platform-level actions
	Action     string // for example "firm.suspend"
	TargetType string
	TargetID   string
	Reason     string // required: why the staff member acted
	Request    any    // the parameters, without secrets or client content
}

var errReasonRequired = errors.New("say why: a staff action needs a reason")

// adminAct runs fn as the affected firm, so row-level security still confines what it writes, with
// the staff member as the actor, and records the action in platform_audit in the same transaction.
// A refused action is recorded too, in its own transaction. Platform-level actions run as system
// work.
func adminAct(ctx context.Context, db *sql.DB, a adminAction, fn func(context.Context, *sql.Tx) error) error {
	member := staffOf(ctx)
	if member == nil {
		return errors.New("staff actions need a signed-in staff member")
	}
	if strings.TrimSpace(a.Reason) == "" {
		return errReasonRequired
	}
	request, err := json.Marshal(a.Request)
	if err != nil || string(request) == "null" {
		request = []byte("{}")
	}
	system := context.WithValue(ctx, systemKey{}, true)
	acting := system
	if a.Firm != "" {
		acting = auth.WithAgent(auth.WithActor(ctx, &auth.Claims{Organization: a.Firm, RegisteredClaims: jwt.RegisteredClaims{Subject: "staff:" + member.Subject}}), "staff:"+member.Subject)
	}
	record := func(ctx context.Context, q interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	}, result, failure string) error {
		_, err := q.ExecContext(ctx, `INSERT INTO platform_audit(staff_user,action,organization_id,target_type,target_id,reason,request,result,error) VALUES($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,$9)`,
			member.Subject, a.Action, a.Firm, a.TargetType, a.TargetID, a.Reason, string(request), result, failure)
		return err
	}
	tx, err := db.BeginTx(acting, nil)
	if err == nil {
		defer tx.Rollback()
		if err = fn(acting, tx); err == nil {
			if err = record(acting, tx, "ok", ""); err == nil {
				err = tx.Commit()
			}
		}
	}
	if err != nil {
		if e := record(system, db, "refused", err.Error()); e != nil {
			log.Printf("Staff action %s by %s could not be audited as refused: %v", a.Action, member.Subject, e)
		}
	}
	return err
}

func registerAdmin(mux *http.ServeMux, db *sql.DB, token string) {
	read := func(next http.HandlerFunc) http.HandlerFunc {
		return staffOnly(token, platformRead, func(w http.ResponseWriter, r *http.Request) {
			if db == nil {
				problem(w, 503, "The admin panel needs a configured database.")
				return
			}
			next(w, r)
		})
	}
	mux.HandleFunc("GET /v1/admin/me", read(func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, staffOf(r.Context()))
	}))
	mux.HandleFunc("GET /v1/admin/overview", read(func(w http.ResponseWriter, r *http.Request) { adminOverview(w, r, db) }))
	mux.HandleFunc("GET /v1/admin/firms", read(func(w http.ResponseWriter, r *http.Request) { adminFirms(w, r, db) }))
	mux.HandleFunc("GET /v1/admin/firms/{id}", read(func(w http.ResponseWriter, r *http.Request) { adminFirm(w, r, db) }))
}

// adminOverview is the platform's health at a glance.
func adminOverview(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	var heartbeats, runs []byte
	var oldestQueued sql.NullTime
	var unknownEffects, firms, suspended, reconnect, failedPreparation int
	err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT
 COALESCE((SELECT jsonb_agg(jsonb_build_object('loop',loop,'instance',instance,'beat_at',beat_at,'stale',beat_at<now()-interval '5 minutes') ORDER BY loop) FROM worker_heartbeats),'[]'::jsonb),
 COALESCE((SELECT jsonb_object_agg(state,n) FROM (SELECT state,count(*) AS n FROM workflow_runs WHERE state IN ('queued','running','waiting') OR finished_at>now()-interval '24 hours' GROUP BY state) s),'{}'::jsonb),
 (SELECT min(created_at) FROM workflow_runs WHERE state='queued'),
 (SELECT count(*) FROM workflow_steps WHERE state='unknown'),
 (SELECT count(*) FROM organizations WHERE id<>'local-development'),
 (SELECT count(*) FROM organizations WHERE id<>'local-development' AND NOT enabled),
 (SELECT count(*) FROM quickbooks_connections WHERE state='reconnect_required')+(SELECT count(*) FROM xero_connections WHERE state='reconnect_required'),
 (SELECT count(*) FROM preparation_jobs WHERE state='failed' AND state_changed_at>now()-interval '24 hours')`).Scan(
			&heartbeats, &runs, &oldestQueued, &unknownEffects, &firms, &suspended, &reconnect, &failedPreparation)
	})
	if err != nil {
		log.Printf("Admin overview could not be read: %v", err)
		problem(w, 503, "The overview is unavailable.")
		return
	}
	var oldest any
	if oldestQueued.Valid {
		oldest = oldestQueued.Time
	}
	jsonReply(w, 200, map[string]any{
		"heartbeats": json.RawMessage(heartbeats), "runs": json.RawMessage(runs), "oldest_queued_at": oldest,
		"unknown_effects": unknownEffects, "firms": firms, "suspended_firms": suspended,
		"reconnect_required": reconnect, "failed_preparation_24h": failedPreparation,
	})
}

type adminFirmRow struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Enabled      bool    `json:"enabled"`
	CreatedAt    string  `json:"created_at"`
	Members      int     `json:"members"`
	Clients      int     `json:"clients"`
	Closes       int     `json:"closes"`
	ActiveRuns   int     `json:"active_runs"`
	FailedRuns7d int     `json:"failed_runs_7d"`
	LastActivity *string `json:"last_activity"`
}

const adminFirmColumns = `o.id,o.name,o.enabled,o.created_at::text,
 (SELECT count(*) FROM current_organization_members m WHERE m.organization_id=o.id AND m.granted),
 (SELECT count(DISTINCT lower(c.entity)) FROM client_closes c WHERE c.organization_id=o.id),
 (SELECT count(*) FROM client_closes c WHERE c.organization_id=o.id),
 (SELECT count(*) FROM workflow_runs r WHERE r.organization_id=o.id AND r.state IN ('queued','running','waiting')),
 (SELECT count(*) FROM workflow_runs r WHERE r.organization_id=o.id AND r.state='failed' AND r.finished_at>now()-interval '7 days'),
 GREATEST((SELECT max(created_at) FROM client_closes WHERE organization_id=o.id),(SELECT max(updated_at) FROM workflow_runs WHERE organization_id=o.id))::text`

func scanFirm(row interface{ Scan(...any) error }) (adminFirmRow, error) {
	var f adminFirmRow
	var last sql.NullString
	err := row.Scan(&f.ID, &f.Name, &f.Enabled, &f.CreatedAt, &f.Members, &f.Clients, &f.Closes, &f.ActiveRuns, &f.FailedRuns7d, &last)
	if last.Valid {
		f.LastActivity = &last.String
	}
	return f, err
}

// adminFirms lists firms, newest first, optionally filtered by a fragment of the name or ID.
func adminFirms(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	search := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(search) > 200 {
		problem(w, 422, "The search is too long.")
		return
	}
	firms := []adminFirmRow{}
	err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+adminFirmColumns+` FROM organizations o
 WHERE o.id<>'local-development' AND ($1='' OR position(lower($1) in lower(o.name))>0 OR position(lower($1) in lower(o.id))>0)
 ORDER BY o.created_at DESC LIMIT 200`, search)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			f, err := scanFirm(rows)
			if err != nil {
				return err
			}
			firms = append(firms, f)
		}
		return rows.Err()
	})
	if err != nil {
		log.Printf("Admin firms could not be read: %v", err)
		problem(w, 503, "Firms are unavailable.")
		return
	}
	jsonReply(w, 200, map[string]any{"firms": firms})
}

// adminFirm is one firm: its summary, members, recent workflow runs and latest health check.
func adminFirm(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	id := r.PathValue("id")
	var firm adminFirmRow
	var members, runs, health []byte
	err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		if firm, err = scanFirm(tx.QueryRowContext(ctx, `SELECT `+adminFirmColumns+` FROM organizations o WHERE o.id=$1`, id)); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT
 COALESCE((SELECT jsonb_agg(jsonb_build_object('subject',subject,'granted',granted,'decided_by',actor,'reason',reason,'decided_at',created_at) ORDER BY granted DESC,subject) FROM current_organization_members WHERE organization_id=$1),'[]'::jsonb),
 COALESCE((SELECT jsonb_agg(jsonb_build_object('id',id,'workflow',workflow,'state',state,'started_by',started_by,'created_at',created_at,'finished_at',finished_at,'error',error) ORDER BY id DESC)
   FROM (SELECT * FROM workflow_runs WHERE organization_id=$1 ORDER BY id DESC LIMIT 20) r),'[]'::jsonb),
 COALESCE((SELECT jsonb_build_object('run_id',id,'finished_at',finished_at,'report',output) FROM workflow_runs WHERE organization_id=$1 AND workflow='firm_health_check' AND state='completed' ORDER BY finished_at DESC LIMIT 1),'null'::jsonb)`, id).Scan(&members, &runs, &health)
	})
	if errors.Is(err, sql.ErrNoRows) || (err == nil && id == "local-development") {
		problem(w, 404, "Firm not found.")
		return
	}
	if err != nil {
		log.Printf("Admin firm %s could not be read: %v", id, err)
		problem(w, 503, "The firm is unavailable.")
		return
	}
	jsonReply(w, 200, map[string]any{"firm": firm, "members": json.RawMessage(members), "runs": json.RawMessage(runs), "health": json.RawMessage(health)})
}

// RunHeartbeat records every 30 seconds that loop is alive, for the admin overview.
func RunHeartbeat(ctx context.Context, db *sql.DB, loop string) {
	instance, _ := os.Hostname()
	for {
		if _, err := db.ExecContext(ctx, `INSERT INTO worker_heartbeats(loop,instance,beat_at) VALUES($1,$2,now()) ON CONFLICT (loop) DO UPDATE SET instance=$2,beat_at=now()`, loop, instance); err != nil && ctx.Err() == nil {
			log.Printf("Heartbeat %s could not be recorded: %v", loop, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
	}
}
