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

	if !isInsideEssential(text, line) {
		return nil, nil
	}

	// Determine prefix typed so far.
	lines := strings.Split(text, "\n")
	prefix := ""
	if line < len(lines) {
		l := lines[line]
		// Strip the leading "- " marker.
		trimmed := strings.TrimLeft(l, " \t")
		if strings.HasPrefix(trimmed, "- ") {
			trimmed = trimmed[2:]
		}
		// Trim to cursor position.
		col := char
		if col > len(l) {
			col = len(l)
		}
		afterDash := strings.Index(l[:col], "- ")
		if afterDash >= 0 {
			prefix = l[afterDash+2 : col]
		}
	}

	var items []protocol.CompletionItem
	kind := protocol.CompletionItemKindReference
	for _, ref := range s.idx.AllSliceRefs() {
		if strings.HasPrefix(ref, prefix) {
			label := ref
			items = append(items, protocol.CompletionItem{
				Label: label,
				Kind:  &kind,
			})
		}
	}

	return items, nil
}
