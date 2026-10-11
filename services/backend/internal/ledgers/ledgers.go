// Package ledgers is the ledger catalog (docs/books-sources.md, Ledger catalog and onboarding): every ledger a client
// can keep, as configuration rather than code per ledger. For each it records its name, its native connector if one
// exists, the steps that export its Trial Balance, General Ledger and open items, its journal import format and what it
// cannot do natively. A client's ledger is recorded at onboarding; a ledger whose connector is not switched on is served
// by files, with its own export steps.
package ledgers

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
)

// Ledger is one catalog entry.
type Ledger struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Connector is the native connector that reads this ledger (quickbooks, xero), or "" when it has none.
	Connector string `json:"connector"`
	// Exports are the steps that export the month's Trial Balance, General Ledger and open items.
	Exports struct {
		TrialBalance  string `json:"trial_balance"`
		GeneralLedger string `json:"general_ledger"`
		OpenItems     string `json:"open_items"`
	} `json:"exports"`
	// JournalImport names the ledger's own journal import format, or "" when it has none.
	JournalImport string `json:"journal_import"`
	// NativeGaps are what the ledger cannot do natively, said plainly.
	NativeGaps []string `json:"native_gaps"`
}

// Other and Unknown are the two choices that are not catalog entries: a ledger the catalog does not list (named by
// the person), and not known yet.
const (
	Other   = "other"
	Unknown = "unknown"
)

//go:embed catalog.json
var raw []byte

var catalog []Ledger

// connectors are the native connectors this build has.
var connectors = []string{"quickbooks", "xero"}

var id = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)

func init() {
	var file struct {
		Version int      `json:"version"`
		Ledgers []Ledger `json:"ledgers"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		panic(fmt.Sprintf("ledger catalog: %v", err))
	}
	seen := map[string]bool{}
	for _, l := range file.Ledgers {
		if !id.MatchString(l.ID) || l.ID == Other || l.ID == Unknown || seen[l.ID] || strings.TrimSpace(l.Name) == "" || (l.Connector != "" && !slices.Contains(connectors, l.Connector)) {
			panic(fmt.Sprintf("ledger catalog: invalid entry %q", l.ID))
		}
		seen[l.ID] = true
	}
	catalog = file.Ledgers
}

// Catalog is every ledger, in the catalog's order.
func Catalog() []Ledger { return slices.Clone(catalog) }

// Find is the catalog entry with id.
func Find(id string) (Ledger, bool) {
	i := slices.IndexFunc(catalog, func(l Ledger) bool { return l.ID == id })
	if i < 0 {
		return Ledger{}, false
	}
	return catalog[i], true
}

// Valid says whether ledger is a choice a client's record can hold: a catalog entry, Other with a name, or Unknown.
func Valid(ledger, otherName string) bool {
	name := strings.TrimSpace(otherName)
	switch ledger {
	case Other:
		return name != "" && len([]rune(name)) <= 120
	case Unknown:
		return name == ""
	}
	_, ok := Find(ledger)
	return ok && name == ""
}

// Switched says whether connector is switched on for a firm: as staff set it for the firm, when they did (pilot is the
// firm's latest pilot, nil for none), else as this environment does. LEDGER_CONNECTORS lists the connectors the
// environment switches on (comma-separated; unset means quickbooks and xero, the two that have passed their
// acceptance). Only connectors this build has can be on.
func Switched(connector string, pilot *bool) bool {
	if connector == "" || !slices.Contains(connectors, connector) {
		return false
	}
	if pilot != nil {
		return *pilot
	}
	return environment(connector)
}

// environment says whether this environment switches connector on.
func environment(connector string) bool {
	on, set := os.LookupEnv("LEDGER_CONNECTORS")
	if !set {
		return true
	}
	for _, c := range strings.Split(on, ",") {
		if strings.TrimSpace(c) == connector {
			return true
		}
	}
	return false
}

// SwitchedSQL is SQL saying whether connector is switched on for the firm whose ID is the SQL expression org (a
// parameter or a column qualified by its table, never a bare column, which would resolve inside the lookup): its
// latest pilot, else this environment's switch. Connecting checks it.
func SwitchedSQL(org, connector string) string {
	if !slices.Contains(connectors, connector) {
		return "false"
	}
	return fmt.Sprintf(`COALESCE((SELECT p.enabled FROM connector_pilots p WHERE p.organization_id=%s AND p.connector='%s' ORDER BY p.id DESC LIMIT 1),%t)`, org, connector, environment(connector))
}
