package documents

import (
	"context"
	"slices"
	"strings"
)

// PageReader reads one rendered PDF page image, returning its text and the model version that read it: Gemini
// (llm.Gemini), when the worker has it configured.
type PageReader interface {
	ReadPage(ctx context.Context, png []byte) (string, string, error)
}

// sameReading says whether two readings of a page say exactly the same thing, laid out differently: the same tokens
// (runs of text between whitespace), character for character, in the same order. Only how much whitespace separates
// them, and where lines break, may differ. It decides only a text page's label: a swapped, changed, added, dropped,
// re-signed or re-split amount ("1 234" against "12 34"), a moved comma or decimal point, a moved label or a different
// word flags the page to check against the original.
func sameReading(a, b string) bool { return slices.Equal(strings.Fields(a), strings.Fields(b)) }
