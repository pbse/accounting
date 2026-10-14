package transport

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"close.local/backend/internal/preparation"
)

// cashBuffer is a client's agreed minimum cash buffer, as a firm reviewer approved it.
type cashBuffer struct {
	ID         string    `json:"id"`
	Amount     string    `json:"amount"`
	Currency   string    `json:"currency"`
	Basis      string    `json:"basis"`
	ProposedBy string    `json:"proposed_by"`
	ApprovedBy string    `json:"approved_by"`
	ApprovedAt time.Time `json:"approved_at"`
}

// bufferAmount is a buffer as a person writes it: zero or more, at most thirteen digits and two decimals.
var bufferAmount = regexp.MustCompile(`^[0-9]{1,13}(\.[0-9]{1,2})?$`)

const bufferColumns = `id::text,amount::text,currency,basis,proposed_by,approved_by,approved_at`

// clientBuffers is a client's buffers, newest (the one in force) first, whichever of its confirmed names a month was
// opened under.
func clientBuffers(ctx context.Context, db *sql.DB, org, entity string, limit int) ([]cashBuffer, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+bufferColumns+` FROM cash_buffers WHERE organization_id=$1 AND client_identity(organization_id,client_entity)=client_identity($1,$2) ORDER BY id DESC LIMIT $3`, org, entity, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []cashBuffer{}
	for rows.Next() {
		var b cashBuffer
		if err = rows.Scan(&b.ID, &b.Amount, &b.Currency, &b.Basis, &b.ProposedBy, &b.ApprovedBy, &b.ApprovedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// bufferProposal is the buffer the agent proposes when none is agreed: two weeks of the forecast's payments, at the
// average week of its thirteen. No payments forecast, no proposal.
func bufferProposal(weeks []cashWeek, currency string) (amount, basis string) {
	total := new(big.Rat)
	for _, w := range weeks {
		if v, ok := new(big.Rat).SetString(w.Disbursements); ok {
			total.Add(total, v)
		}
	}
	if len(weeks) == 0 || total.Sign() <= 0 {
		return "", ""
	}
	week := new(big.Rat).Quo(total, big.NewRat(int64(len(weeks)), 1))
	two := new(big.Rat).Mul(week, big.NewRat(2, 1))
	return two.FloatString(2), fmt.Sprintf("Two weeks of payments: the next %d weeks' forecast payments average %s %s a week.", len(weeks), currency, week.FloatString(2))
}

// bufferFigures adds the buffer in force to a plan: its version, and the lowest forecast balance's headroom over it
// (negative, a shortfall), or, with none agreed, the agent's proposal for a firm reviewer to approve.
func bufferFigures(out map[string]any, buffer *cashBuffer, weeks []cashWeek, lowest, home string, firm bool) {
	out["buffer"] = buffer
	if buffer == nil {
		if amount, basis := bufferProposal(weeks, home); amount != "" && firm {
			out["buffer_proposal"] = map[string]string{"amount": amount, "basis": basis}
		}
		return
	}
	low, ok1 := new(big.Rat).SetString(lowest)
	floor, ok2 := new(big.Rat).SetString(buffer.Amount)
	if ok1 && ok2 && buffer.Currency == home {
		out["headroom"] = new(big.Rat).Sub(low, floor).FloatString(2)
	}
}

func registerCashBuffer(mux *http.ServeMux, db *sql.DB, client func(*http.Request) (string, string, error), plan func(*http.Request, string) (map[string]any, error)) {
	mux.HandleFunc("GET /v1/closes/{id}/cash-buffer", func(w http.ResponseWriter, r *http.Request) {
		org, entity, err := client(r)
		if err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		buffers, err := clientBuffers(r.Context(), db, org, entity, 10)
		if err != nil {
			problem(w, 503, "The cash buffer is unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"buffers": buffers})
	})
	// A firm reviewer agrees the buffer: the agent's proposal as the plan shows it now (agent), or an amount and basis
	// of their own. Each agreement is a new version; the plan compares with the newest.
	mux.HandleFunc("POST /v1/closes/{id}/cash-buffer", func(w http.ResponseWriter, r *http.Request) {
		who, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var in struct {
			Amount    string `json:"amount"`
			Basis     string `json:"basis"`
			Agent     bool   `json:"agent"`
			Confirmed bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Amount, in.Basis = strings.TrimSpace(in.Amount), strings.TrimSpace(in.Basis)
		amount, _ := new(big.Rat).SetString(in.Amount)
		if !in.Confirmed || !bufferAmount.MatchString(in.Amount) {
			problem(w, 422, "Confirm the buffer, as an amount of zero or more with at most two decimals.")
			return
		}
		proposedBy := who
		var currency string
		if in.Agent {
			out, err := plan(r, "")
			if refused, ok := err.(tieRefusal); ok {
				problem(w, refused.status, refused.message)
				return
			}
			if err != nil {
				problem(w, 503, "The cash plan is unavailable.")
				return
			}
			proposal, _ := out["buffer_proposal"].(map[string]string)
			if proposal == nil || proposal["amount"] != amount.FloatString(2) {
				problem(w, 409, "The agent's proposal changed or there is none now. Refresh the cash plan and decide again.")
				return
			}
			in.Basis, proposedBy, currency = proposal["basis"], "agent", out["currency"].(string)
		} else if n := utf8.RuneCountInString(in.Basis); n < 5 || n > 1000 {
			problem(w, 422, "State the buffer's basis (5–1,000 characters).")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "The buffer could not be saved.")
			return
		}
		defer tx.Rollback()
		var org, entity string
		if err = tx.QueryRowContext(r.Context(), `SELECT organization_id,entity FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&org, &entity); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		if currency == "" {
			profile, err := preparation.ClientProfile(r.Context(), tx, r.PathValue("id"))
			if err != nil {
				problem(w, 503, "The buffer could not be saved.")
				return
			}
			if currency = profile.Currency; currency == "" || profile.State != "confirmed" && profile.State != "detected" {
				problem(w, 409, "The client's home currency is not known yet, so a buffer cannot be set in it.")
				return
			}
		}
		var id string
		if err = tx.QueryRowContext(r.Context(), `INSERT INTO cash_buffers(organization_id,client_entity,amount,currency,basis,proposed_by,approved_by) VALUES($1,$2,$3::numeric,$4,$5,$6,$7) RETURNING id::text`,
			org, entity, amount.FloatString(2), currency, in.Basis, proposedBy, who).Scan(&id); err != nil || tx.Commit() != nil {
			problem(w, 503, "The buffer could not be saved.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": id})
	})
}
