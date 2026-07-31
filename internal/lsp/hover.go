package lsp

import (
	"fmt"
	"os"
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/dariuszd21/chisel-releases-lsp/internal/parser"
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
	// Store-backed packages are fetched from a store channel rather than an
	// archive, which is essential context when reading a slice.
	if def := s.idx.FileSliceFile(is.File); def != nil && def.Store != "" {
		md += fmt.Sprintf("\n**Store:** `%s` (default track `%s`)\n", def.Store, def.DefaultTrack)
	}
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.MarkupKindMarkdown,
			Value: md,
		},
	}, nil
}

func renderSliceMarkdown(pkg, sliceName string, sd *parser.SliceDef) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "### `%s_%s`\n\n", pkg, sliceName)
	if sd.Hint != "" {
		fmt.Fprintf(&sb, "_%s_\n\n", sd.Hint)
	}
	if len(sd.Essential) > 0 {
		sb.WriteString("**Essential:**\n")
		for _, e := range sd.Essential {
			fmt.Fprintf(&sb, "- `%s`%s\n", e.Value, channelSuffix(e.Channel))
		}
		sb.WriteString("\n")
	}
	if len(sd.Contents) > 0 {
		sb.WriteString("**Contents:**\n")
		for _, c := range sd.Contents {
			fmt.Fprintf(&sb, "- `%s`%s\n", c.Path, channelSuffix(c.Channel))
		}
	}
	return sb.String()
}

// channelSuffix renders the channel constraints of an entry as a short
// parenthesised suffix, or "" when the entry applies to every channel.
func channelSuffix(patterns []parser.ChannelPattern) string {
	if len(patterns) == 0 {
		return ""
	}
	values := make([]string, 0, len(patterns))
	for _, p := range patterns {
		values = append(values, p.Value)
	}
	return " — channel `" + strings.Join(values, "`, `") + "`"
}
