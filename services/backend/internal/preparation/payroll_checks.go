package preparation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"close.local/backend/internal/payroll"
	"close.local/backend/internal/prepaids"
)

// PayrollCheck is one check run on the close's records before a payroll draft is reviewed: passed, failed (a
// contradiction, which is also a blocker no approval overrides) or unavailable (the records cannot answer it, said so).
type PayrollCheck struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}

// CheckPayroll adds to a payroll paper the checks its reviewer reads beside the two confirmations: no other current
// payroll draft records the provider's pay dates, and, from the close month's QuickBooks capture, no journal naming the
// provider already moves wages out of the clearing account and the clearing account's cash debits equal the provider's.
// The paper cites the capture it read, so a newer capture reopens the review. revises is the draft this one replaces,
// which is not a duplicate of it; its own posted entry is corrected separately, not read as this payroll booked.
func CheckPayroll(ctx context.Context, tx *sql.Tx, closeID, period, revises string, body []byte) ([]byte, error) {
	var paper struct {
		payroll.Workpaper
		Sources []Source `json:"sources"`
	}
	if err := json.Unmarshal(body, &paper); err != nil {
		return nil, err
	}
	in := paper.Input
	checks := []PayrollCheck{}
	fail := func(name, detail string) {
		checks = append(checks, PayrollCheck{name, "failed", detail})
		paper.Blockers = append(paper.Blockers, detail)
	}

	var other string
	err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT w.id::text FROM workpapers w WHERE w.close_id::text=$1 AND w.kind='payroll' AND w.id::text<>$2
 AND lower(btrim(w.paper->'input'->>'provider'))=lower(btrim($3)) AND w.paper->'input'->>'first_pay_date'<=$5 AND $4<=w.paper->'input'->>'last_pay_date'
 AND NOT EXISTS(SELECT 1 FROM workpapers n WHERE n.close_id=w.close_id AND n.paper->>'revises'=w.id::text) ORDER BY w.id LIMIT 1),'')`,
		closeID, revises, in.Provider, in.FirstPayDate, in.LastPayDate).Scan(&other)
	if err != nil {
		return nil, err
	}
	if other != "" {
		fail("No other draft", fmt.Sprintf("Draft #%s already records %s payroll for pay dates overlapping %s to %s.", other, in.Provider, in.FirstPayDate, in.LastPayDate))
	} else {
		checks = append(checks, PayrollCheck{"No other draft", "passed", "No other current draft records " + in.Provider + " payroll for these pay dates."})
	}

	ledger, err := CloseLedger(ctx, tx, closeID)
	if err != nil {
		return nil, err
	}
	clearing, wages := []string{}, map[string]bool{}
	if ledger != nil {
		clearing = ledger.IDs[strings.ToLower(in.ClearingAccount)]
		for _, id := range ledger.IDs[strings.ToLower(in.WageAccount)] {
			wages[id] = true
		}
		paper.Sources = append(paper.Sources, Source{ID: ledger.Source.ID, Digest: ledger.Source.Digest, Name: ledger.Source.Name, Role: "period_ledger", Location: "QuickBooks capture of " + period},
			Source{ID: ledger.ChartSource.ID, Digest: ledger.ChartSource.Digest, Name: ledger.ChartSource.Name, Role: "chart", Location: "chart of accounts"})
	}
	switch {
	case ledger == nil:
		why := "The month has no QuickBooks capture to check against; confirm from the cited ledger page."
		checks = append(checks, PayrollCheck{"Not already booked", "unavailable", why}, PayrollCheck{"Clearing account debits", "unavailable", why})
	case len(clearing) != 1:
		why := "The captured chart of accounts does not name " + in.ClearingAccount + " exactly once; confirm from the cited ledger page."
		checks = append(checks, PayrollCheck{"Not already booked", "unavailable", why}, PayrollCheck{"Clearing account debits", "unavailable", why})
	default:
		booked, unnamed, debits, unreadable := clearingActivity(ledger, period, clearing[0], wages, in.Provider)
		if unreadable != "" {
			why := "A captured " + unreadable + " in " + period + " could not be read; confirm from the cited ledger page."
			checks = append(checks, PayrollCheck{"Not already booked", "unavailable", why}, PayrollCheck{"Clearing account debits", "unavailable", why})
			break
		}
		// Only an entry the revised draft itself posted is corrected separately; any other booking still blocks.
		postedRevision := false
		if revises != "" {
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sandbox_postings p WHERE p.workpaper_id::text=$1 AND p.state IN ('executing','unknown','verified')) OR EXISTS(SELECT 1 FROM production_postings p WHERE p.workpaper_id::text=$1 AND p.state IN ('executing','unknown','verified')) OR EXISTS(SELECT 1 FROM xero_postings p WHERE p.workpaper_id::text=$1 AND p.state IN ('executing','unknown','verified'))`, revises).Scan(&postedRevision); err != nil {
				return nil, err
			}
		}
		switch {
		case postedRevision:
			checks = append(checks, PayrollCheck{"Not already booked", "unavailable", "This draft revises draft #" + revises + ", whose entry was posted and is corrected separately; confirm this one from the ledger."})
		case len(wages) == 0:
			checks = append(checks, PayrollCheck{"Not already booked", "unavailable", "The captured chart of accounts does not name " + in.WageAccount + "; confirm from the cited ledger page."})
		case booked != "":
			fail("Not already booked", "The captured ledger already has a journal ("+booked+") naming "+in.Provider+" that moves wages out of "+in.ClearingAccount+" in "+period+"; this payroll looks recorded.")
		case unnamed != "":
			checks = append(checks, PayrollCheck{"Not already booked", "unavailable", "A journal (" + unnamed + ") moves wages out of " + in.ClearingAccount + " in " + period + " without naming " + in.Provider + "; confirm from the ledger whether it is this payroll."})
		default:
			checks = append(checks, PayrollCheck{"Not already booked", "passed", "No journal in the captured ledger moves wages out of " + in.ClearingAccount + " in " + period + "."})
		}
		want, _ := prepaids.Amount(in.ProviderDebits)
		if debits != want {
			fail("Clearing account debits", fmt.Sprintf("The captured ledger shows %s debited to %s in %s; the summary shows provider debits of %s.", prepaids.Money(debits), in.ClearingAccount, period, prepaids.Money(want)))
		} else {
			checks = append(checks, PayrollCheck{"Clearing account debits", "passed", fmt.Sprintf("The captured ledger shows %s debited to %s in %s, as the summary's provider debits.", prepaids.Money(debits), in.ClearingAccount, period)})
		}
	}
	// A contradiction found here leaves nothing to propose, as a blocker the calculation finds does.
	if len(paper.Blockers) > 0 {
		paper.Proposals = []prepaids.Adjustment{}
	}
	var out map[string]any
	if err = json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	out["checks"], out["blockers"], out["proposals"], out["sources"] = checks, paper.Blockers, paper.Proposals, paper.Sources
	return json.Marshal(out)
}

// clearingActivity reads the close month's captured rows for the clearing account: a journal crediting it while
// debiting a wage account (payroll recorded, named by its number or date), as booked when its memo or a line names the
// provider and as unnamed otherwise, and the cents of cash debited to it by purchases and journals (a credit purchase
// reverses its lines; an unpaid bill is not cash); or the kind of row that could not be read.
func clearingActivity(ledger *Ledger, period, clearing string, wages map[string]bool, provider string) (booked, unnamed string, debits int64, unreadable string) {
	named := wholeWord(strings.ToLower(strings.TrimSpace(provider)))
	for _, row := range ledger.Rows {
		if !strings.HasPrefix(row.Date, period+"-") {
			continue
		}
		var record struct {
			Credit      bool
			DocNumber   string
			PrivateNote string
			Line        []struct {
				Amount                        json.Number
				Description                   string
				AccountBasedExpenseLineDetail *struct{ AccountRef accountRef }
				JournalEntryLineDetail        *struct {
					PostingType string
					AccountRef  accountRef
				}
			}
		}
		if json.Unmarshal(row.Record, &record) != nil {
			return "", "", 0, row.Kind
		}
		creditsClearing, debitsWages, names := false, false, provider != "" && named.MatchString(strings.ToLower(record.PrivateNote))
		for _, line := range record.Line {
			cents, err := prepaids.Amount(line.Amount.String())
			if line.Amount != "" && err != nil {
				return "", "", 0, row.Kind
			}
			names = names || provider != "" && named.MatchString(strings.ToLower(line.Description))
			switch {
			case line.JournalEntryLineDetail != nil && line.JournalEntryLineDetail.AccountRef.Value == clearing:
				if line.JournalEntryLineDetail.PostingType == "Debit" {
					debits += cents
				} else {
					creditsClearing = true
				}
			case line.JournalEntryLineDetail != nil && line.JournalEntryLineDetail.PostingType == "Debit" && wages[line.JournalEntryLineDetail.AccountRef.Value]:
				debitsWages = true
			case line.AccountBasedExpenseLineDetail != nil && line.AccountBasedExpenseLineDetail.AccountRef.Value == clearing && row.Kind == "Purchase":
				if record.Credit {
					debits -= cents
				} else {
					debits += cents
				}
			}
		}
		if row.Kind == "JournalEntry" && creditsClearing && debitsWages {
			label := strings.TrimSpace(record.DocNumber)
			if label == "" {
				label = "dated " + row.Date
			}
			if names && booked == "" {
				booked = label
			} else if !names && unnamed == "" {
				unnamed = label
			}
		}
	}
	return booked, unnamed, debits, ""
}
