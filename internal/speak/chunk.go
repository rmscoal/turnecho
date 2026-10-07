package speak

import (
	"strings"
	"unicode/utf8"
)

// ChunkChars bounds synthesis latency. A single longer word stays intact.
const ChunkChars = 300

// Chunks splits prose at sentence boundaries, falling back to word boundaries.
// Whitespace is normalized; decimals and common abbreviations stay intact.
func Chunks(text string) []string {
	var chunks []string
	var words []string
	length := 0
	flush := func() {
		if len(words) > 0 {
			chunks = append(chunks, strings.Join(words, " "))
			words = nil
			length = 0
		}
	}
	for _, word := range strings.Fields(text) {
		n := utf8.RuneCountInString(word)
		if len(words) > 0 && length+1+n > ChunkChars {
			flush()
		}
		if len(words) > 0 {
			length++
		}
		words = append(words, word)
		length += n
		if sentenceEnd(word) {
			flush()
		}
	}
	flush()
	return chunks
}

func sentenceEnd(word string) bool {
	word = strings.TrimRight(word, "\"'”’)]}")
	if strings.HasSuffix(word, "!") || strings.HasSuffix(word, "?") {
		return true
	}
	if !strings.HasSuffix(word, ".") {
		return false
	}
	switch strings.ToLower(word) {
	case "mr.", "mrs.", "ms.", "dr.", "prof.", "e.g.", "i.e.", "vs.":
		return false
	}
	// Keep initials such as A. Smith together.
	if len(word) == 2 && word[0] >= 'A' && word[0] <= 'Z' {
		return false
	}
	return true
}
