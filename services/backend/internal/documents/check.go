package documents

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

var extractedPage = regexp.MustCompile(`(?m)^--- Page ([0-9]+) ·`)
var printedPage = regexp.MustCompile(`(?im)^\s*Page\s+([0-9]+)\s+of\s+([0-9]+)\s*$`)
var factLine = regexp.MustCompile(`(?im)^\s*(invoice number|invoice date|total amount|currency|service start|service end)\s*:\s*(\S[^\r\n]*)$`)
var entityLine = regexp.MustCompile(`(?im)^\s*client entity\s*:\s*(\S[^\r\n]*)$`)
var adjacentField = regexp.MustCompile(`(?i)^(?:invoice number|invoice date|total amount|currency|service start|service end)\s*:`)

type CheckResult struct {
	Pages     int      `json:"pages"`
	Warnings  []string `json:"warnings"`
	Questions []string `json:"questions"`
}

// NormalizeEntity compares client names ignoring case, punctuation and spacing.
func NormalizeEntity(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return ' '
	}, value)), " "))
}

// EntityIssue accepts a source naming the client or one of its confirmed aliases (normalized). When the
// source names exactly one other entity, alias is that name as written, for a reviewer to confirm.
func EntityIssue(content, expected string, aliases []string) (issue, alias string) {
	normalize := NormalizeEntity
	values := map[string]string{}
	for _, match := range entityLine.FindAllStringSubmatch(content, -1) {
		value := match[1]
		if normalize(value) != normalize(expected) {
			at := strings.IndexByte(value, '\t')
			if spaces := strings.Index(value, "  "); spaces >= 0 && (at < 0 || spaces < at) {
				at = spaces
			}
			if at >= 0 && adjacentField.MatchString(strings.TrimSpace(value[at:])) {
				value = value[:at]
			}
		}
		values[normalize(value)] = strings.TrimSpace(value)
	}
	if len(values) == 0 {
		return "", ""
	}
	for name, written := range values {
		if len(values) == 1 && (name == normalize(expected) || slices.Contains(aliases, name)) {
			return "", ""
		}
		if len(values) == 1 && name != "" {
			alias = written
		}
	}
	return "The source names a different or ambiguous client entity. Compare the original with this close and upload it to the correct client before preparing.", alias
}

func QuoteLocation(name, content, quote string) (string, bool) {
	if quote == "" {
		return "", false
	}
	at := strings.Index(content, quote)
	if at < 0 || strings.LastIndex(content, quote) != at {
		return "", false
	}
	page := 0
	pageStart := 0
	if strings.HasSuffix(strings.ToLower(name), ".pdf") {
		markers := extractedPage.FindAllStringSubmatchIndex(content, -1)
		if len(markers) == 0 || markers[0][0] != 0 {
			return "", false
		}
		for i, marker := range markers {
			number, _ := strconv.Atoi(content[marker[2]:marker[3]])
			if number != i+1 {
				return "", false
			}
			if marker[0] < at {
				newline := strings.IndexByte(content[marker[1]:], '\n')
				if newline < 0 {
					return "", false
				}
				page, pageStart = number, marker[1]+newline+1
			}
		}
	}
	if page > 0 {
		if at < pageStart {
			return "", false
		}
		return fmt.Sprintf("page %d, line %d", page, strings.Count(content[pageStart:at], "\n")+1), true
	}
	return fmt.Sprintf("line %d", strings.Count(content[:at], "\n")+1), true
}

// Check reports only explicit page-number and labeled-value conflicts; it does
// not infer that an unnumbered document is complete or choose a correct value.
// ImageDisputed lists the pages of extracted PDF text whose image Gemini read differently from their text layer, as
// the extractor's own page markers say.
func ImageDisputed(content string) []int {
	pages := []int{}
	for i, at := range extractedPage.FindAllStringIndex(content, -1) {
		if marker, _, _ := strings.Cut(content[at[0]:], "\n"); strings.HasSuffix(marker, imageDiffers+" ---") {
			pages = append(pages, i+1)
		}
	}
	return pages
}

func Check(name, content string) CheckResult {
	result := CheckResult{Warnings: []string{}, Questions: []string{}}
	if strings.HasSuffix(strings.ToLower(name), ".pdf") {
		pages := extractedPage.FindAllStringSubmatchIndex(content, -1)
		result.Pages = len(pages)
		if len(pages) == 0 {
			result.Questions = append(result.Questions, "PDF page labels are unavailable; inspect the original before using facts.")
		} else {
			pagesWithPrinted := 0
			for i, page := range pages {
				end := len(content)
				if i+1 < len(pages) {
					end = pages[i+1][0]
				}
				labels := printedPage.FindAllStringSubmatch(content[page[1]:end], -1)
				if len(labels) > 0 {
					pagesWithPrinted++
				}
				for _, label := range labels {
					current, _ := strconv.Atoi(label[1])
					total, _ := strconv.Atoi(label[2])
					if current != i+1 || total != len(pages) {
						question := fmt.Sprintf("PDF page %d prints '%s'; confirm whether pages are missing or from another document.", i+1, strings.TrimSpace(label[0]))
						if !slices.Contains(result.Questions, question) {
							result.Questions = append(result.Questions, question)
						}
					}
				}
			}
			// A page whose image Gemini read differently from its text layer is named, so whoever uses the document
			// compares that page with the original: the text kept may lack, or contradict, what the page shows. A
			// statement is confirmed only once every page is recorded as checked against the original, and no draft is
			// prepared from such a document without a person confirming its facts.
			for _, page := range ImageDisputed(content) {
				result.Warnings = append(result.Warnings, fmt.Sprintf("PDF page %d: Gemini's reading of the page image differs from its PDF text; compare that page with the original before using its facts.", page))
			}
			if pagesWithPrinted == 0 {
				result.Warnings = append(result.Warnings, fmt.Sprintf("PDF has %d extracted pages but no printed page total; compare with the original to confirm completeness.", len(pages)))
			} else if pagesWithPrinted < len(pages) {
				result.Warnings = append(result.Warnings, "Some PDF pages lack printed page numbers; compare with the original to confirm completeness.")
			}
		}
	}
	values := map[string]map[string]bool{}
	for _, match := range factLine.FindAllStringSubmatch(content, -1) {
		key := strings.ToLower(match[1])
		if values[key] == nil {
			values[key] = map[string]bool{}
		}
		values[key][strings.TrimSpace(match[2])] = true
	}
	for _, key := range []string{"invoice number", "invoice date", "total amount", "currency", "service start", "service end"} {
		if len(values[key]) > 1 {
			result.Questions = append(result.Questions, "Different "+key+" values appear in this source; identify the applicable record and page before preparing.")
		}
	}
	return result
}
