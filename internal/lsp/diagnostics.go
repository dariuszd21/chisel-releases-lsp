package lsp

import (
	"fmt"
	"path/filepath"

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
			Code:     diagCodePtr(DiagCodePackageNameMismatch),
			Message:  d.Message,
		})
	}

	// 2. Glob validation
	for _, d := range analysis.ValidateGlobs(filePath, sf) {
		diags = append(diags, protocol.Diagnostic{
			Range:    toProtocolRange(d.Range),
			Severity: severityPtr(protocol.DiagnosticSeverity(d.Severity)),
			Source:   strPtr("chisel-releases-lsp"),
			Code:     diagCodePtr(DiagCodeInvalidGlob),
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
				Code:     diagCodePtr(DiagCodeSliceCollision),
				Message:  fmt.Sprintf("slice collision: path %q also claimed by %s", col.Path, col.SliceB),
				RelatedInformation: []protocol.DiagnosticRelatedInformation{
					{
						Location: protocol.Location{
							URI:   filePathToURI(col.FileB),
							Range: toProtocolRange(col.RangeB),
						},
						Message: fmt.Sprintf("%s also claims %q", col.SliceB, col.Path),
					},
				},
			})
		}
		if col.FileB == filePath {
			diags = append(diags, protocol.Diagnostic{
				Range:    toProtocolRange(col.RangeB),
				Severity: severityPtr(protocol.DiagnosticSeverityWarning),
				Source:   strPtr("chisel-releases-lsp"),
				Code:     diagCodePtr(DiagCodeSliceCollision),
				Message:  fmt.Sprintf("slice collision: path %q also claimed by %s", col.Path, col.SliceA),
				RelatedInformation: []protocol.DiagnosticRelatedInformation{
					{
						Location: protocol.Location{
							URI:   filePathToURI(col.FileA),
							Range: toProtocolRange(col.RangeA),
						},
						Message: fmt.Sprintf("%s also claims %q", col.SliceA, col.Path),
					},
				},
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
				Code:     diagCodePtr(DiagCodeInvalidSliceRef),
				Message:  fmt.Sprintf("invalid slice reference %q: expected <package>_<slice>", ref.Value),
			})
			continue
		}
		if s.idx.LookupSlice(pkg, slice) == nil {
			diags = append(diags, protocol.Diagnostic{
				Range:    toProtocolRange(ref.ValueRange),
				Severity: severityPtr(protocol.DiagnosticSeverityWarning),
				Source:   strPtr("chisel-releases-lsp"),
				Code:     diagCodePtr(DiagCodeUnknownSliceRef),
				Message:  fmt.Sprintf("unknown slice reference %q", ref.Value),
			})
		}
	}

	// 5. Duplicate slice definitions: same pkg+name declared in two files.
	for _, dup := range analysis.DetectDuplicateSlices(s.idx) {
		if dup.File1 == filePath {
			diags = append(diags, protocol.Diagnostic{
				Range:    toProtocolRange(dup.Range1),
				Severity: severityPtr(protocol.DiagnosticSeverityWarning),
				Source:   strPtr("chisel-releases-lsp"),
				Code:     diagCodePtr(DiagCodeDuplicateSlice),
				Message: fmt.Sprintf("slice %q_%q is also defined in %s",
					dup.Pkg, dup.SliceName, filepath.Base(dup.File2)),
			})
		}
		if dup.File2 == filePath {
			diags = append(diags, protocol.Diagnostic{
				Range:    toProtocolRange(dup.Range2),
				Severity: severityPtr(protocol.DiagnosticSeverityWarning),
				Source:   strPtr("chisel-releases-lsp"),
				Code:     diagCodePtr(DiagCodeDuplicateSlice),
				Message: fmt.Sprintf("slice %q_%q is also defined in %s",
					dup.Pkg, dup.SliceName, filepath.Base(dup.File1)),
			})
		}
	}

	// 6. Missing copyright essential in slice definitions.
	for _, d := range analysis.CheckCopyrightEssential(filePath, sf) {
		diags = append(diags, protocol.Diagnostic{
			Range:    toProtocolRange(d.Range),
			Severity: severityPtr(protocol.DiagnosticSeverity(d.Severity)),
			Source:   strPtr("chisel-releases-lsp"),
			Code:     diagCodePtr(DiagCodeMissingCopyright),
			Message:  d.Message,
		})
	}

	// 7. Duplicate essential references within the same block.
	for _, d := range analysis.CheckDuplicateEssentials(filePath, sf) {
		diags = append(diags, protocol.Diagnostic{
			Range:    toProtocolRange(d.DupRef.ValueRange),
			Severity: severityPtr(protocol.DiagnosticSeverityWarning),
			Source:   strPtr("chisel-releases-lsp"),
			Code:     diagCodePtr(DiagCodeDuplicateEssential),
			Message:  fmt.Sprintf("duplicate essential reference %q", d.DupRef.Value),
			RelatedInformation: []protocol.DiagnosticRelatedInformation{
				{
					Location: protocol.Location{
						URI:   filePathToURI(filePath),
						Range: toProtocolRange(d.FirstRef.ValueRange),
					},
					Message: fmt.Sprintf("first occurrence of %q", d.FirstRef.Value),
				},
			},
		})
	}

	// 8. Lexical sort check.
	for _, d := range analysis.CheckLexicalOrder(filePath, sf) {
		diags = append(diags, protocol.Diagnostic{
			Range:    toProtocolRange(d.Range),
			Severity: severityPtr(protocol.DiagnosticSeverity(d.Severity)),
			Source:   strPtr("chisel-releases-lsp"),
			Code:     diagCodePtr(DiagCodeOutOfOrder),
			Message:  d.Message,
		})
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
