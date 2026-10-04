package transport

import (
	"math/big"
	"strings"
	"testing"
)

func TestDraftCommentaryStatesWhatMovedFromThePacksFigures(t *testing.T) {
	compared := func(id, name, now, before string) packLine {
		a, _ := new(big.Rat).SetString(now)
		b, _ := new(big.Rat).SetString(before)
		return line(id, name, a, b, true)
	}
	pack := map[string]any{"currency": "USD", "profit_and_loss": statement{
		Sections: []packSection{{Name: "Income", Lines: []packLine{compared("4", "Sales", "800", "1000")}}, {Name: "Expense", Lines: []packLine{compared("5", "Rent", "600", "500"), compared("6", "Postage", "12", "10")}}},
		Measures: []packLine{compared("", "Net income", "188", "490")},
	}, "cash_flow": map[string]any{"reconciled": true, "net_change": packLine{Name: "Net change in cash", Amount: "100.00"}}}

	draft := draftCommentary(pack, "50")
	for _, want := range []string{"Net income $188.00, down $302.00 from $490.00.", "materiality of $50: Sales $800.00, down $200.00 from $1000.00; Rent $600.00, up $100.00 from $500.00.", "Net change in cash $100.00.", "Why:"} {
		if !strings.Contains(draft, want) {
			t.Fatalf("the draft says %q: %s", want, draft)
		}
	}
	if strings.Contains(draft, "Postage") {
		t.Fatalf("a movement below materiality is left out: %s", draft)
	}
	// Without a materiality the largest movements are named, and an unreconciled cash flow says nothing about cash.
	pack["cash_flow"] = map[string]any{"reconciled": false, "net_change": packLine{Name: "Net change in cash", Amount: "100.00"}}
	if draft = draftCommentary(pack, ""); !strings.Contains(draft, "Movements: Sales") || !strings.Contains(draft, "Postage $12.00, up $2.00") || strings.Contains(draft, "cash") {
		t.Fatalf("without materiality every movement is named, and cash only when it reconciles: %s", draft)
	}
	// An unchanged figure is not said to move.
	if flat := draftCommentary(map[string]any{"profit_and_loss": statement{Measures: []packLine{compared("", "Net income", "300", "300")}}}, ""); !strings.HasPrefix(flat, "Net income $300.00, unchanged from last month.") {
		t.Fatalf("an unchanged figure is not said to move: %s", flat)
	}
}
