package lsp

import (
	"os"
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

func (s *Server) textDocumentCompletion(_ *glsp.Context, params *protocol.CompletionParams) (any, error) {
	if s.idx == nil {
		return nil, nil
	}

	filePath, err := uriToPath(string(params.TextDocument.URI))
	if err != nil {
		return nil, nil
	}

	// Get document text (prefer in-memory over disk).
	text, ok := s.getDoc(filePath)
	if !ok {
		data, readErr := os.ReadFile(filePath)
		if readErr != nil {
			return nil, nil
		}
		text = string(data)
	}

	line := int(params.Position.Line)
	char := int(params.Position.Character)

	return s.computeCompletion(text, filePathToURI(filePath), line, char), nil
}

// computeCompletion returns completion items for a given cursor position,
// or nil if the position is not inside an essential: list.
func (s *Server) computeCompletion(text string, uri protocol.DocumentUri, line, char int) []protocol.CompletionItem {
	if !isInsideEssential(text, line) {
		return nil
	}

	// Compute the prefix typed after "- " and the replacement range.
	prefix, editRange, needsLeadingSpace := completionPrefixAndRange(text, line, char)

	var items []protocol.CompletionItem
	kind := protocol.CompletionItemKindReference
	for _, ref := range s.idx.AllSliceRefs() {
		if strings.HasPrefix(ref, prefix) {
			ref := ref
			newText := ref
			if needsLeadingSpace {
				// Trigger fired on bare "-" (no space typed yet). Insert a
				// space before the ref so the result is valid YAML: "- ref".
				newText = " " + ref
			}
			items = append(items, protocol.CompletionItem{
				Label: ref,
				Kind:  &kind,
				// Provide an explicit TextEdit so editors replace only the
				// token after "- " and do not clobber the list-item marker.
				TextEdit: protocol.TextEdit{
					Range:   editRange,
					NewText: newText,
				},
			})
		}
	}

	return items
}

// completionPrefixAndRange returns the text already typed after the "- " list
// marker (used to filter candidates), the edit range that should be replaced
// when a completion item is accepted, and whether a leading space must be
// prepended to the NewText (true when the trigger fired on bare "-" with no
// space typed yet).
//
// For a line like "      - libc6" with cursor at column 14:
//   - prefix          = "libc6"
//   - editRange       = {line, 8} → {line, 14}   (replaces "libc6" only)
//   - needsLeadingSpace = false
//
// For a trigger on "      -" (no space yet, cursor at column 7):
//   - prefix          = ""
//   - editRange       = {line, 7} → {line, 7}   (zero-width insert after "-")
//   - needsLeadingSpace = true   (caller must use " "+ref as NewText)
func completionPrefixAndRange(text string, line, char int) (prefix string, editRange protocol.Range, needsLeadingSpace bool) {
	lines := strings.Split(text, "\n")
	// Default: zero-width insert at cursor.
	pos := protocol.Position{Line: uint32(line), Character: uint32(char)}
	editRange = protocol.Range{Start: pos, End: pos}

	if line >= len(lines) {
		return "", editRange, false
	}
	l := lines[line]
	col := char
	if col > len(l) {
		col = len(l)
	}

	// Locate the "- " marker to find where the value token begins.
	afterDash := strings.Index(l[:col], "- ")
	valueStart := -1
	if afterDash >= 0 {
		valueStart = afterDash + 2 // character right after "- "
	} else if dashOnly := strings.Index(l[:col], "-"); dashOnly >= 0 {
		// Trigger character fired on "-" before the space was typed.
		// Edit range starts right after the "-"; caller must prepend " ".
		valueStart = dashOnly + 1
		needsLeadingSpace = true
	}

	if valueStart < 0 {
		return "", editRange, false
	}

	prefix = l[valueStart:col]

	// Extend the edit range rightward to cover any already-typed word chars
	// so that accepting a completion replaces the full existing token.
	wordEnd := col
	for wordEnd < len(l) && isWordChar(l[wordEnd]) {
		wordEnd++
	}

	editRange = protocol.Range{
		Start: protocol.Position{Line: uint32(line), Character: uint32(valueStart)},
		End:   protocol.Position{Line: uint32(line), Character: uint32(wordEnd)},
	}
	return prefix, editRange, needsLeadingSpace
}
