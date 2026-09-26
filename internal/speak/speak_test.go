package speak

import (
	"strings"
	"testing"
)

func TestExtractSummary(t *testing.T) {
	long := strings.Repeat("word ", 200)
	cases := []struct {
		name    string
		message string
		want    string
		valid   bool
	}{
		{
			name:    "valid marker",
			message: "Done.\n\n<!-- turnecho-summary:v1\nFixed the bug.\n-->\n",
			want:    "Fixed the bug.",
			valid:   true,
		},
		{
			name:    "crlf and trailing whitespace",
			message: "Done.\r\n\r\n<!-- turnecho-summary:v1\r\nFixed the bug.\r\n-->\r\n   \t\n",
			want:    "Fixed the bug.",
			valid:   true,
		},
		{
			name:    "whitespace collapses",
			message: "Done.\n\n<!-- turnecho-summary:v1\nFixed  the\n\tbug.\n-->\n",
			want:    "Fixed the bug.",
			valid:   true,
		},
		{
			name:    "last marker wins",
			message: "<!-- turnecho-summary:v1\nFirst.\n-->\n\n<!-- turnecho-summary:v1\nSecond.\n-->\n",
			want:    "Second.",
			valid:   true,
		},
		{
			name:    "missing marker",
			message: "Done with no marker.",
			valid:   false,
		},
		{
			name:    "marker not trailing",
			message: "<!-- turnecho-summary:v1\nFixed.\n-->\nMore text.",
			valid:   false,
		},
		{
			name:    "marker inside body",
			message: "Done.\n\n<!-- turnecho-summary:v1\nFixed <!-- oops\n-->\n",
			valid:   false,
		},
		{
			name:    "closer inside body",
			message: "Done.\n\n<!-- turnecho-summary:v1\nFixed --> oops\n-->\n",
			valid:   false,
		},
		{
			name:    "empty summary",
			message: "Done.\n\n<!-- turnecho-summary:v1\n   \n-->\n",
			valid:   false,
		},
		{
			name:    "too long",
			message: "Done.\n\n<!-- turnecho-summary:v1\n" + long + "\n-->\n",
			valid:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, valid := ExtractSummary(tc.message)
			if valid != tc.valid {
				t.Fatalf("valid=%v, want %v", valid, tc.valid)
			}
			if valid && got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInstructionFormat(t *testing.T) {
	if !strings.HasSuffix(Instruction, "lists inside the summary.\n") {
		t.Error("instruction must end with the summary rules line")
	}
	if !strings.Contains(Instruction, "<!-- turnecho-summary:v1\n<the_agent_summary_here>\n-->") {
		t.Error("instruction must show the exact marker format")
	}
}
