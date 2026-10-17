package transport

import (
	"encoding/json"

	"close.local/backend/internal/prepaids"
)

// Bind the proposed date to the bill actually read, never to model-supplied amounts or capture identifiers.
func billHandoffProposal(raw string, args map[string]any) string {
	var read struct {
		State     string     `json:"state"`
		CaptureID string     `json:"capture_id"`
		Payables  []openItem `json:"payables"`
	}
	if json.Unmarshal([]byte(raw), &read) != nil || read.State != "ready" || read.CaptureID == "" {
		return "read this close's open_items successfully before proposing a payment date"
	}
	for _, bill := range read.Payables {
		if bill.Kind != "Bill" || bill.ID != args["item_id"] {
			continue
		}
		amount, err := prepaids.Amount(bill.Balance)
		if err != nil || amount <= 0 || bill.Currency == "" || bill.Problem != "" {
			return "this bill's amount or currency needs clarification before a payment date can be proposed"
		}
		previous, status := "", "open"
		if bill.Action != nil {
			previous, status = bill.Action.ID, bill.Action.Status
		}
		args["capture_id"], args["previous_id"] = read.CaptureID, previous
		args["label"], args["balance"], args["currency"] = bill.Label, bill.Balance, bill.Currency
		args["due"], args["current_status"] = bill.Due, status
		return ""
	}
	return "that bill was not in this close's open_items read"
}
