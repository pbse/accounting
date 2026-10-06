// Package acceptance is the release corpus: its case format, the recorded model answers it can replay, and how a run is
// scored (docs/acceptance-corpus.md). A case is one client-month: its documents, what an accountant says each should
// yield, and whether the close should be ready afterwards. cmd/acceptance loads each case into a fresh local database,
// drives the real pipeline and scores what it observed against these labels, by work type and with denominators, never as
// one blended accuracy figure.
package acceptance

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"close.local/backend/internal/documents"
)

// Case is one client-month.
type Case struct {
	ID        string     `json:"id"`
	Split     string     `json:"split"` // development, or held_out for cases kept out of tuning
	Client    string     `json:"client"`
	Period    string     `json:"period"`
	Tags      []string   `json:"tags,omitempty"` // what the case exercises, such as wrong_client or ocr_decimal
	Setup     Setup      `json:"setup"`
	Documents []Document `json:"documents"`
	// Signoff, when present, is what the accountant does once the agent is done: approve every draft and define what
	// sign-off covers. Readiness is then read as sign-off would read it, so a false "ready" can show.
	Signoff     *Signoff `json:"signoff,omitempty"`
	ExpectReady bool     `json:"expect_ready"`
	Dir         string   `json:"-"`
}

// Signoff is the accountant's part before sign-off, simulated after the pipeline settles.
type Signoff struct {
	Description   string `json:"description"`
	ApproveDrafts bool   `json:"approve_drafts"`
}

// Setup is the firm configuration the case needs before its documents arrive.
type Setup struct {
	Prepaid    *Procedure `json:"prepaid_procedure,omitempty"`
	QuickBooks *Ledger    `json:"quickbooks,omitempty"`
}

// Procedure is an enabled prepaid procedure for the client.
type Procedure struct {
	Method         string `json:"method"`
	ExpenseAccount string `json:"expense_account"`
	PrepaidAccount string `json:"prepaid_account"`
}

// Ledger is a QuickBooks capture of the month: the chart of accounts, the month's bills and the month-end Trial Balance
// that readiness ties the supported balances out against.
type Ledger struct {
	HomeCurrency string    `json:"home_currency"`
	Accounts     []Account `json:"accounts"`
	Bills        []Bill    `json:"bills"`
	TrialBalance []Balance `json:"trial_balance"`
}

// Account is a chart account; Type is its QuickBooks account type (Expense, Other Current Asset, Accounts Payable, …).
type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

// Balance is one Trial Balance row: an account's debit or credit balance at month-end; both empty is zero.
type Balance struct {
	AccountID string `json:"account_id"`
	Debit     string `json:"debit,omitempty"`
	Credit    string `json:"credit,omitempty"`
}

type Bill struct {
	Vendor    string `json:"vendor"`
	Number    string `json:"number"`
	Amount    string `json:"amount"`
	Date      string `json:"date"`
	AccountID string `json:"account_id"`
}

// check refuses a capture the app could not have taken: every QuickBooks month-end capture holds the Trial Balance, and
// each row names a chart account with a decimal balance on one side.
func (l Ledger) check() []error {
	var problems []error
	accounts := map[string]bool{}
	for _, a := range l.Accounts {
		accounts[a.ID] = true
	}
	if len(l.TrialBalance) == 0 {
		problems = append(problems, errors.New("a QuickBooks capture needs its month-end trial_balance: readiness ties the balances out against it"))
	}
	for _, b := range l.TrialBalance {
		if !accounts[b.AccountID] {
			problems = append(problems, fmt.Errorf("trial balance account %q is not in the chart", b.AccountID))
		}
		for _, v := range []string{b.Debit, b.Credit} {
			if v != "" && !decimalPattern.MatchString(v) {
				problems = append(problems, fmt.Errorf("trial balance account %s: %q is not a decimal amount", b.AccountID, v))
			}
		}
		if b.Debit != "" && b.Credit != "" {
			problems = append(problems, fmt.Errorf("trial balance account %s: a balance is a debit or a credit, not both", b.AccountID))
		}
	}
	for _, b := range l.Bills {
		if !accounts[b.AccountID] {
			problems = append(problems, fmt.Errorf("bill %s is booked to account %q, which is not in the chart", b.Number, b.AccountID))
		}
	}
	return problems
}

var decimalPattern = regexp.MustCompile(`^\d+(\.\d+)?$`)

// Upload is a document as the upload route adds it (documents.UploadContent): a workbook becomes the CSV text of its
// first sheet, named with .csv added; anything else is its bytes. A file over the route's limit is refused, as the
// route refuses it. The app keeps one document per uploaded body.
func (c Case) Upload(file string) (name string, body []byte, err error) {
	if body, err = os.ReadFile(filepath.Join(c.Dir, file)); err != nil {
		return "", nil, err
	}
	if len(body) > documents.MaxUpload {
		return "", nil, fmt.Errorf("%s is over the upload route's 5 MiB limit", file)
	}
	return documents.UploadContent(file, body)
}

// Document is one file of the case with its accountant's label. Model names a recorded model answer, replayed in
// recorded runs: JSON in the model's answer shape, where "$primary" stands for this document's evidence ID and
// "$source:<file>" for another document's.
type Document struct {
	File  string `json:"file"`
	Label Label  `json:"label"`
	Model string `json:"model,omitempty"`
	// Selection is the recorded evidence-selection answer, needed when the close is too large to read whole (more than 20
	// sources or 128 KiB of text) and the model first chooses what to read with this document.
	Selection string `json:"selection,omitempty"`
}

// Label is what the document should yield. WorkType is the work it supports (prepaid, accrual, …) or none. Facts are the
// values a correct reading gives, by field; Drafts are the entries correct preparation proposes, every one of them, when
// the label judges drafts. Exception names a material
// problem a person must see before anything goes on (wrong_client, missing_pages, …); a document whose bytes repeat an
// earlier one's is labeled duplicate.
type Label struct {
	WorkType  string            `json:"work_type"`
	Facts     map[string]string `json:"facts,omitempty"`
	Drafts    []Entry           `json:"drafts,omitempty"`
	Exception string            `json:"exception,omitempty"`
}

// Entry is a proposed journal entry; a label leaves out the fields it does not judge.
type Entry struct {
	Date          string `json:"date,omitempty"`
	DebitAccount  string `json:"debit_account,omitempty"`
	CreditAccount string `json:"credit_account,omitempty"`
	Amount        string `json:"amount,omitempty"`
}

var (
	idPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	periodPattern = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)
)

// Load reads every case of a corpus: each directory holding a case.json, with its documents beside it.
func Load(dir string) ([]Case, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*", "case.json"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no cases in %s: each case is a directory holding case.json", dir)
	}
	slices.Sort(paths)
	cases, seen := []Case{}, map[string]bool{}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var c Case
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&c); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		c.Dir = filepath.Dir(path)
		if err = c.check(); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if seen[c.ID] {
			return nil, fmt.Errorf("%s: case %s appears twice", path, c.ID)
		}
		seen[c.ID] = true
		cases = append(cases, c)
	}
	return cases, nil
}

func (c Case) check() error {
	var problems []error
	if !idPattern.MatchString(c.ID) {
		problems = append(problems, fmt.Errorf("id %q: lowercase letters, digits, - and _", c.ID))
	}
	if c.Split != "development" && c.Split != "held_out" {
		problems = append(problems, fmt.Errorf("split %q: development or held_out", c.Split))
	}
	if strings.TrimSpace(c.Client) == "" || !periodPattern.MatchString(c.Period) {
		problems = append(problems, errors.New("a case needs its client and its month (YYYY-MM)"))
	}
	if c.Signoff != nil && strings.TrimSpace(c.Signoff.Description) == "" {
		problems = append(problems, errors.New("a sign-off step says what sign-off covers"))
	}
	if l := c.Setup.QuickBooks; l != nil {
		problems = append(problems, l.check()...)
	}
	if c.ExpectReady && c.Signoff == nil {
		problems = append(problems, errors.New("a close expected ready needs its sign-off step: without one nothing defines the scope"))
	}
	if len(c.Documents) == 0 {
		problems = append(problems, errors.New("a case needs at least one document"))
	}
	files, bodies := map[string]bool{}, map[string]string{}
	for _, d := range c.Documents {
		if d.File == "" || filepath.Base(d.File) != d.File || files[d.File] {
			problems = append(problems, fmt.Errorf("document %q: a file name in the case's directory, once", d.File))
			continue
		}
		files[d.File] = true
		_, body, err := c.Upload(d.File)
		if err != nil {
			problems = append(problems, fmt.Errorf("document %s: %w", d.File, err))
		}
		// The same uploaded bytes again are one document to the app (two workbooks with the same first sheet are too);
		// the label must say the copy is a duplicate.
		if earlier, ok := bodies[string(body)]; ok && err == nil && d.Label.Exception != "duplicate" {
			problems = append(problems, fmt.Errorf("document %s repeats %s byte for byte as uploaded: label it a duplicate", d.File, earlier))
		} else if !ok && err == nil {
			bodies[string(body)] = d.File
		}
		for _, recorded := range []string{d.Model, d.Selection} {
			if recorded == "" {
				continue
			}
			if _, err := os.Stat(filepath.Join(c.Dir, recorded)); err != nil || filepath.Base(recorded) != recorded {
				problems = append(problems, fmt.Errorf("document %s: recorded answer %q is not a file beside it", d.File, recorded))
			}
		}
		if strings.TrimSpace(d.Label.WorkType) == "" {
			problems = append(problems, fmt.Errorf("document %s: label its work type, or none", d.File))
		}
		if _, known := Exceptions[d.Label.Exception]; d.Label.Exception != "" && !known {
			problems = append(problems, fmt.Errorf("document %s: the harness cannot yet tell whether the app found %q; known: %s", d.File, d.Label.Exception, strings.Join(sortedKeys(Exceptions), ", ")))
		}
	}
	return errors.Join(problems...)
}
