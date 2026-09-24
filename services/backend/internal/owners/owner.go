// Package owners prepares one owner-related reclassification after the client confirms the purpose.
// An unanswered purpose stays a question; tax treatment is never chosen here.
package owners

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/prepaids"
)

const Method = "owner_reclass_v1"

var EntityTypes = []string{"sole_proprietor", "partnership", "s_corp", "c_corp"}

type Input struct {
	Entity                       string `json:"entity"`
	ClosePeriod                  string `json:"close_period"`
	EntityType                   string `json:"entity_type"`
	TransactionReference         string `json:"transaction_reference"`
	TransactionDate              string `json:"transaction_date"`
	Payee                        string `json:"payee"`
	Amount                       string `json:"amount"`
	LedgerReference              string `json:"ledger_reference"`
	Direction                    string `json:"direction"`
	PurposeStatus                string `json:"purpose_status"`
	StatedPurpose                string `json:"stated_purpose,omitempty"`
	ClientConfirmationReference  string `json:"client_confirmation_reference,omitempty"`
	Treatment                    string `json:"treatment,omitempty"`
	BusinessUsePercent           int    `json:"business_use_percent"`
	BookedAccount                string `json:"booked_account,omitempty"`
	OwnerAccount                 string `json:"owner_account,omitempty"`
	Partner                      string `json:"partner,omitempty"`
	DividendDeclarationReference string `json:"dividend_declaration_reference,omitempty"`
	RepaymentReference           string `json:"repayment_reference,omitempty"`
	ReimbursementReference       string `json:"reimbursement_reference,omitempty"`
	NotBookedConfirmed           bool   `json:"not_booked_confirmed,omitempty"`
	Method                       string `json:"method"`
	TreatmentConfirmed           bool   `json:"treatment_confirmed,omitempty"`
}

type Workpaper struct {
	Status   string               `json:"status"`
	Method   string               `json:"method"`
	Input    Input                `json:"input"`
	Portion  string               `json:"portion"`
	Proposal *prepaids.Adjustment `json:"proposal"`
	Question string               `json:"question,omitempty"`
	Blockers []string             `json:"blockers"`
}

func bounded(values ...*string) bool {
	for _, v := range values {
		*v = strings.TrimSpace(*v)
		if *v == "" || len(*v) > 200 {
			return false
		}
	}
	return true
}

func Prepare(in Input) (Workpaper, error) {
	fail := func(format string, args ...any) (Workpaper, error) { return Workpaper{}, fmt.Errorf(format, args...) }
	if !bounded(&in.Entity, &in.TransactionReference, &in.Payee, &in.LedgerReference) {
		return fail("Provide the client, transaction reference, payee and ledger evidence reference (1–200 characters each).")
	}
	if in.Method != Method || !slices.Contains(EntityTypes, in.EntityType) {
		return fail("Choose the owner reclassification method and the client's recorded entity type.")
	}
	month, err := time.Parse("2006-01", in.ClosePeriod)
	if err != nil {
		return fail("Enter a supported close month.")
	}
	date, err := time.Parse("2006-01-02", in.TransactionDate)
	if err != nil || date.Format("2006-01") != in.ClosePeriod {
		return fail("The transaction must be dated in the close month; earlier periods need the firm's prior-period approval.")
	}
	amount, err := prepaids.Amount(in.Amount)
	if err != nil || amount == 0 {
		return fail("Enter the transaction amount as a positive amount with at most two decimals.")
	}
	in.Amount = prepaids.Money(amount)
	personal := in.Direction == "personal_paid_by_business"
	if !personal && in.Direction != "business_paid_personally" {
		return fail("Choose whether the business paid a personal cost or the owner paid a business cost.")
	}
	result := Workpaper{Status: "draft", Method: Method, Blockers: []string{}}
	if in.PurposeStatus == "open" {
		if in.Treatment != "" || in.StatedPurpose != "" || in.ClientConfirmationReference != "" {
			return fail("An open question cannot carry a treatment or the client's answer.")
		}
		if personal {
			result.Question = fmt.Sprintf("What was the $%s payment to %s on %s for? If it was personal, will you repay the business?", in.Amount, in.Payee, in.TransactionDate)
		} else {
			result.Question = fmt.Sprintf("Did you personally pay $%s to %s on %s for the business, and should the business reimburse you?", in.Amount, in.Payee, in.TransactionDate)
		}
		result.Blockers = append(result.Blockers, "The client has not confirmed the purpose; nothing is proposed.")
		result.Input = in
		return result, nil
	}
	if in.PurposeStatus != "answered" {
		return fail("Mark the purpose as an open question or as answered by the client.")
	}
	in.StatedPurpose = strings.TrimSpace(in.StatedPurpose)
	if len(in.StatedPurpose) < 5 || len(in.StatedPurpose) > 500 || !bounded(&in.ClientConfirmationReference, &in.BookedAccount, &in.OwnerAccount) || !in.TreatmentConfirmed {
		return fail("Record what the client said, cite their answer, name the booked and owner accounts, and confirm the treatment follows the entity type and firm policy.")
	}
	if strings.EqualFold(in.BookedAccount, in.OwnerAccount) {
		return fail("The booked and owner accounts must differ.")
	}
	if in.BusinessUsePercent < 0 || in.BusinessUsePercent > 100 {
		return fail("Business use must be a whole percent from 0 to 100.")
	}
	allowed := map[bool][]string{true: {"distribution", "due_from_owner"}, false: {"contribution", "due_to_owner"}}[personal]
	if !slices.Contains(allowed, in.Treatment) {
		return fail("Choose %s for this direction.", strings.Join(allowed, " or "))
	}
	evidence := map[string]*string{"due_from_owner": &in.RepaymentReference, "due_to_owner": &in.ReimbursementReference}
	if ref := evidence[in.Treatment]; ref != nil && !bounded(ref) {
		return fail("A balance owed needs the cited confirmation that it will be repaid or reimbursed.")
	}
	if in.Treatment == "distribution" && in.EntityType == "c_corp" && !bounded(&in.DividendDeclarationReference) {
		return fail("A C corporation distribution needs the cited dividend declaration; otherwise ask the client how it should be treated.")
	}
	if in.EntityType == "partnership" && (in.Treatment == "distribution" || in.Treatment == "contribution") && !bounded(&in.Partner) {
		return fail("Name the partner whose draw or contribution account this is.")
	}
	if !personal && !in.NotBookedConfirmed {
		return fail("Confirm the ledger evidence shows the cost is not already booked or reimbursed.")
	}
	percent := int64(in.BusinessUsePercent)
	if personal {
		percent = 100 - percent
	}
	portion := (amount*percent*2 + 100) / 200
	result.Portion = prepaids.Money(portion)
	if portion > 0 {
		debit, credit := in.OwnerAccount, in.BookedAccount
		if !personal {
			debit, credit = in.BookedAccount, in.OwnerAccount
		}
		result.Proposal = &prepaids.Adjustment{Date: month.AddDate(0, 1, -1).Format("2006-01-02"), DebitAccount: debit, CreditAccount: credit, Amount: result.Portion}
	}
	result.Input = in
	return result, nil
}
