package lsp

import (
	"fmt"
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/dariuszd21/chisel-releases-lsp/internal/parser"
)

// textDocumentDocumentSymbol returns a hierarchy of symbols for the given file.
// For a chisel slice file it returns one Module symbol (the package) whose
// children are Key symbols, one per slice.
func (s *Server) textDocumentDocumentSymbol(_ *glsp.Context, params *protocol.DocumentSymbolParams) (any, error) {
	if s.idx == nil {
		return nil, nil
	}
	filePath, err := uriToPath(string(params.TextDocument.URI))
	if err != nil {
		return nil, nil
	}
	sf := s.idx.FileSliceFile(filePath)
	if sf == nil {
		return nil, nil
	}
	if sf.Package == "" {
		return []protocol.DocumentSymbol{}, nil
	}

	pkgRange := toProtocolRange(sf.PackageRange)

	// Build slice children in file order.
	children := make([]protocol.DocumentSymbol, 0, len(sf.SliceOrder))
	for _, name := range sf.SliceOrder {
		sd := sf.Slices[name]
		detail := sliceDetail(sd)
		sym := protocol.DocumentSymbol{
			Name:           name,
			Detail:         &detail,
			Kind:           protocol.SymbolKindKey,
			Range:          toProtocolRange(sd.NameRange),
			SelectionRange: toProtocolRange(sd.NameRange),
		}
		children = append(children, sym)
	}

	// Store-backed packages are known by their prefixed unique name, which is
	// what slice references use, so show that in the outline.
	pkgName := s.idx.PackageName(filePath)
	if pkgName == "" {
		pkgName = sf.Package
	}
	pkgSym := protocol.DocumentSymbol{
		Name:           pkgName,
		Kind:           protocol.SymbolKindModule,
		Range:          pkgRange,
		SelectionRange: pkgRange,
		Children:       children,
	}
	if sf.Store != "" {
		detail := fmt.Sprintf("store %s, default-track %s", sf.Store, sf.DefaultTrack)
		pkgSym.Detail = &detail
	}
	return []protocol.DocumentSymbol{pkgSym}, nil
}

// sliceDetail returns "N contents, M essential" for use as a symbol detail string.
func sliceDetail(sd *parser.SliceDef) string {
	var parts []string
	if n := len(sd.Contents); n > 0 {
		parts = append(parts, fmt.Sprintf("%d contents", n))
	}
	if n := len(sd.Essential); n > 0 {
		parts = append(parts, fmt.Sprintf("%d essential", n))
	}
	return strings.Join(parts, ", ")
}

// workspaceSymbol returns all pkg_slice symbols whose name contains the query
// string (case-insensitive). An empty query returns all symbols.
func (s *Server) workspaceSymbol(_ *glsp.Context, params *protocol.WorkspaceSymbolParams) ([]protocol.SymbolInformation, error) {
	if s.idx == nil {
		return nil, nil
	}
	query := strings.ToLower(params.Query)
	var results []protocol.SymbolInformation
	for _, ref := range s.idx.AllSliceRefs() {
		if query != "" && !strings.Contains(strings.ToLower(ref), query) {
			continue
		}
		pkg, sliceName := splitWorkspaceRef(ref)
		is := s.idx.LookupSlice(pkg, sliceName)
		if is == nil {
			continue
		}
		results = append(results, protocol.SymbolInformation{
			Name: ref,
			Kind: protocol.SymbolKindKey,
			Location: protocol.Location{
				URI:   filePathToURI(is.File),
				Range: toProtocolRange(is.Def.NameRange),
			},
		})
	}
	return results, nil
}

// splitWorkspaceRef splits "pkg_slice" into ("pkg", "slice") using the last underscore.
func splitWorkspaceRef(ref string) (pkg, slice string) {
	i := strings.LastIndex(ref, "_")
	if i <= 0 {
		return ref, ""
	}
	return ref[:i], ref[i+1:]
}
