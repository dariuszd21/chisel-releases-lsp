package lsp

import (
	"fmt"
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
// CodeActions. It reads the document from the in-memory store to validate that
// targeted lines have the expected YAML structure before offering edits.
func (s *Server) computeCodeActions(
	filePath string,
	uri protocol.DocumentUri,
	clientDiags []protocol.Diagnostic,
) []protocol.CodeAction {
	quickFix := protocol.CodeActionKindQuickFix
	trueVal := true

	// Document text is needed to validate line structure and compute last-line ranges.
	doc, _ := s.getDoc(filePath)
	lines := strings.Split(doc, "\n")

	var actions []protocol.CodeAction
	for _, diag := range clientDiags {
		if diag.Code == nil {
			continue
		}
		code, _ := diag.Code.Value.(string)
		switch code {
		case DiagCodeUnknownSliceRef, DiagCodeInvalidSliceRef:
			lineNum := int(diag.Range.Start.Line)
			// Only offer the remove action when the line is a YAML block sequence
			// item ("  - ...") to avoid mangling non-list lines.
			if !isListItemLine(lines, lineNum) {
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
