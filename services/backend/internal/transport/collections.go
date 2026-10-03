package transport

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/delivery"
	"close.local/backend/internal/ledger"
	"close.local/backend/internal/secrets"
)

var emailAddress = regexp.MustCompile(`^[^@\s<>,;"]{1,64}@[^@\s<>,;"]+\.[^@\s<>,;"]{2,}$`)

// collectionBody is the plain default message about an open invoice; the accountant reads and may change it before it
// is saved, and approves it before anything is sent.
func collectionBody(entity string, item openItem) string {
	due := ""
	if item.Due != "" {
		due = " was due on " + item.Due + " and"
	}
	return fmt.Sprintf("Hello,\n\nOur records show that %s for %s%s remains unpaid. If it has already been paid, please disregard this message. Otherwise, please let us know when we can expect payment, or reply if anything about the invoice needs to be resolved.\n\nThank you,\n%s",
		item.Label, ledger.Show(item.Balance, item.Currency), due, entity)
}

// registerCollections prepares and sends collection messages to a client's customers: only under the client's recorded
// authority, to an address that is not marked do not contact and has not bounced, each message approved by a person
// before the outbox sends it. Nothing here records a payment, a promise or a write-off.
func registerCollections(mux *http.ServeMux, db *sql.DB) {
	client := func(r *http.Request) (org, entity string, err error) {
		err = db.QueryRowContext(r.Context(), `SELECT organization_id,entity FROM client_closes WHERE id::text=$1`, r.PathValue("id")).Scan(&org, &entity)
		return
	}
	// authority is the sender the client's recorded permission in force allows, or "" when none is granted.
	authority := func(r *http.Request, org, entity string) (string, error) {
		var sender string
		err := db.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT CASE WHEN granted THEN sender ELSE '' END FROM customer_contact_authority WHERE organization_id=$1 AND lower(client_entity)=lower($2) ORDER BY id DESC LIMIT 1),'')`, org, entity).Scan(&sender)
		return sender, err
	}
	// blocked says why an address may not be emailed: the client asked not to, or an email to it bounced, was complained
	// about or was suppressed.
	blocked := func(r *http.Request, org, entity, recipient string) (string, error) {
		var reason string
		err := db.QueryRowContext(r.Context(), `SELECT CASE WHEN COALESCE((SELECT do_not_contact FROM collection_contact_preferences WHERE organization_id=$1 AND lower(client_entity)=lower($2) AND lower(recipient)=lower($3) ORDER BY id DESC LIMIT 1),false) THEN 'This address is marked do not contact.'
 WHEN EXISTS(SELECT 1 FROM collection_bounces WHERE organization_id=$1 AND client_entity=lower($2) AND recipient=lower($3)) THEN 'An earlier email to this address bounced, was marked as spam or was suppressed.'
 ELSE '' END`, org, entity, recipient).Scan(&reason)
		return reason, err
	}
	mux.HandleFunc("GET /v1/closes/{id}/collections", func(w http.ResponseWriter, r *http.Request) {
		org, entity, err := client(r)
		if err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		var current, messages, preferences, bounced []byte
		err = db.QueryRowContext(r.Context(), `SELECT
 COALESCE((SELECT to_jsonb(a) FROM (SELECT granted,sender,reference,recorded_by,recorded_at FROM customer_contact_authority WHERE organization_id=$1 AND lower(client_entity)=lower($2) ORDER BY id DESC LIMIT 1) a),'null'),
 COALESCE((SELECT jsonb_agg(jsonb_build_object('id',c.id::text,'item_id',c.item_id,'label',c.label,'customer',c.customer,'amount',c.amount::text,'currency',c.currency,'recipient',c.recipient,'reply_to',c.reply_to,'subject',c.subject,'body',c.body,'created_by',c.created_by,'created_at',c.created_at,
   'email',(SELECT jsonb_build_object('id',m.id,'state',m.state,'delivery',m.delivery,'error',m.error,'approved_by',m.approved_by,'created_at',m.created_at) FROM email_delivery m WHERE m.collection_id=c.id)) ORDER BY c.id DESC)
   FROM (SELECT * FROM collection_messages WHERE close_id::text=$3 ORDER BY id DESC LIMIT 100) c),'[]'),
 COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY p.recipient) FROM (SELECT DISTINCT ON (lower(recipient)) recipient,do_not_contact,reason,recorded_by,recorded_at FROM collection_contact_preferences WHERE organization_id=$1 AND lower(client_entity)=lower($2) ORDER BY lower(recipient),id DESC) p),'[]'),
 COALESCE((SELECT jsonb_agg(recipient ORDER BY recipient) FROM collection_bounces WHERE organization_id=$1 AND client_entity=lower($2)),'[]')`,
			org, entity, r.PathValue("id")).Scan(&current, &messages, &preferences, &bounced)
		if err != nil {
			problem(w, 503, "Collections are unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"authority": json.RawMessage(current), "messages": json.RawMessage(messages), "preferences": json.RawMessage(preferences), "bounced": json.RawMessage(bounced)})
	})
	mux.HandleFunc("POST /v1/closes/{id}/collections", func(w http.ResponseWriter, r *http.Request) {
		who, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var in struct {
			Action       string `json:"action"`
			Granted      bool   `json:"granted"`
			Reference    string `json:"reference"`
			Recipient    string `json:"recipient"`
			DoNotContact bool   `json:"do_not_contact"`
			Reason       string `json:"reason"`
			ItemID       string `json:"item_id"`
			ReplyTo      string `json:"reply_to"`
			Subject      string `json:"subject"`
			Body         string `json:"body"`
			MessageID    string `json:"message_id"`
			ConnectionID string `json:"connection_id"`
			Confirmed    bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		org, entity, err := client(r)
		if err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		in.Recipient, in.ReplyTo = strings.TrimSpace(in.Recipient), strings.TrimSpace(in.ReplyTo)
		switch in.Action {
		case "authority":
			// The firm records the client's own authority to contact its customers from the firm's current sender, and where
			// the client gave it.
			if a := auth.Actor(r.Context()); a != nil && a.Organization != "" && !slices.Contains(a.Permissions, "requests:send") {
				problem(w, 403, "Recording the client's authority requires requests:send.")
				return
			}
			if in.Reference = strings.TrimSpace(in.Reference); !in.Confirmed || len(in.Reference) < 5 || utf8.RuneCountInString(in.Reference) > 2000 {
				problem(w, 422, "Confirm the client's decision and say where they gave it (5–2,000 characters), for example the engagement letter's section.")
				return
			}
			sender := ""
			if in.Granted {
				if db.QueryRowContext(r.Context(), `SELECT sender FROM email_connections WHERE organization_id=$1 AND enabled`, org).Scan(&sender) != nil {
					problem(w, 409, "Connect the firm's sender first: the client authorizes contact from that sender.")
					return
				}
			}
			_, err = db.ExecContext(r.Context(), `INSERT INTO customer_contact_authority(organization_id,client_entity,granted,sender,reference,recorded_by) VALUES($1,$2,$3,$4,$5,$6)`, org, entity, in.Granted, sender, in.Reference, who)
		case "preference":
			if in.Reason = strings.TrimSpace(in.Reason); !emailAddress.MatchString(in.Recipient) || in.Reason == "" || utf8.RuneCountInString(in.Reason) > 2000 {
				problem(w, 422, "Give the address and why (up to 2,000 characters).")
				return
			}
			_, err = db.ExecContext(r.Context(), `INSERT INTO collection_contact_preferences(organization_id,client_entity,recipient,do_not_contact,reason,recorded_by) VALUES($1,$2,$3,$4,$5,$6)`, org, entity, in.Recipient, in.DoNotContact, in.Reason, who)
		case "draft":
			in.Subject, in.Body = strings.TrimSpace(in.Subject), strings.TrimSpace(in.Body)
			if !emailAddress.MatchString(in.Recipient) || (in.ReplyTo != "" && !emailAddress.MatchString(in.ReplyTo)) || utf8.RuneCountInString(in.Subject) > 200 || (in.Body != "" && (utf8.RuneCountInString(in.Body) < 10 || utf8.RuneCountInString(in.Body) > 5000)) {
				problem(w, 422, "Give the customer contact's email address the client provided, and optionally a reply-to address, a subject (up to 200 characters) and a message (10–5,000 characters).")
				return
			}
			receivables, _, meta, e := latestOpenItems(r, db)
			if e != nil {
				problem(w, 409, "Capture the client's open invoices first.")
				return
			}
			i := slices.IndexFunc(receivables, func(item openItem) bool { return item.Kind == "Invoice" && item.ID == in.ItemID })
			if i < 0 || receivables[i].Balance == "" {
				problem(w, 409, "That invoice is not open in the latest capture.")
				return
			}
			item := receivables[i]
			if in.Subject == "" {
				subject := []rune("Payment reminder: " + item.Label)
				in.Subject = string(subject[:min(len(subject), 200)])
			}
			if in.Body == "" {
				in.Body = collectionBody(entity, item)
			}
			var id string
			if err = db.QueryRowContext(r.Context(), `INSERT INTO collection_messages(organization_id,close_id,client_entity,company,capture_id,item_id,label,customer,amount,currency,recipient,reply_to,subject,body,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::numeric,$10,$11,$12,$13,$14,$15) RETURNING id::text`,
				org, r.PathValue("id"), entity, meta["company"], fmt.Sprint(meta["capture_id"]), item.ID, item.Label, item.Party, item.Balance, item.Currency, in.Recipient, in.ReplyTo, in.Subject, in.Body, who).Scan(&id); err != nil {
				problem(w, 503, "The draft could not be saved.")
				return
			}
			jsonReply(w, 200, map[string]any{"id": id})
			return
		case "approve":
			approve(w, r, db, org, entity, who, in.MessageID, in.ConnectionID, in.Confirmed, authority, blocked)
			return
		default:
			problem(w, 422, "Record the client's authority or a contact preference, draft a message, or approve one.")
			return
		}
		if err != nil {
			problem(w, 503, "The record could not be saved.")
			return
		}
		jsonReply(w, 200, map[string]bool{"saved": true})
	})
}

// approve queues one drafted message for the outbox after checking, again, everything that allows it to be sent now.
func approve(w http.ResponseWriter, r *http.Request, db *sql.DB, org, entity, who, messageID, connectionID string, confirmed bool,
	authority func(*http.Request, string, string) (string, error), blocked func(*http.Request, string, string, string) (string, error)) {
	a := auth.Actor(r.Context())
	if a == nil || a.Organization == "" || !slices.Contains(a.Permissions, "requests:send") {
		problem(w, 403, "Sending to a client's customers requires a hosted session with requests:send.")
		return
	}
	if !confirmed {
		problem(w, 422, "Confirm the recipient, sender and message before sending.")
		return
	}
	var m struct{ recipient, replyTo, subject, body, company, capture, itemID, amount, currency string }
	if err := db.QueryRowContext(r.Context(), `SELECT recipient,reply_to,subject,body,company,capture_id,item_id,amount::text,currency FROM collection_messages WHERE id::text=$1 AND close_id::text=$2`, messageID, r.PathValue("id")).
		Scan(&m.recipient, &m.replyTo, &m.subject, &m.body, &m.company, &m.capture, &m.itemID, &m.amount, &m.currency); err != nil {
		problem(w, 404, "Draft not found.")
		return
	}
	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		problem(w, 503, "Email outbox unavailable.")
		return
	}
	defer tx.Rollback()
	// Holding the close serializes approvals of this message with each other and with a new capture of the books.
	var locked, existing, connection, sender string
	if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&locked); err != nil {
		problem(w, 404, "Close not found.")
		return
	}
	if err = tx.QueryRowContext(r.Context(), `SELECT id FROM email_outbox WHERE collection_id::text=$1`, messageID).Scan(&existing); err == nil {
		jsonReply(w, 200, map[string]string{"id": existing})
		return
	} else if err != sql.ErrNoRows {
		problem(w, 503, "Email history unavailable.")
		return
	}
	authorized, err := authority(r, org, entity)
	if err != nil {
		problem(w, 503, "The client's authority is unavailable.")
		return
	}
	if authorized == "" {
		problem(w, 409, "Record the client's authority to contact its customers before sending.")
		return
	}
	reason, err := blocked(r, org, entity, m.recipient)
	if err != nil {
		problem(w, 503, "Contact preferences are unavailable.")
		return
	}
	if reason != "" {
		problem(w, 409, reason)
		return
	}
	// The message speaks for the books it was drafted from: the same capture, and the invoice still open with the same
	// balance. Otherwise it is drafted again from the current books.
	receivables, _, meta, err := latestOpenItems(r, db)
	if err != nil || meta["company"] != m.company || fmt.Sprint(meta["capture_id"]) != m.capture {
		problem(w, 409, "The books were captured again since this message was drafted. Draft it again from the current open invoices.")
		return
	}
	i := slices.IndexFunc(receivables, func(item openItem) bool { return item.Kind == "Invoice" && item.ID == m.itemID })
	if open, _ := new(big.Rat).SetString(m.amount); i < 0 || receivables[i].Currency != m.currency || func() bool { b, ok := new(big.Rat).SetString(receivables[i].Balance); return !ok || b.Cmp(open) != 0 }() {
		problem(w, 409, "This invoice is no longer open for the drafted amount. Draft it again from the current open invoices.")
		return
	}
	if err = tx.QueryRowContext(r.Context(), `SELECT id::text,sender FROM email_connections WHERE organization_id=$1 AND enabled`, org).Scan(&connection, &sender); err != nil || connection != connectionID {
		problem(w, 409, "Verify and select the current firm sender before approving delivery.")
		return
	}
	if sender != authorized {
		problem(w, 409, "The client authorized contact from "+authorized+", not the firm's current sender "+sender+". Record the client's authority for the current sender first.")
		return
	}
	if a.ExpiresAt == nil {
		problem(w, 403, "A current expiring user session is required.")
		return
	}
	until := time.Now().Add(30 * time.Minute)
	if a.ExpiresAt.Time.Before(until) {
		until = a.ExpiresAt.Time
	}
	id := rand.Text()
	payload, _ := json.Marshal(delivery.Email{From: sender, To: []string{m.recipient}, ReplyTo: m.replyTo, Subject: m.subject, Text: m.body})
	encrypted, err := secrets.Seal(org+"/email/"+id, payload)
	if err != nil {
		problem(w, 503, err.Error())
		return
	}
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO email_outbox(id,collection_id,connection_id,payload,approved_by,approved_until,firm_authority) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, messageID, connection, encrypted, who, until, firmAccess(r)); err != nil || tx.Commit() != nil {
		problem(w, 503, "Email approval could not be confirmed. Refresh before retrying.")
		return
	}
	jsonReply(w, 200, map[string]string{"id": id})
}
