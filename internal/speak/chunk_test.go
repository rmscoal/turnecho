package speak

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestChunks(t *testing.T) {
	for _, test := range []struct {
		text string
		want []string
	}{
		{" \n\t", nil},
		{"Hello there. How are you? All good!", []string{"Hello there.", "How are you?", "All good!"}},
		{"Dr. Smith uses version 2.3. It works.", []string{"Dr. Smith uses version 2.3.", "It works."}},
		{"Use e.g. Go and SQL. Then test.", []string{"Use e.g. Go and SQL.", "Then test."}},
		{"A. Smith said \"Done.\" Next step.", []string{"A. Smith said \"Done.\"", "Next step."}},
		{"hello\nworld without punctuation", []string{"hello world without punctuation"}},
	} {
		if got := Chunks(test.text); !reflect.DeepEqual(got, test.want) {
			t.Errorf("Chunks(%q) = %q, want %q", test.text, got, test.want)
		}
	}
}

func TestChunksBoundedWithoutLosingWords(t *testing.T) {
	text := strings.Repeat("工程 hello ", 200)
	chunks := Chunks(text)
	if len(chunks) < 2 {
		t.Fatal("long prose was not split")
	}
	for _, chunk := range chunks {
		if utf8.RuneCountInString(chunk) > ChunkChars {
			t.Fatalf("chunk exceeds limit: %d", utf8.RuneCountInString(chunk))
		}
	}
	if strings.Join(chunks, " ") != strings.Join(strings.Fields(text), " ") {
		t.Fatal("chunking changed text order or lost words")
	}
	longWord := strings.Repeat("a", ChunkChars+10)
	if got := Chunks("hello " + longWord + " goodbye"); !reflect.DeepEqual(got, []string{"hello", longWord, "goodbye"}) {
		t.Errorf("long word was split: %q", got)
	}
}
