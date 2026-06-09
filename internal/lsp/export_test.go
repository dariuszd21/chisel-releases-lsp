package lsp

import (
	"github.com/dariuszd21/chisel-releases-lsp/internal/index"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/dariuszd21/chisel-releases-lsp/internal/parser"
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

// ExportReferencesWithDecl calls computeReferences with includeDeclaration=true.
func (s *Server) ExportReferencesWithDecl(filePath, text string, line, char int) []protocol.Location {
	return s.computeReferences(filePath, text, line, char, true)
}

// ExportReindexAndPublish exposes reindexAndPublish for testing.
func (s *Server) ExportReindexAndPublish(n Notifier, filePath string, content []byte) {
	s.reindexAndPublish(n, filePath, content)
}

// ExportPublishDiagnosticsForFile exposes publishDiagnosticsForFile for testing.
func (s *Server) ExportPublishDiagnosticsForFile(n Notifier, filePath string) {
	s.publishDiagnosticsForFile(n, filePath)
}

// ExportRepublishOpenFiles exposes republishOpenFiles for testing.
func (s *Server) ExportRepublishOpenFiles(n Notifier, skipPath string) {
	s.republishOpenFiles(n, skipPath)
}
func (s *Server) SetDocForTest(filePath, text string) {
	s.setDoc(filePath, text)
}

// ExportRename calls textDocumentRename with the given params.
func (s *Server) ExportRename(filePath string, line, char int, newName string) (*protocol.WorkspaceEdit, error) {
	uri := filePathToURI(filePath)
	return s.textDocumentRename(nil, &protocol.RenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: uint32(line), Character: uint32(char)},
		},
		NewName: newName,
	})
}

// ExportPrepareRename calls textDocumentPrepareRename with the given params.
func (s *Server) ExportPrepareRename(filePath string, line, char int) (any, error) {
	uri := filePathToURI(filePath)
	return s.textDocumentPrepareRename(nil, &protocol.PrepareRenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: uint32(line), Character: uint32(char)},
		},
	})
}

// ExportCodeAction calls computeCodeActions with the given file path and
// client diagnostics, returning the resulting quick-fix actions.
func (s *Server) ExportCodeAction(filePath string, clientDiags []protocol.Diagnostic) []protocol.CodeAction {
	uri := filePathToURI(filePath)
	return s.computeCodeActions(filePath, uri, clientDiags)
}

// ExportCompletion calls computeCompletion for testing.
func (s *Server) ExportCompletion(filePath, text string, line, char int) []protocol.CompletionItem {
	uri := filePathToURI(filePath)
	return s.computeCompletion(text, uri, line, char)
}

// ExportCompletionPrefixAndRange exposes completionPrefixAndRange for testing.
func ExportCompletionPrefixAndRange(text string, line, char int) (string, protocol.Range, bool, bool) {
	return completionPrefixAndRange(text, line, char)
}

// ExportComputeDiagnostics exposes computeDiagnostics for testing.
func ExportComputeDiagnostics(s *Server, filePath string) []protocol.Diagnostic {
	return s.computeDiagnostics(filePath)
}

// SetMinPrefixLen sets the minimum prefix length for completion for testing.
func (s *Server) SetMinPrefixLen(n int) {
	s.configMu.Lock()
	s.config.MinPrefixLen = n
	s.configMu.Unlock()
}

// ExportApplySettings exposes applySettings for testing.
func (s *Server) ExportApplySettings(v any) {
	s.applySettings(v)
}

// ExportHover calls textDocumentHover with the given file path and position.
// Call SetDocForTest first if the file content hasn't been written to disk yet.
func (s *Server) ExportHover(filePath string, line, char int) (*protocol.Hover, error) {
	uri := filePathToURI(filePath)
	return s.textDocumentHover(nil, &protocol.HoverParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: uint32(line), Character: uint32(char)},
		},
	})
}
