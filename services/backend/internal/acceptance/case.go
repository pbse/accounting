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
)

// Case is one client-month.
type Case struct {
	ID          string     `json:"id"`
	Split       string     `json:"split"` // development, or held_out for cases kept out of tuning
	Client      string     `json:"client"`
	Period      string     `json:"period"`
	Tags        []string   `json:"tags,omitempty"` // what the case exercises, such as wrong_client or ocr_decimal
	Setup       Setup      `json:"setup"`
	Documents   []Document `json:"documents"`
	ExpectReady bool       `json:"expect_ready"`
	Dir         string     `json:"-"`
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

// Ledger is a QuickBooks capture of the month: the chart of accounts and the month's bills.
type Ledger struct {
	HomeCurrency string    `json:"home_currency"`
	Accounts     []Account `json:"accounts"`
	Bills        []Bill    `json:"bills"`
}

type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Bill struct {
	Vendor    string `json:"vendor"`
	Number    string `json:"number"`
	Amount    string `json:"amount"`
	Date      string `json:"date"`
	AccountID string `json:"account_id"`
}

// Document is one file of the case with its accountant's label. Model names a recorded model answer, replayed in
// recorded runs: JSON in the model's answer shape, where "$primary" stands for this document's evidence ID and
// "$source:<file>" for another document's.
type Document struct {
	File  string `json:"file"`
	Label Label  `json:"label"`
	Model string `json:"model,omitempty"`
}

// Label is what the document should yield. WorkType is the work it supports (prepaid, accrual, …) or none. Facts are the
// values a correct reading gives, by field; DraftAmount is the month's entry a correct draft proposes. Exception names a
// material problem a person must see before anything goes on (wrong_client, ocr_decimal, duplicate, …).
type Label struct {
	WorkType    string            `json:"work_type"`
	Facts       map[string]string `json:"facts,omitempty"`
	DraftAmount string            `json:"draft_amount,omitempty"`
	Exception   string            `json:"exception,omitempty"`
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
	if len(c.Documents) == 0 {
		problems = append(problems, errors.New("a case needs at least one document"))
	}
	files := map[string]bool{}
	for _, d := range c.Documents {
		if d.File == "" || filepath.Base(d.File) != d.File || files[d.File] {
			problems = append(problems, fmt.Errorf("document %q: a file name in the case's directory, once", d.File))
			continue
		}
		files[d.File] = true
		if _, err := os.Stat(filepath.Join(c.Dir, d.File)); err != nil {
			problems = append(problems, fmt.Errorf("document %s: %w", d.File, err))
		}
		if d.Model != "" {
			if _, err := os.Stat(filepath.Join(c.Dir, d.Model)); err != nil || filepath.Base(d.Model) != d.Model {
				problems = append(problems, fmt.Errorf("document %s: recorded answer %q is not a file beside it", d.File, d.Model))
			}
		}
		if strings.TrimSpace(d.Label.WorkType) == "" {
			problems = append(problems, fmt.Errorf("document %s: label its work type, or none", d.File))
		}
	}
	return errors.Join(problems...)
}
