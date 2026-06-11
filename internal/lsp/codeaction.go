package lsp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

func (s *Server) textDocumentCodeAction(_ *glsp.Context, params *protocol.CodeActionParams) (any, error) {
	filePath, err := uriToPath(string(params.TextDocument.URI))
	if err != nil {
		return nil, nil
	}
	return s.computeCodeActions(filePath, params.TextDocument.URI, params.Context.Diagnostics), nil
}

// computeCodeActions maps a set of (client-reflected) diagnostics to quick-fix
// CodeActions. It reads the document from the in-memory store (or disk as
// fallback) to validate that targeted lines have the expected YAML structure
// before offering edits.
func (s *Server) computeCodeActions(
	filePath string,
	uri protocol.DocumentUri,
	clientDiags []protocol.Diagnostic,
) []protocol.CodeAction {
	quickFix := protocol.CodeActionKindQuickFix
	trueVal := true

	// Document text is needed to validate line structure and compute last-line ranges.
	// Fall back to disk if the file isn't in the in-memory store (e.g. code action
	// triggered before textDocument/didOpen was processed).
	doc, ok := s.getDoc(filePath)
	if !ok {
		data, err := os.ReadFile(filePath)
		if err == nil {
			doc = string(data)
		}
	}
	lines := strings.Split(doc, "\n")

	// glsp's IntegerOrString.UnmarshalJSON uses a value receiver, so the
	// diagnostic Code.Value is always nil after JSON deserialization. Re-compute
	// diagnostics server-side to build a reliable line→code map and use that
	// instead of the (broken) client-reflected codes.
	lineCode := make(map[uint32]string)
	for _, d := range s.computeDiagnostics(filePath) {
		if d.Code != nil {
			if c, ok := d.Code.Value.(string); ok {
				lineCode[d.Range.Start.Line] = c
			}
		}
	}

	var actions []protocol.CodeAction
	for _, diag := range clientDiags {
		// Resolve code: prefer freshly-computed server value; fall back to
		// whatever the client sent (works if the client fixed the round-trip).
		code := lineCode[diag.Range.Start.Line]
		if code == "" && diag.Code != nil {
			code, _ = diag.Code.Value.(string)
		}
		switch code {
		case DiagCodeUnknownSliceRef, DiagCodeInvalidSliceRef:
			lineNum := int(diag.Range.Start.Line)
			// Offer the remove action for both v1/v2 list items ("  - pkg_slice")
			// and v3 map entries ("  pkg_slice:" or "  badref:").
			// Diagnostics for these codes are always on essential-block lines so
			// deletion is safe; the guard is just a sanity check against corrupt input.
			if !isListItemLine(lines, lineNum) && !isYAMLMapKeyLine(lines, lineNum) {
				continue
			}
			title := "Remove unknown reference"
			if code == DiagCodeInvalidSliceRef {
				title = "Remove invalid reference"
			}
			editRange := fullLineDeleteRange(lines, lineNum)
			actions = append(actions, protocol.CodeAction{
				Title:       title,
				Kind:        &quickFix,
				Diagnostics: []protocol.Diagnostic{diag},
				IsPreferred: &trueVal,
				Edit: &protocol.WorkspaceEdit{
					Changes: map[protocol.DocumentUri][]protocol.TextEdit{
						uri: {{Range: editRange, NewText: ""}},
					},
				},
			})

		case DiagCodePackageNameMismatch:
			stem := strings.TrimSuffix(filepath.Base(filePath), ".yaml")
			actions = append(actions, protocol.CodeAction{
				Title:       fmt.Sprintf("Fix package name to %q", stem),
				Kind:        &quickFix,
				Diagnostics: []protocol.Diagnostic{diag},
				IsPreferred: &trueVal,
				Edit: &protocol.WorkspaceEdit{
					Changes: map[protocol.DocumentUri][]protocol.TextEdit{
						uri: {{Range: diag.Range, NewText: stem}},
					},
				},
			})
		}
	}
	return actions
}

// isListItemLine reports whether the line at lineNum (0-indexed, within lines)
// is a YAML block sequence entry — i.e. its non-whitespace prefix is "- ".
func isListItemLine(lines []string, lineNum int) bool {
	if lineNum < 0 || lineNum >= len(lines) {
		return false
	}
	return strings.HasPrefix(strings.TrimLeft(lines[lineNum], " \t"), "- ")
}

// isYAMLMapKeyLine reports whether the line at lineNum is an indented YAML
// mapping key (e.g. "  pkg_slice:" or "  badformat:"). It matches any line
// whose trimmed content contains ":" and doesn't start with "-" or "#".
// Used to identify v3-style essential entries for code-action deletion.
func isYAMLMapKeyLine(lines []string, lineNum int) bool {
	if lineNum < 0 || lineNum >= len(lines) {
		return false
	}
	trimmed := strings.TrimSpace(lines[lineNum])
	return trimmed != "" &&
		!strings.HasPrefix(trimmed, "-") &&
		!strings.HasPrefix(trimmed, "#") &&
		strings.Contains(trimmed, ":")
}

// fullLineDeleteRange returns a Range that, when replaced with an empty string,
// removes the entire line at lineNum including its trailing newline.
// For the last line (no trailing newline), the range ends at the last character.
func fullLineDeleteRange(lines []string, lineNum int) protocol.Range {
	start := protocol.Position{Line: uint32(lineNum), Character: 0}
	if lineNum+1 < len(lines) {
		return protocol.Range{
			Start: start,
			End:   protocol.Position{Line: uint32(lineNum + 1), Character: 0},
		}
	}
	// Last line — delete to end of line content (no following newline to consume).
	return protocol.Range{
		Start: start,
		End:   protocol.Position{Line: uint32(lineNum), Character: uint32(len(lines[lineNum]))},
	}
}
