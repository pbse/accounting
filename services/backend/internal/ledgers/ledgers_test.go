package ledgers

import "testing"

// The catalog lists every ledger a client can keep, each with its export steps; a client's record holds a catalog
// entry, another ledger by name, or not known yet; and a connector is switched on per environment, or for a firm piloting it.
func TestTheCatalogAndItsSwitches(t *testing.T) {
	if len(Catalog()) < 12 {
		t.Fatalf("the catalog lists the ledgers firms keep: %d", len(Catalog()))
	}
	for _, l := range Catalog() {
		if l.Exports.TrialBalance == "" || l.Exports.GeneralLedger == "" || l.Exports.OpenItems == "" {
			t.Fatalf("%s has every export's steps", l.ID)
		}
	}
	if qbo, ok := Find("quickbooks_online"); !ok || qbo.Connector != "quickbooks" {
		t.Fatal("QuickBooks Online is read by its connector")
	}
	if desktop, ok := Find("quickbooks_desktop"); !ok || desktop.Connector != "" {
		t.Fatal("QuickBooks Desktop has no connector")
	}
	for _, c := range []struct {
		ledger, other string
		valid         bool
	}{{"sage_50", "", true}, {"sage_50", "Sage", false}, {Other, "Acumatica", true}, {Other, " ", false}, {Unknown, "", true}, {Unknown, "x", false}, {"nonsense", "", false}} {
		if Valid(c.ledger, c.other) != c.valid {
			t.Fatalf("Valid(%q, %q) is %v", c.ledger, c.other, c.valid)
		}
	}
	t.Setenv("LEDGER_CONNECTORS", "quickbooks")
	yes, no := true, false
	if !Switched("quickbooks", nil) || Switched("xero", nil) || !Switched("xero", &yes) || Switched("quickbooks", &no) || Switched("conductor", &yes) || Switched("", &yes) {
		t.Fatal("the environment's list switches connectors on; a pilot switches on one this build has, for its firm")
	}
	// Empty, as a deployment passes a setting it was not given, is the default: every connector on; "none" is none.
	t.Setenv("LEDGER_CONNECTORS", "")
	if !Switched("quickbooks", nil) || !Switched("xero", nil) {
		t.Fatal("an empty setting switches every connector on")
	}
	t.Setenv("LEDGER_CONNECTORS", "none")
	if Switched("quickbooks", nil) || Switched("xero", nil) {
		t.Fatal("none switches every connector off")
	}
}
