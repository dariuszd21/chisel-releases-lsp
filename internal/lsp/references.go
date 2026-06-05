package lsp

import (
	"os"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/dariuszd21/chisel-releases-lsp/internal/parser"
)

func (s *Server) textDocumentReferences(_ *glsp.Context, params *protocol.ReferenceParams) ([]protocol.Location, error) {
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

	pkg, sliceName := resolveRefTarget(s, filePath, token)
	if pkg == "" {
		return nil, nil
	}

	indexed := s.idx.FindReferences(pkg, sliceName)
	if len(indexed) == 0 {
		return []protocol.Location{}, nil
	}

	locs := make([]protocol.Location, 0, len(indexed))
	for _, r := range indexed {
		locs = append(locs, protocol.Location{
			URI:   filePathToURI(r.File),
			Range: toProtocolRange(r.Range),
		})
	}
	return locs, nil
}

// resolveRefTarget resolves the cursor token to a (pkg, sliceName) pair.
//
// Two contexts are handled:
//   - The token is already a "pkg_slice" reference (e.g. inside an essential list) →
//     SliceRefFromToken returns the pair directly.
//   - The token is a bare slice name (e.g. the cursor is on the "libs:" key in the
//     slices: section) → pair it with the package declared in the current file.
func resolveRefTarget(s *Server, filePath, token string) (pkg, sliceName string) {
	pkg, sliceName = parser.SliceRefFromToken(token)
	if pkg != "" {
		return pkg, sliceName
	}

	// Bare slice name: look up the package declared in the current file.
	sf := s.idx.FileSliceFile(filePath)
	if sf == nil || sf.Package == "" {
		return "", ""
	}
	return sf.Package, token
}
