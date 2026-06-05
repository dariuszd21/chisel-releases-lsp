package lsp

import (
	"fmt"
	"os"
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/canonical/chisel-releases-lsp/internal/parser"
)

func (s *Server) textDocumentHover(_ *glsp.Context, params *protocol.HoverParams) (*protocol.Hover, error) {
	if s.idx == nil {
		return nil, nil
	}

	filePath, err := uriToPath(string(params.TextDocument.URI))
	if err != nil {
		return nil, nil
	}

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

	token := wordAtPosition(text, line, char)
	if token == "" {
		return nil, nil
	}

	pkg, sliceName := parser.SliceRefFromToken(token)
	if pkg == "" {
		return nil, nil
	}

	is := s.idx.LookupSlice(pkg, sliceName)
	if is == nil {
		return nil, nil
	}

	md := renderSliceMarkdown(pkg, sliceName, is.Def)
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.MarkupKindMarkdown,
			Value: md,
		},
	}, nil
}

func renderSliceMarkdown(pkg, sliceName string, sd *parser.SliceDef) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### `%s_%s`\n\n", pkg, sliceName))
	if len(sd.Essential) > 0 {
		sb.WriteString("**Essential:**\n")
		for _, e := range sd.Essential {
			sb.WriteString(fmt.Sprintf("- `%s`\n", e.Value))
		}
		sb.WriteString("\n")
	}
	if len(sd.Contents) > 0 {
		sb.WriteString("**Contents:**\n")
		for _, c := range sd.Contents {
			sb.WriteString(fmt.Sprintf("- `%s`\n", c.Path))
		}
	}
	return sb.String()
}

