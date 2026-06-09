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
func (s *Server) computeCompletion(text string, _ protocol.DocumentUri, line, char int) []protocol.CompletionItem {
	if !isInsideEssential(text, line) {
		return nil
	}

	// Compute the prefix typed after "- " and the replacement range.
	prefix, editRange, needsLeadingSpace, appendColon := completionPrefixAndRange(text, line, char)

	// Only offer completions once the user has typed enough characters to be
	// useful.  This prevents noisy popups immediately after "-" or at column 0.
	if len(prefix) < s.minPrefixLen() {
		return nil
	}

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
			} else if appendColon {
				// v3 map key context: the ref must be followed by ":" to
				// produce a valid YAML mapping key: "pkg_slice:".
				newText = ref + ":"
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
// when a completion item is accepted, whether a leading space must be prepended
// to NewText, and whether a trailing colon must be appended (v3 map key context).
//
// For a v1/v2 line like "      - libc6" with cursor at column 14:
//   - prefix          = "libc6"
//   - editRange       = {line, 8} → {line, 14}
//   - needsLeadingSpace = false
//   - appendColon      = false
//
// For a v1/v2 trigger on "      -" (no space yet, cursor at column 7):
//   - prefix          = ""
//   - editRange       = {line, 7} → {line, 7}
//   - needsLeadingSpace = true
//   - appendColon      = false
//
// For a v3 map key line "      libc6_" (cursor at column 12):
//   - prefix          = "libc6_"
//   - editRange       = {line, 6} → {line, 12}  (extended to cover any existing ":")
//   - needsLeadingSpace = false
//   - appendColon      = true  (caller must use ref+":" as NewText)
func completionPrefixAndRange(text string, line, char int) (prefix string, editRange protocol.Range, needsLeadingSpace, appendColon bool) {
	lines := strings.Split(text, "\n")
	// Default: zero-width insert at cursor.
	pos := protocol.Position{Line: uint32(line), Character: uint32(char)}
	editRange = protocol.Range{Start: pos, End: pos}

	if line >= len(lines) {
		return "", editRange, false, false
	}
	l := lines[line]
	col := char
	if col > len(l) {
		col = len(l)
	}

	// Find the leading-whitespace length so we can locate the first content char.
	leadingSpaces := 0
	for leadingSpaces < len(l) && (l[leadingSpaces] == ' ' || l[leadingSpaces] == '\t') {
		leadingSpaces++
	}

	// Cursor is inside the indentation — nothing to complete.
	if leadingSpaces > col {
		return "", editRange, false, false
	}

	var valueStart int
	// Classify the line by its first non-whitespace character.
	// A single "-" at the start of indented content is the v1/v2 list-item marker.
	// Any other first character (or an empty/whitespace-only line) is v3 map-key context.
	if leadingSpaces < len(l) && l[leadingSpaces] == '-' {
		// v1/v2 list item marker.
		if leadingSpaces+1 < len(l) && l[leadingSpaces+1] == ' ' {
			// "- " form: value starts two characters after the indent.
			valueStart = leadingSpaces + 2
		} else {
			// Bare "-" trigger: the user typed "-" but not the space yet.
			// The caller must prepend " " to produce valid YAML.
			valueStart = leadingSpaces + 1
			needsLeadingSpace = true
		}
	} else {
		// v3 map key context (or empty/whitespace-only line): value starts at
		// the first non-space position. Completion items must include a trailing colon.
		valueStart = leadingSpaces
		appendColon = true
	}

	// If valueStart is beyond the cursor the trigger fired before the value
	// position; return a zero-width cursor edit with empty prefix.
	if valueStart > col {
		return "", editRange, needsLeadingSpace, appendColon
	}

	prefix = l[valueStart:col]

	// Extend the edit range rightward to cover any already-typed word chars
	// so that accepting a completion replaces the full existing token.
	wordEnd := col
	for wordEnd < len(l) && isWordChar(l[wordEnd]) {
		wordEnd++
	}
	// For v3 map keys, also consume an existing trailing colon so the
	// replacement doesn't produce a double colon ("libc6_libs::").
	if appendColon && wordEnd < len(l) && l[wordEnd] == ':' {
		wordEnd++
	}

	editRange = protocol.Range{
		Start: protocol.Position{Line: uint32(line), Character: uint32(valueStart)},
		End:   protocol.Position{Line: uint32(line), Character: uint32(wordEnd)},
	}
	return prefix, editRange, needsLeadingSpace, appendColon
}
