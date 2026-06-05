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

	locs := s.computeReferences(filePath, text, line, char, params.Context.IncludeDeclaration)
	// Return empty (not nil) so editors show "No references found" instead of an error.
	if locs == nil {
		return []protocol.Location{}, nil
	}
	return locs, nil
}

// computeReferences resolves the token at (line, char) to a (pkg, sliceName)
// pair and returns all essential-list locations that reference it.
// When includeDeclaration is true, the slice's own definition location is
// prepended to the results so the caller can navigate to the definition too.
func (s *Server) computeReferences(filePath, text string, line, char int, includeDeclaration bool) []protocol.Location {
	token := wordAtPosition(text, line, char)
	if token == "" {
		return nil
	}

	pkg, sliceName := resolveRefTarget(s, filePath, token)
	if pkg == "" {
		return nil
	}

	var locs []protocol.Location

	// Optionally prepend the definition location.
	if includeDeclaration {
		if is := s.idx.LookupSlice(pkg, sliceName); is != nil {
			locs = append(locs, protocol.Location{
				URI:   filePathToURI(is.File),
				Range: toProtocolRange(is.Def.NameRange),
			})
		}
	}

	for _, r := range s.idx.FindReferences(pkg, sliceName) {
		locs = append(locs, protocol.Location{
			URI:   filePathToURI(r.File),
			Range: toProtocolRange(r.Range),
		})
	}

	// Fallback: when no external references exist (and we haven't already
	// prepended the declaration), return the definition's own location so
	// the user always gets at least one result when calling Find References
	// on a defined slice. This prevents the confusing "No references found"
	// message when the cursor is on a slice that simply isn't used anywhere yet.
	if len(locs) == 0 {
		if is := s.idx.LookupSlice(pkg, sliceName); is != nil {
			locs = append(locs, protocol.Location{
				URI:   filePathToURI(is.File),
				Range: toProtocolRange(is.Def.NameRange),
			})
		}
	}

	return locs
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
