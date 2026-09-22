package reference

import _ "embed"

// Prepaid is the labeled PRD section 9 fixture, not a calculation service.
//
//go:embed prepaid.json
var Prepaid []byte
