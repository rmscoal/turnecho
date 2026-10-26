// Package speak validates TurnEcho summary markers in agent messages.
//
// Every host speaks summaries through the same trailing marker contract.
package speak

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// OpenMarker starts the summary block the prompt hook asks agents to append.
const OpenMarker = "<!-- turnecho-summary:v1\n"

// CloseMarker ends the summary block.
const CloseMarker = "\n-->"

// MaxChars bounds the spoken summary length.
const MaxChars = 500

// Instruction is the additional context the prompt hook injects.
const Instruction = `
At the end of your final response, append exactly one TurnEcho summary using this format:


<!-- turnecho-summary:v1
<the_agent_summary_here>
-->



Write 1 to 3 short spoken sentences, maximum 60 words.
Use plain conversational language.
Include the outcome, important blocker, or next action.
Do not use Markdown, URLs, file paths, code, IDs, or lists inside the summary.
`

// ExtractSummary returns the validated summary at the end of a message.
// It reports false when the marker is missing or invalid.
func ExtractSummary(message string) (string, bool) {
	normalized := strings.TrimRightFunc(strings.ReplaceAll(message, "\r\n", "\n"), unicode.IsSpace)
	if !strings.HasSuffix(normalized, CloseMarker) {
		return "", false
	}

	start := strings.LastIndex(normalized, OpenMarker)
	if start == -1 {
		return "", false
	}

	raw := normalized[start+len(OpenMarker) : len(normalized)-len(CloseMarker)]
	if strings.Contains(raw, "<!--") || strings.Contains(raw, "-->") {
		return "", false
	}

	summary := strings.Join(strings.Fields(raw), " ")
	if summary == "" {
		return "", false
	}
	if utf8.RuneCountInString(summary) > MaxChars {
		return "", false
	}
	return summary, true
}
