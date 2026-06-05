package lsp

import (
	"github.com/canonical/chisel-releases-lsp/internal/index"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/canonical/chisel-releases-lsp/internal/parser"
)

// ExportWordAtPosition exposes wordAtPosition for testing.
func ExportWordAtPosition(text string, line, char int) string {
	return wordAtPosition(text, line, char)
}

// ExportIsInsideEssential exposes isInsideEssential for testing.
func ExportIsInsideEssential(text string, lineIdx int) bool {
	return isInsideEssential(text, lineIdx)
}

// ExportRenderSliceMarkdown exposes renderSliceMarkdown for testing.
func ExportRenderSliceMarkdown(pkg, sliceName string, sd *parser.SliceDef) string {
	return renderSliceMarkdown(pkg, sliceName, sd)
}

// ExportComputeDiagnostics exposes computeDiagnostics for testing.
func (s *Server) ExportComputeDiagnostics(filePath string) []protocol.Diagnostic {
	return s.computeDiagnostics(filePath)
}

// ExportCollectEssentialRefs exposes collectEssentialRefs for testing.
func ExportCollectEssentialRefs(sf *parser.SliceFile) []parser.EssentialRef {
	return collectEssentialRefs(sf)
}

// NewWithIndex creates a Server with a pre-populated index, for testing.
func NewWithIndex(idx *index.Index) *Server {
	s := New()
	s.idx = idx
	return s
}

// ExportURIToPath exposes uriToPath for testing.
func ExportURIToPath(uri string) (string, error) {
	return uriToPath(uri)
}

// ExportSliceDetail exposes sliceDetail for testing.
func ExportSliceDetail(sd *parser.SliceDef) string {
	return sliceDetail(sd)
}

// ExportDocumentSymbol calls textDocumentDocumentSymbol with a fake path and returns the result.
func (s *Server) ExportDocumentSymbol(filePath string) ([]protocol.DocumentSymbol, error) {
	uri := filePathToURI(filePath)
	result, err := s.textDocumentDocumentSymbol(nil, &protocol.DocumentSymbolParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
	})
	if err != nil || result == nil {
		return nil, err
	}
	syms, ok := result.([]protocol.DocumentSymbol)
	if !ok {
		return nil, nil
	}
	return syms, nil
}

// ExportWorkspaceSymbol calls workspaceSymbol with the given query.
func (s *Server) ExportWorkspaceSymbol(query string) ([]protocol.SymbolInformation, error) {
	return s.workspaceSymbol(nil, &protocol.WorkspaceSymbolParams{Query: query})
}

// ExportRevertToDisk exposes the index-revert logic of textDocumentDidClose for testing.
// It reverts the given filePath in the index back to disk state (or removes it if the file is gone).
// Returns the error from IndexFile if any.
func (s *Server) ExportRevertToDisk(filePath string) error {
	if s.idx == nil {
		return nil
	}
	return s.idx.IndexFile(filePath)
}

// ExportFilePathToURI exposes filePathToURI for testing.
func ExportFilePathToURI(p string) protocol.DocumentUri {
	return filePathToURI(p)
}

// ExportToProtocolRange exposes toProtocolRange for testing.
func ExportToProtocolRange(r parser.Range) protocol.Range {
	return toProtocolRange(r)
}

// ExportReferences calls textDocumentReferences with the given file URI and position.
func (s *Server) ExportReferences(filePath string, line, char int) ([]protocol.Location, error) {
	uri := filePathToURI(filePath)
	return s.textDocumentReferences(nil, &protocol.ReferenceParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: uint32(line), Character: uint32(char)},
		},
	})
}

// SetDocForTest injects document text into the server's docs map, simulating an open document.
func (s *Server) SetDocForTest(filePath, text string) {
	s.setDoc(filePath, text)
}
