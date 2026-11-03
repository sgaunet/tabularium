package chat

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// The prompt excerpt's budget, in runes.
//
// head is generous because the document text comes first and its opening is
// what identifies the document; tail is what guarantees the instructions
// survive, and the analysis prompt's vocabulary block runs to a few hundred
// bytes.
const (
	traceHead = 2 << 10
	traceTail = 1 << 10
)

// writeRole writes a prompt under a role label, one line at a time.
//
// Line by line because slog cannot do this: a TextHandler escapes the newlines
// of a multi-line value, and a prompt arrives as one unreadable run of \n. The
// label is repeated on every line so a system prompt and a user prompt stay
// told apart however long they are.
func writeRole(w io.Writer, role, prompt string) {
	if prompt == "" {
		return
	}
	for line := range strings.SplitSeq(prompt, "\n") {
		// The write is a courtesy to whoever is watching; a stderr that has gone
		// away must not fail the request that was going out anyway.
		_, _ = fmt.Fprintf(w, "  %-6s| %s\n", role, line)
	}
}

// elide drops the middle of an over-long prompt and says how much it dropped.
//
// The middle rather than the tail: the analysis prompt puts the document first
// and the vocabulary that decides the answer last, so a tail cut would hide the
// very part a reader is checking. Keeping both ends works for any prompt shape
// and asks nothing of the packages that build them.
func elide(s string) string {
	total := utf8.RuneCountInString(s)
	if total <= traceHead+traceTail {
		return s
	}

	runes := []rune(s)
	return string(runes[:traceHead]) +
		fmt.Sprintf("\n… (elided %d of %d runes) …\n", total-traceHead-traceTail, total) +
		string(runes[total-traceTail:])
}
