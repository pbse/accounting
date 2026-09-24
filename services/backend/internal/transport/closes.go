package transport

import (
	"close.local/backend/internal/loans"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strings"
	"time"

	"close.local/backend/internal/accruals"
	"close.local/backend/internal/auth"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/reconciliation"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type savedClose struct {
	ID        string `json:"id"`
	Entity    string `json:"entity"`
	Period    string `json:"period"`
	DueOn     string `json:"due_on,omitempty"`
	Priority  string `json:"priority,omitempty"`
	Assigned  bool   `json:"assigned,omitempty"`
	OwnedByMe bool   `json:"owned_by_me,omitempty"`
	// Basis is the client's approved accounting basis; BasisScope says whether a client override sets it.
	Basis      string `json:"basis,omitempty"`
	BasisScope string `json:"basis_scope,omitempty"`
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

// The authenticated organization owns every close and its child records.
func savedCloses(db *sql.DB, token string) http.Handler {
	verifier := workspaceVerifier()
	mux := http.NewServeMux()
	registerPosting(mux, db)
	registerVendorCredit(mux, db)
	registerBillCorrection(mux, db)
	registerProductionPosting(mux, db)
	registerEvidence(mux, db)
	registerForwardedEvidence(mux, db)
	registerReview(mux, db)
	registerRequests(mux, db)
	registerWork(mux, db)
	registerCarry(mux, db)
	registerRecurring(mux, db)
	registerRecurringAccrual(mux, db)
	registerReadiness(mux, db)
	registerPlanning(mux, db)
	registerEffort(mux, db)
	registerIndependence(mux, db)
	registerProcedures(mux, db)
	registerProcedureLearning(mux, db)
	registerAccess(mux, db)
	registerMembers(mux, db)
	registerOrganizationRevocation(mux, db)
	registerRetention(mux, db)
	registerOperations(mux, db)
	registerEmail(mux, db)
	registerEmailReplies(mux, db)
	registerCoordinator(mux, db)
	registerCoverage(mux, db)
	registerAttachments(mux, db)
	registerQuickBooks(mux, db)
	registerQuickBooksEntities(mux, db)
	mux.HandleFunc("GET /v1/closes/{id}/export", exportClose(db, false))
	mux.HandleFunc("GET /v1/closes/{id}/binder", exportClose(db, true))
	mux.HandleFunc("GET /v1/closes", func(w http.ResponseWriter, r *http.Request) {
		search := strings.TrimSpace(r.URL.Query().Get("q"))
		if len(search) > 200 {
			problem(w, 422, "Client search is too long.")
			return
		}
		rows, err := db.QueryContext(r.Context(), `SELECT id::text,entity,period,`+closePlanning+`$3 FROM client_closes WHERE organization_id=$1 AND ($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=client_closes.organization_id AND g.subject=$3 AND lower(g.client_entity)=lower(client_closes.entity) AND g.granted)) AND ($4='' OR entity ILIKE '%'||$4||'%' OR period=$4) ORDER BY period DESC,id DESC LIMIT 101`, organization(r), firmAccess(r), subject(r), search)
		if err != nil {
			problem(w, 503, "Saved closes are unavailable.")
			return
		}
		defer rows.Close()
		items := []savedClose{}
		for rows.Next() {
			var item savedClose
			if rows.Scan(&item.ID, &item.Entity, &item.Period, &item.DueOn, &item.Priority, &item.Assigned, &item.OwnedByMe) != nil {
				problem(w, 503, "Could not read saved closes.")
				return
			}
			items = append(items, item)
		}
		if rows.Err() != nil {
			problem(w, 503, "Could not read saved closes.")
			return
		}
		more := len(items) > 100
		if more {
			items = items[:100]
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
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Close storage unavailable.")
			return
		}
		defer tx.Rollback()
		var limit, count int
		err = tx.QueryRowContext(r.Context(), `SELECT max_closes FROM organizations WHERE id=$1 AND enabled FOR UPDATE`, organization(r)).Scan(&limit)
		if err == nil {
			err = tx.QueryRowContext(r.Context(), `SELECT count(*) FROM client_closes WHERE organization_id=$1`, organization(r)).Scan(&count)
		}
		if err != nil {
			problem(w, 503, "Firm capacity is unavailable.")
			return
		}
		var item savedClose
		err = tx.QueryRowContext(r.Context(), `SELECT id::text,entity,period FROM client_closes WHERE organization_id=$1 AND lower(entity)=lower($2) AND period=$3`, organization(r), input.Entity, input.Period).Scan(&item.ID, &item.Entity, &item.Period)
		if err != nil && err != sql.ErrNoRows {
			problem(w, 503, "Could not check the current close.")
			return
		}
		if err == sql.ErrNoRows {
			if count >= limit {
				problem(w, 429, "This firm has reached its close limit. Archive or retain existing work according to policy before opening another period.")
				return
			}
			err = tx.QueryRowContext(r.Context(), `INSERT INTO client_closes(entity,period,organization_id) VALUES($1,$2,$3) RETURNING id::text,entity,period`, input.Entity, input.Period, organization(r)).Scan(&item.ID, &item.Entity, &item.Period)
		}
		if err != nil {
			problem(w, 503, "Could not save the close. Retry safely with the same client and month.")
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
		err := db.QueryRowContext(r.Context(), `SELECT id::text,entity,period,`+closePlanning+`$2,COALESCE(b.method,''),COALESCE(b.scope,'') FROM client_closes LEFT JOIN LATERAL (SELECT p.method,CASE WHEN p.client_entity<>'' THEN 'client' ELSE 'firm' END scope FROM procedure_versions p WHERE p.organization_id=client_closes.organization_id AND p.kind='basis' AND (p.client_entity='' OR lower(p.client_entity)=lower(client_closes.entity)) ORDER BY (p.client_entity<>'') DESC,p.id DESC LIMIT 1) b ON true WHERE id::text=$1`, r.PathValue("id"), subject(r)).Scan(&item.ID, &item.Entity, &item.Period, &item.DueOn, &item.Priority, &item.Assigned, &item.OwnedByMe, &item.Basis, &item.BasisScope)
		if err == sql.ErrNoRows {
			problem(w, 404, "Close not found.")
			return
		}
		if err != nil {
			problem(w, 503, "Could not open the close.")
			return
		}
		rows, err := db.QueryContext(r.Context(), `SELECT id::text,kind,created_at,paper,digest,review_state,review_id FROM workpaper_review_state WHERE close_id=$1 ORDER BY id DESC LIMIT 101`, item.ID)
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
			problem(w, 409, err.Error())
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
	scoped := organizationBoundary(db, mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if (len(token) < 32 && verifier.ClientID == "") || db == nil {
			problem(w, 503, "Saved closes require a configured database and workspace authentication.")
			return
		}
		actor, err := verifier.Authenticate(r, token)
		if err != nil {
			problem(w, 401, "Sign in with an authorized workspace account, or unlock local development access.")
			return
		}
		r = r.WithContext(auth.WithActor(r.Context(), actor))
		scoped.ServeHTTP(w, r)
	})
}
func ConfiguredAPI() (http.Handler, func(), error) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return API(), func() {}, nil
	}
	if os.Getenv("WORKOS_CLIENT_ID") != "" {
		if !strings.HasPrefix(os.Getenv("WORKOS_ISSUER"), "https://") {
			return nil, nil, fmt.Errorf("WorkOS requires an exact HTTPS token issuer")
		}
	} else if len(os.Getenv("WORKSPACE_TOKEN")) < 32 {
		return nil, nil, fmt.Errorf("WORKSPACE_TOKEN must contain at least 32 characters when DATABASE_URL is set")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid database configuration")
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	return apiWithStore(db, os.Getenv("WORKSPACE_TOKEN")), func() { db.Close() }, nil
}

func workspaceVerifier() *auth.Verifier {
	return &auth.Verifier{ClientID: os.Getenv("WORKOS_CLIENT_ID"), Organization: os.Getenv("WORKOS_ORGANIZATION_ID"), Issuer: os.Getenv("WORKOS_ISSUER")}
}
