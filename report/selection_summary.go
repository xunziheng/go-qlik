package report

import "strings"

// Restore the observed comma escape in Qlik's selection summary. Other
// backslashes are literal content; JSON escaping was already decoded on input.
func decodeSelectionSummary(summary string) string {
	return strings.ReplaceAll(summary, `\,`, `,`)
}
