package transport

import (
	"bytes"
	"close.local/backend/internal/bookfiles"
	"close.local/backend/internal/llm"
	"close.local/backend/internal/loans"
	"close.local/backend/internal/owners"
	"close.local/backend/internal/payroll"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"close.local/backend/internal/accruals"
	"close.local/backend/internal/auth"
	"close.local/backend/internal/documents"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/reconciliation"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type savedClose struct {
	ID     string `json:"id"`
	Entity string `json:"entity"`
	// Client is the client's own name in lower case (a confirmed alias resolves to it), so a list groups a client's
	// closes under one row whatever name each was opened with; SignedOff says its sign-off is recorded.
	Client    string `json:"client,omitempty"`
	SignedOff bool   `json:"signed_off,omitempty"`
	// Unchecked says a recorded sign-off could not be rechecked: neither standing nor known to have changed.
	Unchecked bool `json:"unchecked,omitempty"`
	// Rechecked says SignedOff was confirmed against the close as it is now (the last three months' closes).
	Rechecked bool   `json:"rechecked,omitempty"`
	Period    string `json:"period"`
	DueOn     string `json:"due_on,omitempty"`
	Priority  string `json:"priority,omitempty"`
	Assigned  bool   `json:"assigned,omitempty"`
	OwnedByMe bool   `json:"owned_by_me,omitempty"`
	// Owner is who holds the close (a subject), on the list of closes; a firm administrator sees whom it names.
	Owner string `json:"owner,omitempty"`
	// CanAssign says the reader may hand the close to a teammate: they hold it, nobody does, or they are a firm administrator.
	CanAssign bool `json:"can_assign,omitempty"`
	// CanTakeOver says the reader may take the close from the colleague who holds it: a firm administrator who reviews work.
	CanTakeOver bool `json:"can_take_over,omitempty"`
	// CanSignOff says the reader's role may sign the close off (close:signoff), as the sign-off route checks.
	CanSignOff bool `json:"can_sign_off,omitempty"`
	// Handed is how the reader came to hold the close when someone else handed it to them: who, when, and their note.
	Handed *closeHandover `json:"handed,omitempty"`
	// Basis is the client's approved accounting basis; BasisScope says whether a client override sets it.
	Basis      string               `json:"basis,omitempty"`
	BasisScope string               `json:"basis_scope,omitempty"`
	Profile    *preparation.Profile `json:"profile,omitempty"`
}

type closeHandover struct {
	By   string    `json:"by"`
	Note string    `json:"note"`
	At   time.Time `json:"at"`
}

const closePlanning = `COALESCE(due_on::text,''),priority,owner<>'',owner<>'' AND owner=`

type savedPaper struct {
	Digest      string `json:"digest"`
	ReviewState string `json:"review_state"`
	ReviewID    string `json:"review_id"`

	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	CreatedAt time.Time       `json:"created_at"`
	Paper     json.RawMessage `json:"paper"`
}

func jsonReply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}
func problem(w http.ResponseWriter, status int, message string) {
	code := map[int]string{401: "UNAUTHENTICATED", 403: "SCOPE_DENIED", 404: "NOT_FOUND", 409: "CONFLICT", 410: "EXPIRED", 429: "LIMIT_REACHED"}[status]
	if code == "" && status >= 500 {
		code = "UNAVAILABLE"
	} else if code == "" {
		code = "INVALID_REQUEST"
	}
	problemCode(w, status, code, message)
}

// problemCode names a condition the branch proves, such as SOURCE_STALE or EXECUTION_UNKNOWN.
func problemCode(w http.ResponseWriter, status int, code, message string) {
	jsonReply(w, status, map[string]string{"code": code, "message": message})
}
func jobProblem(w http.ResponseWriter, err error, message string) {
	if err != nil && strings.Contains(err.Error(), "Firm active job limit reached") {
		problem(w, 429, "This firm has reached its active preparation limit. Resolve or cancel existing work before starting more.")
		return
	}
	problem(w, 503, message)
}
func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		problem(w, 415, "Send application/json.")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 512*1024)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(value) != nil || d.Decode(new(any)) != io.EOF {
		problem(w, 400, "Send one valid JSON object, at most 512 KiB, with only supported fields.")
		return false
	}
	return true
}
func prepareReconciliation(w http.ResponseWriter, r *http.Request) {
	var input reconciliation.Input
	if !decode(w, r, &input) {
		return
	}
	paper, err := reconciliation.Prepare(input)
	if err != nil {
		problem(w, 422, err.Error())
		return
	}
	jsonReply(w, 200, paper)
}

// openClose opens the close of entity for period inside tx, or returns the one already open: the firm's capacity,
// its roster, confirmed aliases and the client's opening month are checked as the Clients page checks them, and a new
// month takes the firm's close template. created says a close was inserted. A refusal carries the status and message
// the route answers with; the caller commits.
func openClose(ctx context.Context, tx *sql.Tx, org, entity, period string, roles func(context.Context) map[string]reviewer) (item savedClose, created bool, refused *closeActionError) {
	var err error
	var limit, count int
	err = tx.QueryRowContext(ctx, `SELECT max_closes FROM organizations WHERE id=$1 AND enabled FOR UPDATE`, org).Scan(&limit)
	if err == nil {
		err = tx.QueryRowContext(ctx, `SELECT count(*) FROM client_closes WHERE organization_id=$1`, org).Scan(&count)
	}
	if err != nil {
		return item, false, &closeActionError{status: 503, message: "Firm capacity is unavailable."}
	}
	err = tx.QueryRowContext(ctx, `SELECT id::text,entity,period FROM client_closes WHERE organization_id=$1 AND lower(entity)=lower($2) AND period=$3`, org, entity, period).Scan(&item.ID, &item.Entity, &item.Period)
	if err != nil && err != sql.ErrNoRows {
		return item, false, &closeActionError{status: 503, message: "Could not check the current close."}
	}
	if err == sql.ErrNoRows {
		if count >= limit {
			return item, false, &closeActionError{status: 429, message: "This firm has reached its close limit. Archive or retain existing work according to policy before opening another period."}
		}
		// A client the firm's roster says has left owes no new month; its existing months still open.
		var left bool
		if err = tx.QueryRowContext(ctx, `SELECT `+rosterLeft("$1", "$2"), org, entity).Scan(&left); err != nil {
			return item, false, &closeActionError{status: 503, message: "Could not check the current close."}
		}
		if left {
			return item, false, &closeActionError{status: 409, message: entity + " is inactive on the firm's roster. A firm administrator reactivates it under Settings → Clients before a new month is opened."}
		}
		// A client's first close in the firm is its opening month. A month opened earlier than that one, before its
		// baseline is reviewed, becomes the opening month instead. A name that is a confirmed alias of more than one
		// client is not guessed.
		var clients int
		var first bool
		var earlier string
		err = tx.QueryRowContext(ctx, `SELECT (SELECT count(DISTINCT lower(client_entity)) FROM client_aliases WHERE organization_id=$1 AND alias=$3 AND retired_at IS NULL AND lower(client_entity)<>lower($2)),
 NOT EXISTS(SELECT 1 FROM client_closes c WHERE `+sameClient+`),
 COALESCE((SELECT c.id::text FROM client_closes c WHERE `+sameClient+` AND c.takeover AND c.period>$4 LIMIT 1),'')`, org, entity, documents.NormalizeEntity(entity), period).Scan(&clients, &first, &earlier)
		if err == nil && clients > 1 {
			return item, false, &closeActionError{status: 409, message: "This name is a confirmed alias of more than one client. Open the close under the client's own name."}
		}
		if err == nil && earlier != "" {
			var moved sql.Result
			if moved, err = tx.ExecContext(ctx, `UPDATE client_closes c SET takeover=false WHERE c.id::text=$1 AND NOT EXISTS(SELECT 1 FROM opening_baselines b WHERE b.close_id=c.id) AND NOT EXISTS(SELECT 1 FROM takeover_setups s WHERE s.close_id=c.id)`, earlier); err == nil {
				n, _ := moved.RowsAffected()
				first = n > 0
			}
		}
		if err == nil {
			err = tx.QueryRowContext(ctx, `INSERT INTO client_closes(entity,period,organization_id,takeover) VALUES($1,$2,$3,$4) RETURNING id::text,entity,period`, entity, period, org, first).Scan(&item.ID, &item.Entity, &item.Period)
		}
		// A new month of a roster client takes the firm's monthly close template: its due date and owner.
		if err == nil {
			created = true
			err = applyCloseTemplate(ctx, tx, item.ID, roles)
		}
	}
	if err != nil {
		return item, false, &closeActionError{status: 503, message: "Could not save the close. Retry safely with the same client and month."}
	}
	return item, created, nil
}

// The authenticated organization owns every close and its child records.
// sessionStore, when given, is the pool that holds sign-in sessions (apiWithStores keeps it apart from the firm's);
// the attention stream rechecks a session on it. Without one the firm's pool serves, as tests and tools do.
func savedCloses(db *sql.DB, token string, sessionStore ...*sql.DB) http.Handler {
	verifier := workspaceVerifier()
	sessionDB := db
	if len(sessionStore) > 0 && sessionStore[0] != nil {
		sessionDB = sessionStore[0]
	}
	mux := http.NewServeMux()
	registerPosting(mux, db)
	registerVendorCredit(mux, db)
	registerBillCorrection(mux, db)
	registerProductionPosting(mux, db)
	registerXeroPosting(mux, db)
	registerXeroCorrections(mux, db)
	registerEvidence(mux, db)
	registerForwardedEvidence(mux, db)
	registerReview(mux, db)
	registerRequests(mux, db)
	registerRequestProposals(mux, db)
	registerWork(mux, db)
	registerCarry(mux, db)
	registerRecurring(mux, db)
	registerRecurringAccrual(mux, db)
	registerReadiness(mux, db)
	registerPlanning(mux, db)
	registerRoster(mux, db)
	registerClients(mux, db)
	registerImports(mux, db)
	registerAccountMaps(mux, db)
	registerCompanyLinks(mux, db)
	registerImportScopes(mux, db)
	registerImportAcceptance(mux, db)
	registerClientProfiles(mux, db)
	registerBooksSources(mux, db)
	registerLedgers(mux, db)
	registerClientConnections(mux, db)
	registerEffort(mux, db)
	registerIndependence(mux, db)
	registerAgentAccess(mux, db)
	registerProcedures(mux, db)
	registerProcedureLearning(mux, db)
	registerAccess(mux, db)
	registerMembers(mux, db)
	registerMemberRoles(mux, db)
	registerOrganizationRevocation(mux, db)
	registerRetention(mux, db)
	registerOperations(mux, db)
	registerEmail(mux, db)
	registerEmailSettlement(mux, db)
	registerEmailReplies(mux, db)
	registerCoordinator(mux, db)
	registerCoverage(mux, db)
	registerAttachments(mux, db)
	registerQuickBooks(mux, db)
	registerQuickBooksEntities(mux, db)
	registerXero(mux, db)
	registerStripe(mux, db)
	registerAging(mux, db)
	registerOpenItems(mux, db)
	registerCollections(mux, db)
	registerOpeningBaseline(mux, db)
	registerTakeoverSetup(mux, db)
	registerFirmExport(mux, db)
	registerCashPlan(mux, db)
	registerMatching(mux, db)
	registerIndexRates(mux, db)
	registerCreditApplications(mux, db)
	registerDrive(mux)
	registerDaily(mux, db)
	registerChat(mux, db)
	registerAttention(mux, db, sessionDB)
	registerMe(mux, db)
	registerHome(mux, db)
	registerSecurity(mux, db)
	registerPortfolio(mux, db)
	registerGoals(mux, db)
	registerRoutines(mux, db)
	registerPlanProposals(mux, db)
	registerHandoffs(mux, db)
	registerSetup(mux, db)
	registerHistoricalExports(mux, db)
	registerInvitations(mux, db)
	registerStatements(mux, db)
	registerCanvas(mux, db)
	registerSearch(mux, db)
	mux.HandleFunc("GET /v1/closes/{id}/export", exportClose(db, false))
	mux.HandleFunc("GET /v1/closes/{id}/binder", exportClose(db, true))
	mux.HandleFunc("GET /v1/closes/{id}/year-end", yearEndHandoff(db))
	mux.HandleFunc("GET /v1/closes/{id}/tie-out", tieOut(db))
	mux.HandleFunc("GET /v1/closes/{id}/flux", flux(db))
	mux.HandleFunc("GET /v1/closes/{id}/management-pack", managementReport(db))
	mux.HandleFunc("POST /v1/closes/{id}/cash-flow", importCashFlow(db))
	registerBudget(mux, db)
	registerManagementPublications(mux, db)
	registerExplanations(mux, db)
	registerExamples(mux, db)
	mux.HandleFunc("GET /v1/closes/{id}/workpapers/{paper}/standing-approval", standingApproval(db))
	mux.HandleFunc("POST /v1/closes/{id}/workpapers/{paper}/standing-approval", standingApproval(db))
	mux.HandleFunc("GET /v1/closes/{id}/work/{job}/hints", workHints(db))
	mux.HandleFunc("GET /v1/closes/{id}/memory", clientMemory(db))
	mux.HandleFunc("POST /v1/closes/{id}/memory", clientMemory(db))
	mux.HandleFunc("GET /v1/closes/{id}/brief", closeBrief(mux, db))
	mux.HandleFunc("POST /v1/closes/{id}/replay", replay(db))
	mux.HandleFunc("GET /v1/closes/{id}/replay", replay(db))
	// The reader's closes, newest first, matching a client name (its own or a confirmed alias), a month or nothing. A
	// recorded sign-off counts only while it still stands, as Home and the morning brief count it.
	mux.HandleFunc("GET /v1/closes", func(w http.ResponseWriter, r *http.Request) {
		search := strings.TrimSpace(r.URL.Query().Get("q"))
		if len(search) > 200 {
			problem(w, 422, "Client search is too long.")
			return
		}
		tx, err := db.BeginTx(r.Context(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			problem(w, 503, "Saved closes are unavailable.")
			return
		}
		defer tx.Rollback()
		rows, err := tx.QueryContext(r.Context(), `SELECT id::text,entity,period,`+closePlanning+`$3,client_identity(organization_id,entity),COALESCE((SELECT decision FROM close_signoffs s WHERE s.close_id=client_closes.id ORDER BY s.id DESC LIMIT 1),'')='signed_off',owner FROM client_closes WHERE organization_id=$1 AND ($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=client_closes.organization_id AND g.subject=$3 AND lower(g.client_entity)=lower(client_closes.entity) AND g.granted))
 AND ($4='' OR entity ILIKE '%'||$4||'%' OR period=$4 OR client_identity(organization_id,entity) ILIKE '%'||$4||'%' OR client_identity(organization_id,entity)=client_identity(organization_id,$4)) ORDER BY period DESC,client_closes.id DESC LIMIT 101`, organization(r), firmAccess(r), subject(r), search)
		if err != nil {
			problem(w, 503, "Saved closes are unavailable.")
			return
		}
		items := []savedClose{}
		for rows.Next() {
			var item savedClose
			if rows.Scan(&item.ID, &item.Entity, &item.Period, &item.DueOn, &item.Priority, &item.Assigned, &item.OwnedByMe, &item.Client, &item.SignedOff, &item.Owner) != nil {
				rows.Close()
				problem(w, 503, "Could not read saved closes.")
				return
			}
			items = append(items, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			problem(w, 503, "Could not read saved closes.")
			return
		}
		more := len(items) > 100
		if more {
			items = items[:100]
		}
		// The recheck reads the whole close, so the list makes it for the last three months' closes only, as the roster
		// does; an older recorded sign-off is returned as recorded (Rechecked false).
		since := shiftMonth(time.Now().UTC().Format("2006-01"), -2)
		for i := range items {
			if !items[i].SignedOff || items[i].Period < since {
				continue
			}
			items[i].Rechecked = true
			state, err := recheckSignOff(r.Context(), tx, items[i].ID)
			if err != nil {
				problem(w, 503, "Could not read saved closes.")
				return
			}
			items[i].SignedOff, items[i].Unchecked = state == "signed_off", state == "unknown"
		}
		jsonReply(w, 200, map[string]any{"closes": items, "has_more": more})
	})
	mux.HandleFunc("POST /v1/closes", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Entity string `json:"entity"`
			Period string `json:"period"`
		}
		if !decode(w, r, &input) {
			return
		}
		input.Entity = strings.TrimSpace(input.Entity)
		if _, err := time.Parse("2006-01", input.Period); err != nil || len(input.Entity) < 1 || len(input.Entity) > 200 {
			problem(w, 422, "Enter a client name (1–200 characters) and valid close month.")
			return
		}
		allowed, accessErr := clientAccess(db, r, input.Entity)
		if accessErr != nil {
			problem(w, 503, "Client access unavailable.")
			return
		}
		if !allowed {
			problem(w, 403, "You are not assigned to this client.")
			return
		}
		// The firm's roles, for the owner the template may give the close, are read before any lock is taken when the
		// firm names an owner; roles that cannot be read give it none (applyCloseTemplate).
		roles := templateRoles(organization(r))
		warmTemplateRoles(r.Context(), db, organization(r), roles)
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Close storage unavailable.")
			return
		}
		defer tx.Rollback()
		item, _, refused := openClose(r.Context(), tx, organization(r), input.Entity, input.Period, roles)
		if refused != nil {
			problem(w, refused.status, refused.message)
			return
		}
		if tx.Commit() != nil {
			problem(w, 503, "Could not confirm the close. Reopen this client and month before retrying.")
			return
		}
		jsonReply(w, 200, item)
	})
	mux.HandleFunc("GET /v1/closes/{id}", func(w http.ResponseWriter, r *http.Request) {
		var item savedClose
		err := db.QueryRowContext(r.Context(), `SELECT id::text,entity,period,`+closePlanning+`$2,COALESCE(b.method,''),COALESCE(b.scope,'') FROM client_closes LEFT JOIN LATERAL (SELECT p.method,CASE WHEN p.client_entity<>'' THEN 'client' ELSE 'firm' END scope FROM procedure_versions p WHERE p.organization_id=client_closes.organization_id AND p.kind='basis' AND (p.client_entity='' OR lower(p.client_entity)=lower(client_closes.entity)) AND (p.effective_from IS NULL OR p.effective_from<=client_closes.period) ORDER BY (p.client_entity<>'') DESC,p.id DESC LIMIT 1) b ON true WHERE id::text=$1`, r.PathValue("id"), subject(r)).Scan(&item.ID, &item.Entity, &item.Period, &item.DueOn, &item.Priority, &item.Assigned, &item.OwnedByMe, &item.Basis, &item.BasisScope)
		if err == sql.ErrNoRows {
			problem(w, 404, "Close not found.")
			return
		}
		if err != nil {
			problem(w, 503, "Could not open the close.")
			return
		}
		var signed bool
		if err = db.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT decision FROM close_signoffs WHERE close_id=$1 ORDER BY id DESC LIMIT 1),'')='signed_off'`, item.ID).Scan(&signed); err != nil {
			problem(w, 503, "Could not open the close.")
			return
		}
		item.CanAssign = !signed && subject(r) != "" && canHandOver(r) && (item.OwnedByMe || !item.Assigned || canManageMembers(r))
		item.CanSignOff = canSignOff(r)
		item.CanTakeOver = !signed && item.Assigned && !item.OwnedByMe && canTakeOver(r)
		if item.OwnedByMe {
			var h closeHandover
			var to string
			err = db.QueryRowContext(r.Context(), `SELECT actor,reason,created_at,to_owner FROM close_owner_changes WHERE close_id=$1 ORDER BY id DESC LIMIT 1`, item.ID).Scan(&h.By, &h.Note, &h.At, &to)
			if err != nil && err != sql.ErrNoRows {
				problem(w, 503, "Could not open the close.")
				return
			}
			if err == nil && to == subject(r) && h.By != to {
				item.Handed = &h
			}
		}
		rows, err := db.QueryContext(r.Context(), `SELECT id::text,kind,created_at,paper,digest,review_state,review_id FROM workpaper_review_state WHERE close_id=$1 ORDER BY workpaper_review_state.id DESC LIMIT 101`, item.ID)
		if err != nil {
			problem(w, 503, "Could not open workpapers.")
			return
		}
		defer rows.Close()
		papers := []savedPaper{}
		for rows.Next() {
			var paper savedPaper
			if rows.Scan(&paper.ID, &paper.Kind, &paper.CreatedAt, &paper.Paper, &paper.Digest, &paper.ReviewState, &paper.ReviewID) != nil {
				problem(w, 503, "Could not read workpapers.")
				return
			}
			papers = append(papers, paper)
		}
		if rows.Err() != nil {
			problem(w, 503, "Could not read workpapers.")
			return
		}
		more := len(papers) > 100
		if more {
			papers = papers[:100]
		}
		profile, err := preparation.ClientProfile(r.Context(), db, item.ID)
		if err != nil {
			problem(w, 503, "Could not read the client's currency.")
			return
		}
		item.Profile = &profile
		jsonReply(w, 200, map[string]any{"close": item, "workpapers": papers, "has_more": more})
	})
	mux.HandleFunc("GET /v1/closes/{id}/reconciliation/prior-items", func(w http.ResponseWriter, r *http.Request) {
		account := strings.TrimSpace(r.URL.Query().Get("account"))
		accountType := r.URL.Query().Get("account_type")
		if accountType == "" {
			accountType = "bank"
		}
		if account == "" || len(account) > 200 || (accountType != "bank" && accountType != "credit_card") {
			problem(w, 422, "Choose the account and its type to carry forward.")
			return
		}
		var entity, period, org string
		if err := db.QueryRowContext(r.Context(), `SELECT entity,period,organization_id FROM client_closes WHERE id::text=$1 AND organization_id=$2`, r.PathValue("id"), organization(r)).Scan(&entity, &period, &org); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		month, _ := time.Parse("2006-01", period)
		previous := month.AddDate(0, -1, 0).Format("2006-01")
		var paperID, priorCloseID, state string
		var raw []byte
		err := db.QueryRowContext(r.Context(), `SELECT w.id::text,c.id::text,w.review_state,w.paper FROM workpaper_review_state w JOIN client_closes c ON c.id=w.close_id WHERE c.organization_id=$1 AND lower(c.entity)=lower($2) AND c.period=$3 AND w.kind='reconciliation' AND lower(w.paper->'input'->>'account')=lower($4) AND COALESCE(NULLIF(w.paper->'input'->>'account_type',''),'bank')=$5 ORDER BY w.id DESC LIMIT 1`, org, entity, previous, account, accountType).Scan(&paperID, &priorCloseID, &state, &raw)
		if err == sql.ErrNoRows {
			jsonReply(w, 200, map[string]any{"items": []reconciliation.PriorItem{}, "message": "No prior-month reconciliation exists for this account."})
			return
		}
		if err != nil {
			problem(w, 503, "Could not read the previous reconciliation.")
			return
		}
		if state != "approved" {
			jsonReply(w, 200, map[string]any{"items": []reconciliation.PriorItem{}, "message": "The latest prior-month reconciliation needs accountant approval before carry-forward."})
			return
		}
		var prior reconciliation.Workpaper
		if json.Unmarshal(raw, &prior) != nil {
			problem(w, 503, "Could not read the approved prior workpaper.")
			return
		}
		items, err := reconciliation.CarryForwardItems(prior, "Approved "+previous+" reconciliation workpaper #"+paperID)
		if err != nil {
			problem(w, 409, err.Error())
			return
		}
		jsonReply(w, 200, map[string]any{"items": items, "prior_close_id": priorCloseID, "prior_workpaper_id": paperID, "message": "Review each carried item against the prior workpaper and current statement before confirming."})
	})
	mux.HandleFunc("POST /v1/closes/{id}/workpapers", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Kind  string          `json:"kind"`
			Input json.RawMessage `json:"input"`
		}
		if !decode(w, r, &input) {
			return
		}
		var paper any
		var entity, period string
		var err error
		switch input.Kind {
		case "prepaid":
			var value prepaids.Input
			err = preparation.Strict(input.Input, &value)
			if err == nil {
				paper, err = prepaids.Prepare(value)
			}
			entity = strings.TrimSpace(value.Entity)
			period = value.ClosePeriod
		case "accrual":
			var value accruals.Input
			err = preparation.Strict(input.Input, &value)
			if err == nil {
				paper, err = accruals.Prepare(value)
			}
			entity = strings.TrimSpace(value.Entity)
			period = value.ClosePeriod
		case "reconciliation":
			var value reconciliation.Input
			err = preparation.Strict(input.Input, &value)
			if err == nil {
				paper, err = reconciliation.Prepare(value)
			}
			entity = strings.TrimSpace(value.Entity)
			period = value.ClosePeriod
		case "owner":
			var value owners.Input
			err = preparation.Strict(input.Input, &value)
			if err == nil {
				paper, err = owners.Prepare(value)
			}
			entity = strings.TrimSpace(value.Entity)
			period = value.ClosePeriod
		case "payroll":
			var value payroll.Input
			err = preparation.Strict(input.Input, &value)
			if err == nil {
				paper, err = payroll.Prepare(value)
			}
			entity = strings.TrimSpace(value.Entity)
			period = value.ClosePeriod
		case "loan":
			var value loans.Input
			err = preparation.Strict(input.Input, &value)
			if err == nil {
				paper, err = loans.Prepare(value)
			}
			entity = strings.TrimSpace(value.Entity)
			period = value.ClosePeriod
		default:
			problem(w, 422, "Unsupported workpaper kind.")
			return
		}
		if err != nil {
			problem(w, 422, err.Error())
			return
		}
		body, err := json.Marshal(paper)
		if err != nil {
			problem(w, 500, "Could not encode the workpaper.")
			return
		}
		if err = preparation.CheckProcedureMethod(r.Context(), db, r.PathValue("id"), input.Kind, body); err != nil {
			var policy preparation.PolicyRequired
			if errors.As(err, &policy) {
				problemCode(w, 409, "POLICY_REQUIRED", err.Error())
				return
			}
			problem(w, 409, err.Error())
			return
		}
		if err = preparation.CheckIndexRates(r.Context(), db, input.Kind, body); err != nil {
			refuseIndex(w, err)
			return
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(body))
		var saved savedPaper
		err = db.QueryRowContext(r.Context(), `INSERT INTO workpapers(close_id,kind,digest,paper,created_by) SELECT id,$2,$3,$4,$7 FROM client_closes WHERE id::text=$1 AND entity=$5 AND period=$6 ON CONFLICT(close_id,kind,digest) DO UPDATE SET digest=workpapers.digest RETURNING id::text,kind,created_at,paper`, r.PathValue("id"), input.Kind, digest, string(body), entity, period, subject(r)).Scan(&saved.ID, &saved.Kind, &saved.CreatedAt, &saved.Paper)
		if err == sql.ErrNoRows {
			problem(w, 409, "The workpaper client and month must match the selected close.")
			return
		}
		if err != nil {
			problem(w, 503, "Save could not be confirmed. Retry the same draft safely.")
			return
		}
		jsonReply(w, 200, saved)
	})
	scoped := organizationBoundary(db, idempotent(db, mux))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if (len(token) < 32 && verifier.ClientID == "") || db == nil {
			problem(w, 503, "Saved closes require a configured database and workspace authentication.")
			return
		}
		// Only MCP tool dispatch sets an actor before this point, after its own verification and grant check.
		if auth.Actor(r.Context()) == nil {
			actor, err := verifier.Authenticate(r, token)
			if err != nil {
				problem(w, 401, "Sign in with an authorized workspace account, or unlock local development access.")
				return
			}
			r = r.WithContext(auth.WithActor(r.Context(), actor))
		}
		scoped.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = 200
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// idempotent runs a POST that carries an Idempotency-Key at most once per firm within 24 hours. Only a fingerprint
// of the request and its final status are kept, never the response, so a repeat is refused with a code saying what
// happened and the client re-reads current state. Some refusals follow real work, so every outcome holds its key.
func idempotent(db *sql.DB, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, keyed := r.Header["Idempotency-Key"]
		key := r.Header.Get("Idempotency-Key")
		if r.Method != "POST" || !keyed {
			next.ServeHTTP(w, r)
			return
		}
		if key == "" || len(key) > 255 || strings.IndexFunc(key, func(c rune) bool { return c < '!' || c > '~' }) >= 0 {
			problem(w, 422, "Use an Idempotency-Key of 1–255 visible ASCII characters.")
			return
		}
		// A client's books file may be up to 50 MiB, with its form around it; other requests are far smaller.
		limit := int64(32 << 20)
		if strings.HasSuffix(r.URL.Path, "/client/imports") {
			limit = bookfiles.MaxFile + (1 << 20)
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
		if err != nil {
			problem(w, 413, "The request body is too large.")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		fingerprint := preparation.Digest([]byte(strings.Join([]string{subject(r), r.URL.Path, r.URL.RawQuery, r.Header.Get("Content-Type"), string(body)}, "\x00")))
		org := organization(r)
		if _, err = db.ExecContext(r.Context(), `DELETE FROM idempotency_keys WHERE organization_id=$1 AND created_at<now()-interval '24 hours'`, org); err != nil {
			problem(w, 503, "Idempotency keys are unavailable.")
			return
		}
		var inserted bool
		err = db.QueryRowContext(r.Context(), `INSERT INTO idempotency_keys(organization_id,key,fingerprint) VALUES($1,$2,$3) ON CONFLICT DO NOTHING RETURNING true`, org, key, fingerprint).Scan(&inserted)
		if err == sql.ErrNoRows {
			var first string
			var status sql.NullInt64
			if err = db.QueryRowContext(r.Context(), `SELECT fingerprint,status FROM idempotency_keys WHERE organization_id=$1 AND key=$2`, org, key).Scan(&first, &status); err != nil {
				problem(w, 503, "Idempotency keys are unavailable.")
				return
			}
			switch {
			case first != fingerprint:
				problemCode(w, 422, "IDEMPOTENCY_KEY_REUSED", "This Idempotency-Key was used for a different request. Use a new key.")
			case !status.Valid || status.Int64 >= 500:
				problemCode(w, 409, "IDEMPOTENCY_IN_PROGRESS", "A request with this key is still running or its outcome is uncertain. Re-read the current state before trying again with a new key.")
			default:
				problemCode(w, 409, "IDEMPOTENT_REPLAY", fmt.Sprintf("This request already completed with HTTP %d and was not repeated. Re-read the current state.", status.Int64))
			}
			return
		}
		if err != nil {
			problem(w, 503, "Idempotency keys are unavailable.")
			return
		}
		recorder := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		// A client that disconnects after the work finished must not leave the key looking unfinished.
		persist, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		defer cancel()
		if _, err = db.ExecContext(persist, `UPDATE idempotency_keys SET status=$3 WHERE organization_id=$1 AND key=$2`, org, key, max(recorder.status, 200)); err != nil {
			log.Print("Idempotency receipt not saved; the key stays uncertain until it expires: ", err)
		}
	})
}

func ConfiguredAPI() (http.Handler, func(), error) {
	models, err := llm.FromEnv()
	if err != nil {
		return nil, nil, err
	}
	if _, err := serverSessions(); err != nil {
		return nil, nil, err
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return models.Bind(API()), func() {}, nil
	}
	if os.Getenv("WORKOS_CLIENT_ID") != "" {
		if !strings.HasPrefix(os.Getenv("WORKOS_ISSUER"), "https://") {
			return nil, nil, fmt.Errorf("WorkOS requires an exact HTTPS token issuer")
		}
	} else if len(os.Getenv("WORKSPACE_TOKEN")) < 32 {
		return nil, nil, fmt.Errorf("WORKSPACE_TOKEN must contain at least 32 characters when DATABASE_URL is set")
	}
	db, err := OpenTenantDB(url, "")
	if err != nil {
		return nil, nil, fmt.Errorf("invalid database configuration")
	}
	sessions, err := OpenTenantDB(url, "")
	if err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("invalid database configuration")
	}
	// A new connection to the database costs a TLS handshake and a password exchange, several round trips before the
	// first query, so the API keeps every connection it opens and opens them before the first request needs one. A
	// page asks for its data in parallel requests; each needs its own connection.
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(20)
	db.SetConnMaxIdleTime(0)
	db.SetConnMaxLifetime(time.Hour)
	sessions.SetMaxOpenConns(3)
	sessions.SetMaxIdleConns(3)
	sessions.SetConnMaxIdleTime(0)
	sessions.SetConnMaxLifetime(time.Hour)
	go warm(db, 8)
	go warm(sessions, 2)
	return models.Bind(apiWithStores(db, sessions, os.Getenv("WORKSPACE_TOKEN"))), func() { db.Close(); sessions.Close() }, nil
}

// warm opens up to n connections at once, each returned to the pool idle as soon as it is ready, so a request arriving
// during the warm-up can use the first one opened.
func warm(db *sql.DB, n int) {
	ctx, cancel := context.WithTimeout(systemContext(context.Background()), 30*time.Second)
	defer cancel()
	var opening sync.WaitGroup
	for range n {
		opening.Go(func() {
			if conn, err := db.Conn(ctx); err == nil {
				conn.PingContext(ctx)
				conn.Close()
			}
		})
	}
	opening.Wait()
}

func workspaceVerifier() *auth.Verifier {
	return &auth.Verifier{ClientID: os.Getenv("WORKOS_CLIENT_ID"), Organization: os.Getenv("WORKOS_ORGANIZATION_ID"), Issuer: os.Getenv("WORKOS_ISSUER")}
}

// canSignOff says the reader may sign the close off as the sign-off route allows it: a reviewer (workpapers:review,
// which reviewActor requires) whose role also holds close:signoff; local development, with no organization, may.
func canSignOff(r *http.Request) bool {
	a := auth.Actor(r.Context())
	return a != nil && (a.Organization == "" || (slices.Contains(a.Permissions, "workpapers:review") && slices.Contains(a.Permissions, "close:signoff")))
}
