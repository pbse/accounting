package transport

import (
	"bytes"
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
	"github.com/golang-jwt/jwt/v5"
)

// Connecting through a request (docs/books-sources.md, Client-provided books; delivery step 12): a client asked to
// connect its books signs in to QuickBooks or Xero from the request's private link, without a login at the firm. The
// sign-in is the one a member makes from the client's page, under the request as its actor, and bound to it: it starts
// only from a live link, expires with that link at the latest, is checked against it again when the provider returns,
// and its state is single use. The provider's answer is finished on the server, and the connection it makes waits for
// a member who manages integrations to confirm the company (or choose the organisation), as every connection does. The
// request is marked by the ledger it asks to connect in its scope ("connect", with QuickBooks' environment).

// recorded keeps the reply a connection action wrote, for a caller that answers the client another way.
type recorded struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (r *recorded) Header() http.Header {
	if r.header == nil {
		r.header = http.Header{}
	}
	return r.header
}

func (r *recorded) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

func (r *recorded) Write(b []byte) (int, error) {
	r.WriteHeader(200)
	return r.body.Write(b)
}

// requestActor is who a connection made through request id acts as.
func requestActor(org, id string) *auth.Claims {
	return &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "client request #" + id}, Organization: org}
}

var connectNames = map[string]string{"quickbooks": "QuickBooks", "xero": "Xero"}

// connectRequestBody is the request's wording: which ledger, how the client signs in, and that the firm confirms it.
func connectRequestBody(client, provider string) string {
	choose := "company"
	if provider == "xero" {
		choose = "organisation"
	}
	return "Please connect " + client + "'s " + connectNames[provider] + " to your accounting firm with the Connect " + connectNames[provider] + " button on this page. You sign in to " +
		connectNames[provider] + " yourself and choose the " + choose + "; your password is never shared with the firm. The firm confirms the " + choose + " is " + client + "'s before reading any of its books."
}

func registerConnectRequests(mux *http.ServeMux, db *sql.DB) {
	// A member who manages integrations asks the client to connect its books: a request on the client's newest month
	// they can open, with a private link, whose email is approved apart, as any request's is.
	mux.HandleFunc("POST /v1/closes/client/connect-requests", func(w http.ResponseWriter, r *http.Request) {
		a := auth.Actor(r.Context())
		if a == nil || (a.Organization != "" && !slices.Contains(a.Permissions, "integrations:manage")) {
			problem(w, 403, "Asking a client to connect its books requires integrations:manage.")
			return
		}
		name, ok := wholeClient(w, r, db)
		if !ok {
			return
		}
		var in struct {
			Provider    string `json:"provider"`
			Environment string `json:"environment"`
			Recipient   string `json:"recipient"`
			Confirmed   bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		if connectNames[in.Provider] == "" || (in.Provider == "quickbooks" && in.Environment != "production" && in.Environment != "sandbox") || (in.Provider == "xero" && in.Environment != "") {
			problem(w, 422, "Ask for QuickBooks (production or sandbox) or Xero.")
			return
		}
		recipient, e := mail.ParseAddress(strings.TrimSpace(in.Recipient))
		if !in.Confirmed || e != nil || recipient.Address != strings.TrimSpace(in.Recipient) || len(in.Recipient) > 254 {
			problem(w, 422, "Confirm the request and its exact recipient email address.")
			return
		}
		org := organization(r)
		if connectorOff(r.Context(), w, db, org, in.Provider, connectNames[in.Provider]) {
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Request storage unavailable.")
			return
		}
		defer tx.Rollback()
		// The client's newest month, under any of its names, that the member can open by its own name's access.
		rows, err := tx.QueryContext(r.Context(), `SELECT id::text,entity FROM client_closes WHERE organization_id=$1 AND client_identity(organization_id,entity)=client_identity($1,$2) ORDER BY period DESC,id DESC`, org, name)
		if err != nil {
			problem(w, 503, "Request storage unavailable.")
			return
		}
		var closes [][2]string
		for rows.Next() {
			var c [2]string
			if err = rows.Scan(&c[0], &c[1]); err != nil {
				break
			}
			closes = append(closes, c)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		var closeID string
		for _, c := range closes {
			if err != nil || closeID != "" {
				break
			}
			var allowed bool
			if allowed, err = clientAccess(db, r, c[1]); allowed {
				closeID = c[0]
			}
		}
		if err == nil && closeID != "" {
			_, err = tx.ExecContext(r.Context(), `SELECT 1 FROM client_closes WHERE id::text=$1 FOR UPDATE`, closeID)
		}
		switch {
		case err != nil:
			problem(w, 503, "Request storage unavailable.")
			return
		case closeID == "":
			problem(w, 409, name+" has no month you can open yet. Open one, then ask the client to connect.")
			return
		}
		body := connectRequestBody(name, in.Provider)
		var draft string
		if err = tx.QueryRowContext(r.Context(), `INSERT INTO request_drafts(close_id,body,workpaper_ids,context_digest,created_by) VALUES($1,$2,'[]',$3,$4) RETURNING id::text`,
			closeID, body, preparation.Digest([]byte(body)), reviewerName(r)).Scan(&draft); err != nil {
			problem(w, 503, "Request storage unavailable.")
			return
		}
		scope := map[string]any{"snapshot_id": "", "bindings": []any{}, "jobs": []string{}, "connect": in.Provider}
		if in.Environment != "" {
			scope["environment"] = in.Environment
		}
		raw, _ := json.Marshal(scope)
		rid, token, passcode, err := openConsolidated(r.Context(), tx, draft, recipient.Address, reviewerName(r), raw, nil)
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			problem(w, 503, "The request could not be saved.")
			return
		}
		jsonReply(w, 201, map[string]string{"id": rid, "close_id": closeID, "body": body, "link_token": token, "passcode": passcode})
	})
}

// clientRequestConnect starts the sign-in a connect request asks for, from its live private link: the client's own
// name, the firm's switch for the ledger and the member's connect action decide it, under the request as actor. An
// attempt of the request's own still waiting is set aside first, so a client who abandoned one can start again.
func clientRequestConnect(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || len(token) < 26 || len(token) > 128 || db == nil {
			problem(w, 404, "Request link unavailable. Ask the firm for a new link.")
			return
		}
		if !linkPasscode(w, r, db, token) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		var rid, org, entity, provider, environment string
		var expires time.Time
		if err := db.QueryRowContext(ctx, `SELECT q.id::text,c.organization_id,c.entity,q.coverage_scope->>'connect',COALESCE(q.coverage_scope->>'environment',''),l.expires_at
 FROM client_request_links l JOIN work_requests q ON q.id=l.request_id JOIN request_drafts d ON d.id=q.draft_id JOIN client_closes c ON c.id=d.close_id JOIN organizations o ON o.id=c.organization_id
 WHERE l.digest=$1 AND l.revoked_at IS NULL AND l.expires_at>now() AND o.enabled AND q.coverage_scope ? 'connect'`, preparation.Digest([]byte(token))).Scan(&rid, &org, &entity, &provider, &environment, &expires); err != nil {
			problem(w, 404, "This link does not ask you to connect your books, or it has expired. Ask the firm for a new link.")
			return
		}
		if connectNames[provider] == "" {
			problem(w, 404, "This link does not ask you to connect your books.")
			return
		}
		if connectorOff(ctx, w, db, org, provider, connectNames[provider]) {
			return
		}
		// A new connection is made under the client's own name, as from the client's page.
		name, known, _, _, err := clientNamed(ctx, db, org, entity, true, "")
		if err != nil || !known {
			problem(w, 503, "The client is unavailable.")
			return
		}
		if _, err = db.ExecContext(ctx, `UPDATE `+provider+`_connections SET state='disconnected',updated_at=now(),error='Set aside: the client started again from the request.' WHERE request_id::text=$1 AND state='authorizing'`, rid); err != nil {
			problem(w, 503, "Connection storage unavailable.")
			return
		}
		rec := &recorded{}
		a := requestActor(org, rid)
		if provider == "quickbooks" {
			quickbooksAct(ctx, rec, db, a, name, "", "", quickbooksIn{Action: "connect", Environment: environment})
		} else {
			xeroAct(ctx, rec, db, a, name, "", "", xeroIn{Action: "connect"})
		}
		var started struct{ ID, URL string }
		if rec.status != 200 || json.Unmarshal(rec.body.Bytes(), &started) != nil || started.ID == "" {
			var refused struct {
				Message string `json:"message"`
			}
			json.Unmarshal(rec.body.Bytes(), &refused)
			problem(w, 409, cmp.Or(refused.Message, "The sign-in could not be started. Contact the firm."))
			return
		}
		// Bound to the request, it expires with its link at the latest.
		if _, err = db.ExecContext(ctx, `UPDATE `+provider+`_connections SET request_id=$2::bigint,oauth_expires_at=LEAST(oauth_expires_at,$3) WHERE id::text=$1 AND state='authorizing'`, started.ID, rid, expires); err != nil {
			problem(w, 503, "Connection storage unavailable.")
			return
		}
		jsonReply(w, 200, map[string]string{"url": started.URL})
	}
}

// requestConnected finishes, where the provider returned, a sign-in a client made through request (the attempt id, in
// org): only while the request's link is live, by the member's own finishing
// step under the request as actor, which leaves the connection for a member to confirm. A sign-in finished marks the
// link used, so the request counts as answered. The client is sent back to the request page, told how it went.
func requestConnected(w http.ResponseWriter, r *http.Request, db *sql.DB, provider, org, id, request string, finish func(context.Context, http.ResponseWriter, *auth.Claims)) {
	origin, err := url.Parse(os.Getenv("PUBLIC_APP_URL"))
	if err != nil || origin.Scheme != "https" || origin.Host == "" {
		problem(w, 503, "Configure the exact HTTPS application origin.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	var live, on bool
	err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM client_request_links WHERE request_id::text=$1 AND revoked_at IS NULL AND expires_at>now())`, request).Scan(&live)
	if err == nil {
		on, err = connectorOn(ctx, db, org, provider)
	}
	outcome := "failed"
	if err == nil && live && on {
		rec := &recorded{}
		finish(ctx, rec, requestActor(org, request))
		if rec.status == 200 {
			outcome = provider
			db.ExecContext(ctx, `UPDATE client_request_links SET used_at=COALESCE(used_at,now()) WHERE request_id::text=$1 AND revoked_at IS NULL AND expires_at>now()`, request)
		}
	} else if err == nil {
		// A request withdrawn, or a switch turned off, since the sign-in began: its attempt ends here.
		db.ExecContext(ctx, `UPDATE `+provider+`_connections SET state='disconnected',updated_at=now(),error='The request was withdrawn or expired before the sign-in finished.' WHERE id::text=$1 AND state='authorizing'`, id)
	}
	origin.Path, origin.RawQuery, origin.Fragment = "/request", url.Values{"connected": {outcome}}.Encode(), ""
	http.Redirect(w, r, origin.String(), http.StatusSeeOther)
}
