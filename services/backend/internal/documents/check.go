package documents

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var extractedPage = regexp.MustCompile(`(?m)^--- Page ([0-9]+) ·`)
var printedPage = regexp.MustCompile(`(?im)^\s*Page\s+([0-9]+)\s+of\s+([0-9]+)\s*$`)
var factLine = regexp.MustCompile(`(?im)^\s*(invoice number|invoice date|total amount|currency|service start|service end)\s*:\s*(\S[^\r\n]*)$`)

type CheckResult struct {
	Pages     int      `json:"pages"`
	Warnings  []string `json:"warnings"`
	Questions []string `json:"questions"`
}

// Check reports only explicit page-number and labeled-value conflicts; it does
// not infer that an unnumbered document is complete or choose a correct value.
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
