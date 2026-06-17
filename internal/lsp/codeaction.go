package lsp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/dariuszd21/chisel-releases-lsp/internal/analysis"
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

		case DiagCodeSliceCollision:
			// Find the conflicting location from the collision map so we can
			// offer "Go to conflicting slice" navigation.
			if s.idx == nil {
				continue
			}
			lineNum := diag.Range.Start.Line
			for _, col := range analysis.DetectCollisions(s.idx) {
				var otherURI string
				var otherLine, otherChar uint32
				if col.FileA == filePath && col.RangeA.Start.Line == int(lineNum) {
					otherURI = string(filePathToURI(col.FileB))
					otherLine = uint32(col.RangeB.Start.Line)
					otherChar = uint32(col.RangeB.Start.Character)
				} else if col.FileB == filePath && col.RangeB.Start.Line == int(lineNum) {
					otherURI = string(filePathToURI(col.FileA))
					otherLine = uint32(col.RangeA.Start.Line)
					otherChar = uint32(col.RangeA.Start.Character)
				}
				if otherURI == "" {
					continue
				}
				actionKind := protocol.CodeActionKindEmpty
				actions = append(actions, protocol.CodeAction{
					Title:       fmt.Sprintf("Go to conflicting slice in %s", filepath.Base(col.FileA)),
					Kind:        &actionKind,
					Diagnostics: []protocol.Diagnostic{diag},
					Command: &protocol.Command{
						Title:     "Go to conflicting slice",
						Command:   CmdGotoConflict,
						Arguments: []any{otherURI, float64(otherLine), float64(otherChar)},
					},
				})
				break
			}
		case DiagCodeMissingCopyright:
			if s.idx == nil {
				continue
			}
			sf := s.idx.FileSliceFile(filePath)
			if sf == nil {
				continue
			}
			pkg := sf.Package
			copyrightRef := pkg + "_copyright"
			edit, ok := insertTopLevelCopyrightEdit(lines, pkg)
			if !ok {
				continue
			}
			actions = append(actions, protocol.CodeAction{
				Title:       fmt.Sprintf("Add %q to package essentials (covers all slices)", copyrightRef),
				Kind:        &quickFix,
				Diagnostics: []protocol.Diagnostic{diag},
				IsPreferred: &trueVal,
				Edit: &protocol.WorkspaceEdit{
					Changes: map[protocol.DocumentUri][]protocol.TextEdit{
						uri: {edit},
					},
				},
			})
		}
	}
	return actions
}

// detectEssentialFormat scans lines for an essential: block and returns "v3"
// if items are mapping keys (no leading "- "), or "v1" for sequence format.
// Defaults to "v1" when no existing items are found.
func detectEssentialFormat(lines []string) string {
	inEssential := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "essential:" {
			inEssential = true
			continue
		}
		if inEssential {
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			indent := len(line) - len(strings.TrimLeft(line, " \t"))
			if indent == 0 {
				break // left the block
			}
			if strings.HasPrefix(trimmed, "- ") {
				return "v1"
			}
			// Non-empty, indented, not a list item → v3 mapping key
			return "v3"
		}
	}
	return "v1"
}

// insertTopLevelCopyrightEdit computes a TextEdit that inserts <pkg>_copyright
// into the package-level essential: block. If no such block exists, it creates
// one after the "package:" line. The insertion format (v1 or v3) is detected
// from the existing file content.
func insertTopLevelCopyrightEdit(lines []string, pkg string) (protocol.TextEdit, bool) {
	copyrightRef := pkg + "_copyright"
	format := detectEssentialFormat(lines)

	var itemText string
	if format == "v3" {
		itemText = "  " + copyrightRef + ":\n"
	} else {
		itemText = "  - " + copyrightRef + "\n"
	}

	// Look for top-level essential: block (indent 0).
	for i, line := range lines {
		if strings.TrimSpace(line) == "essential:" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			insertPos := protocol.Position{Line: uint32(i + 1), Character: 0}
			return protocol.TextEdit{
				Range:   protocol.Range{Start: insertPos, End: insertPos},
				NewText: itemText,
			}, true
		}
	}

	// No top-level essential: block — create one after the "package:" line.
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "package:") && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			insertPos := protocol.Position{Line: uint32(i + 1), Character: 0}
			return protocol.TextEdit{
				Range:   protocol.Range{Start: insertPos, End: insertPos},
				NewText: "essential:\n" + itemText,
			}, true
		}
	}

	return protocol.TextEdit{}, false
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
