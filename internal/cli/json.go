package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/duncanite/go-bosch/internal/terminal"
)

// written prints a document whole, each line after lead: as JSON, indented
// or on one line, when it is JSON, and as the appliance's text when it is
// not. The lines are this command's; what is on each is the appliance's.
func (root *rootCommand) written(lead string, document []byte, indented bool) {
	// What follows a document is no part of it, and indenting keeps it: a
	// message that ends with a line end would print as no JSON.
	document = bytes.TrimSpace(document)
	if len(document) == 0 {
		return
	}

	var (
		laid bytes.Buffer
		err  error
	)

	if indented {
		err = json.Indent(&laid, document, "", "  ")
	} else {
		err = json.Compact(&laid, document)
	}

	if err != nil {
		terminal.Printf(root.out, "%s%s\n", lead, string(document))

		return
	}

	for line := range strings.SplitSeq(drawn(laid.Bytes()), "\n") {
		terminal.Printf(root.out, "%s%s\n", lead, line)
	}
}

// drawn is a JSON document with every character a terminal would not
// simply draw written as JSON itself writes one, \uXXXX, a pair of them
// for a character past the sixteen bits: the document reads back the same,
// and holds nothing a terminal obeys. Such a character is inside a string:
// outside one, a document that was indented or compacted, with nothing
// after it, holds spaces and line ends alone, and the line ends are kept.
//
// What terminal.Text would write for them is Go's, and two of its forms,
// \x7f and \U000e0067, are no JSON.
func drawn(document []byte) string {
	var written strings.Builder

	for _, char := range string(document) {
		if char == '\n' || unicode.IsPrint(char) {
			written.WriteRune(char)
		} else {
			written.WriteString(escaped(char))
		}
	}

	return written.String()
}

// escaped is one character as JSON escapes it: \uXXXX, and a pair of
// them, its two halves, for a character sixteen bits do not hold.
func escaped(char rune) string {
	if high, low := utf16.EncodeRune(char); high != unicode.ReplacementChar {
		return fmt.Sprintf(`\u%04x\u%04x`, high, low)
	}

	return fmt.Sprintf(`\u%04x`, char)
}
