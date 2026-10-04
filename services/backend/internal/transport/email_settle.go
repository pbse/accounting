package transport

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"close.local/backend/internal/auth"
)

// emailSettlement is a person's reading of the provider's own records for one email: accepted, with the provider's
// message ID, or failed (the provider has no such message), from the state they saw.
type emailSettlement struct {
	ExpectedState string `json:"expected_state"`
	Outcome       string `json:"outcome"`
	ProviderID    string `json:"provider_id"`
	Evidence      string `json:"evidence"`
}

// settleEmail settles an email still in one of states, as seen, and never one a worker is sending right now. Its
// attempts and error stay; the settlement is added to the error line so every view of the email shows it.
func settleEmail(ctx context.Context, tx *sql.Tx, outbox string, states []string, in emailSettlement, who string) (string, error) {
	in.ProviderID, in.Evidence = strings.TrimSpace(in.ProviderID), strings.TrimSpace(in.Evidence)
	var state, connection, provider, recipient, org string
	var sending bool
	err := tx.QueryRowContext(ctx, `SELECT m.state,m.connection_id::text,COALESCE(m.provider_id,''),
 COALESCE((SELECT q.recipient FROM work_requests q WHERE q.id=m.request_id),(SELECT c.recipient FROM collection_messages c WHERE c.id=m.collection_id),''),
 x.organization_id,m.state='sending' AND m.claimed_at>=now()-interval '2 minutes'
 FROM email_outbox m JOIN email_connections x ON x.id=m.connection_id WHERE m.id=$1 FOR UPDATE OF m`, outbox).Scan(&state, &connection, &provider, &recipient, &org, &sending)
	if err != nil {
		return "", err
	}
	n := utf8.RuneCountInString(in.Evidence)
	switch {
	case state != in.ExpectedState || !slices.Contains(states, state):
		return org, refusal{409, "This email is not in the state you saw, or cannot be settled here. Refresh."}
	case sending:
		return org, refusal{409, "This email is being sent right now. Wait for the attempt to finish."}
	case n < 5 || n > 2000:
		return org, refusal{422, "Say what the provider's records show and how you searched them: the sender, recipient, time and this email's ID (5–2,000 characters)."}
	case in.Outcome == "accepted" && (in.ProviderID == "" || len(in.ProviderID) > 200):
		return org, refusal{422, "Give the provider's message ID for an accepted email."}
	case in.Outcome == "failed" && (in.ProviderID != "" || state != "unknown"):
		return org, refusal{422, "Only an unknown email is settled as failed, without a message ID; a queued or sending one is settled only as accepted."}
	case in.Outcome != "accepted" && in.Outcome != "failed":
		return org, refusal{422, "Settle the email as accepted or failed."}
	case provider != "" && (in.Outcome == "failed" || in.ProviderID != provider):
		return org, refusal{409, "This email already has the provider's message ID " + provider + "; it can only be settled as accepted with that ID."}
	}
	if in.Outcome == "accepted" {
		var taken bool
		// Settlements naming the same message ID on a sender wait for each other, so two emails never both take it.
		if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('email_provider:'||$1||':'||$2))`, connection, in.ProviderID); err != nil {
			return org, err
		}
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM email_outbox WHERE connection_id::text=$1 AND provider_id=$2 AND id<>$3)
 OR EXISTS(SELECT 1 FROM email_delivery_events WHERE connection_id::text=$1 AND provider_id=$2 AND lower(recipient)<>lower($4))`, connection, in.ProviderID, outbox, recipient).Scan(&taken); err != nil {
			return org, err
		}
		if taken {
			return org, refusal{409, "That message ID belongs to another email or recipient on this sender."}
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO email_settlements(organization_id,outbox_id,prior_state,outcome,provider_id,evidence,settled_by) VALUES($1,$2,$3,$4,NULLIF($5,''),$6,$7)`, org, outbox, state, in.Outcome, in.ProviderID, in.Evidence, who); err != nil {
		return org, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE email_outbox SET state=$2,provider_id=COALESCE(NULLIF($3,''),provider_id),claim=NULL,finished_at=now(),
 error=ltrim(error||' Settled as '||$2||' by '||$4||' from the provider''s records: '||$5) WHERE id=$1`, outbox, in.Outcome, in.ProviderID, who, in.Evidence)
	return org, err
}

// registerEmailSettlement lets the firm settle an unknown email of a close it can open.
func registerEmailSettlement(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("POST /v1/closes/{id}/email/{outbox}/settle", func(w http.ResponseWriter, r *http.Request) {
		who, ok := reviewActor(w, r)
		if !ok {
			return
		}
		if a := auth.Actor(r.Context()); a != nil && a.Organization != "" && !slices.Contains(a.Permissions, "requests:send") {
			problem(w, 403, "Settling an email requires requests:send.")
			return
		}
		var in emailSettlement
		if !decode(w, r, &in) {
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Email unavailable.")
			return
		}
		defer tx.Rollback()
		var mine bool
		if err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM email_outbox m LEFT JOIN work_requests q ON q.id=m.request_id LEFT JOIN request_drafts d ON d.id=q.draft_id
 LEFT JOIN collection_messages c ON c.id=m.collection_id WHERE m.id=$1 AND COALESCE(d.close_id,c.close_id)::text=$2)`, r.PathValue("outbox"), r.PathValue("id")).Scan(&mine); err != nil {
			problem(w, 503, "Email unavailable.")
			return
		}
		if !mine {
			problem(w, 404, "Email not found in this close.")
			return
		}
		_, err = settleEmail(r.Context(), tx, r.PathValue("outbox"), []string{"unknown"}, in, who)
		var refused refusal
		if errors.As(err, &refused) {
			problem(w, refused.status, refused.message)
			return
		}
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "The settlement could not be saved.")
			return
		}
		jsonReply(w, 200, map[string]string{"state": in.Outcome})
	})
}

// adminSettleEmail lets platform staff settle a firm's email during recovery, also while the firm is suspended and for
// an email a restored backup still shows as queued or sending, from the provider's records.
func adminSettleEmail(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	firm, outbox := r.PathValue("id"), r.PathValue("outbox")
	var in struct {
		emailSettlement
		Reason string `json:"reason"`
	}
	if !adminReason(w, r, &in, func() string { return in.Reason }) {
		return
	}
	who := "staff:" + staffOf(r.Context()).Subject
	err := adminAct(r.Context(), db, adminAction{Firm: firm, Action: "email.settle", TargetType: "email", TargetID: outbox, Reason: strings.TrimSpace(in.Reason), Request: map[string]string{"expected_state": in.ExpectedState, "outcome": in.Outcome, "provider_id": in.ProviderID}},
		func(ctx context.Context, tx *sql.Tx) error {
			org, err := settleEmail(ctx, tx, outbox, []string{"unknown", "queued", "sending"}, in.emailSettlement, who)
			if org != "" && org != firm {
				return sql.ErrNoRows
			}
			return err
		})
	var refused refusal
	switch {
	case errors.As(err, &refused):
		problem(w, refused.status, refused.message)
	case errors.Is(err, sql.ErrNoRows):
		problem(w, 404, "Email not found for this firm.")
	case err != nil:
		adminFirmProblem(w, err, "email settlement")
	default:
		jsonReply(w, 200, map[string]string{"state": in.Outcome})
	}
}
