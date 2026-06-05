package lsp

import "github.com/canonical/chisel-releases-lsp/internal/parser"

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
