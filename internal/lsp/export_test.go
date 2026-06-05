package lsp

import (
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
