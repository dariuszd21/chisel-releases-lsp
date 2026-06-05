package lsp

import (
	"fmt"

	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/dariuszd21/chisel-releases-lsp/internal/analysis"
	"github.com/dariuszd21/chisel-releases-lsp/internal/parser"
)

// publishDiagnosticsForFile computes and sends diagnostics for a single file.
func (s *Server) publishDiagnosticsForFile(n Notifier, filePath string) {
	diags := s.computeDiagnostics(filePath)
	if diags == nil {
		return
	}
	publishDiagnostics(n, filePathToURI(filePath), diags)
}

// computeDiagnostics returns the full diagnostic list for filePath.
// Returns nil (not an empty slice) when the file is not indexed.
func (s *Server) computeDiagnostics(filePath string) []protocol.Diagnostic {
	if s.idx == nil {
		return nil
	}
	sf := s.idx.FileSliceFile(filePath)
	if sf == nil {
		return nil
	}

	diags := []protocol.Diagnostic{}

	// 1. Package name ↔ filename consistency
	if d := analysis.CheckPackageName(filePath, sf); d != nil {
		diags = append(diags, protocol.Diagnostic{
			Range:    toProtocolRange(d.Range),
			Severity: severityPtr(protocol.DiagnosticSeverity(d.Severity)),
			Source:   strPtr("chisel-releases-lsp"),
			Message:  d.Message,
		})
	}

	// 2. Glob validation
	for _, d := range analysis.ValidateGlobs(filePath, sf) {
		diags = append(diags, protocol.Diagnostic{
			Range:    toProtocolRange(d.Range),
			Severity: severityPtr(protocol.DiagnosticSeverity(d.Severity)),
			Source:   strPtr("chisel-releases-lsp"),
			Message:  d.Message,
		})
	}

	// 3. Slice collision detection (cross-file; report only entries in this file)
	for _, col := range analysis.DetectCollisions(s.idx) {
		if col.FileA == filePath {
			diags = append(diags, protocol.Diagnostic{
				Range:    toProtocolRange(col.RangeA),
				Severity: severityPtr(protocol.DiagnosticSeverityWarning),
				Source:   strPtr("chisel-releases-lsp"),
				Message:  fmt.Sprintf("slice collision: path %q also claimed by %s", col.Path, col.SliceB),
			})
		}
		if col.FileB == filePath {
			diags = append(diags, protocol.Diagnostic{
				Range:    toProtocolRange(col.RangeB),
				Severity: severityPtr(protocol.DiagnosticSeverityWarning),
				Source:   strPtr("chisel-releases-lsp"),
				Message:  fmt.Sprintf("slice collision: path %q also claimed by %s", col.Path, col.SliceA),
			})
		}
	}

	// 4. Validate essential references exist in the index.
	for _, ref := range collectEssentialRefs(sf) {
		pkg, slice := parser.SliceRefFromToken(ref.Value)
		if pkg == "" {
			diags = append(diags, protocol.Diagnostic{
				Range:    toProtocolRange(ref.ValueRange),
				Severity: severityPtr(protocol.DiagnosticSeverityError),
				Source:   strPtr("chisel-releases-lsp"),
				Message:  fmt.Sprintf("invalid slice reference %q: expected <package>_<slice>", ref.Value),
			})
			continue
		}
		if s.idx.LookupSlice(pkg, slice) == nil {
			diags = append(diags, protocol.Diagnostic{
				Range:    toProtocolRange(ref.ValueRange),
				Severity: severityPtr(protocol.DiagnosticSeverityWarning),
				Source:   strPtr("chisel-releases-lsp"),
				Message:  fmt.Sprintf("unknown slice reference %q", ref.Value),
			})
		}
	}

	return diags
}

// collectEssentialRefs gathers all essential refs from a SliceFile (top-level + per-slice).
// Slices are visited in SliceOrder for deterministic results.
func collectEssentialRefs(sf *parser.SliceFile) []parser.EssentialRef {
	refs := append([]parser.EssentialRef{}, sf.Essential...)
	for _, name := range sf.SliceOrder {
		refs = append(refs, sf.Slices[name].Essential...)
	}
	return refs
}

