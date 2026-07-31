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
	if s.idx.IsReleaseFile(filePath) {
		return s.computeReleaseDiagnostics(filePath)
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
	for _, d := range analysis.CheckCopyrightEssential(filePath, sf, s.idx.PackageName(filePath)) {
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

	// 9. Prefer validation.
	for _, d := range analysis.ValidatePrefer(filePath, sf, s.idx) {
		diags = append(diags, protocol.Diagnostic{
			Range:    toProtocolRange(d.Range),
			Severity: severityPtr(protocol.DiagnosticSeverity(d.Severity)),
			Source:   strPtr("chisel-releases-lsp"),
			Code:     diagCodePtr(DiagCodeInvalidPrefer),
			Message:  d.Message,
		})
	}

	// 10. Glob-exact cross-package collision detection.
	for _, col := range analysis.DetectGlobCollisions(s.idx) {
		if col.GlobFile == filePath {
			diags = append(diags, protocol.Diagnostic{
				Range:    toProtocolRange(col.GlobRange),
				Severity: severityPtr(protocol.DiagnosticSeverityWarning),
				Source:   strPtr("chisel-releases-lsp"),
				Code:     diagCodePtr(DiagCodeGlobCollision),
				Message:  fmt.Sprintf("glob collision: pattern %q matches path %q also claimed by %s", col.GlobPath, col.ExactPath, col.ExactSlice),
				RelatedInformation: []protocol.DiagnosticRelatedInformation{{
					Location: protocol.Location{URI: filePathToURI(col.ExactFile), Range: toProtocolRange(col.ExactRange)},
					Message:  fmt.Sprintf("%s also claims %q", col.ExactSlice, col.ExactPath),
				}},
			})
		}
		if col.ExactFile == filePath {
			diags = append(diags, protocol.Diagnostic{
				Range:    toProtocolRange(col.ExactRange),
				Severity: severityPtr(protocol.DiagnosticSeverityWarning),
				Source:   strPtr("chisel-releases-lsp"),
				Code:     diagCodePtr(DiagCodeGlobCollision),
				Message:  fmt.Sprintf("glob collision: path %q is matched by pattern %q from %s", col.ExactPath, col.GlobPath, col.GlobSlice),
				RelatedInformation: []protocol.DiagnosticRelatedInformation{{
					Location: protocol.Location{URI: filePathToURI(col.GlobFile), Range: toProtocolRange(col.GlobRange)},
					Message:  fmt.Sprintf("%s has pattern %q matching this path", col.GlobSlice, col.GlobPath),
				}},
			})
		}
	}

	// 11. Redundant path check (exact path covered by glob in same slice).
	for _, d := range analysis.CheckRedundantPaths(filePath, sf) {
		diags = append(diags, protocol.Diagnostic{
			Range:    toProtocolRange(d.Range),
			Severity: severityPtr(protocol.DiagnosticSeverity(d.Severity)),
			Source:   strPtr("chisel-releases-lsp"),
			Code:     diagCodePtr(DiagCodeRedundantPath),
			Message:  d.Message,
		})
	}

	release := s.idx.Release()

	// 12. Store field validation (store:, default-track:, file placement).
	for _, d := range analysis.CheckStoreFields(filePath, sf, release) {
		diags = append(diags, protocol.Diagnostic{
			Range:    toProtocolRange(d.Range),
			Severity: severityPtr(protocol.DiagnosticSeverity(d.Severity)),
			Source:   strPtr("chisel-releases-lsp"),
			Code:     diagCodePtr(DiagCodeInvalidStore),
			Message:  d.Message,
		})
	}

	// 13. Channel pattern validation on contents: and essential: entries.
	for _, d := range analysis.CheckChannels(filePath, sf, release) {
		diags = append(diags, protocol.Diagnostic{
			Range:    toProtocolRange(d.Range),
			Severity: severityPtr(protocol.DiagnosticSeverity(d.Severity)),
			Source:   strPtr("chisel-releases-lsp"),
			Code:     diagCodePtr(DiagCodeInvalidChannel),
			Message:  d.Message,
		})
	}

	// Cache the line→code map so computeCodeActions can look up diagnostic codes
	// without re-running all checks.
	lineCode := make(map[uint32]string, len(diags))
	for _, d := range diags {
		if d.Code != nil {
			if c, ok := d.Code.Value.(string); ok {
				lineCode[d.Range.Start.Line] = c
			}
		}
	}
	s.diagCacheMu.Lock()
	s.diagLineCode[filePath] = lineCode
	s.diagCacheMu.Unlock()

	return diags
}

// computeReleaseDiagnostics returns the diagnostics for the release definition
// file (chisel.yaml): unknown format versions and malformed `stores:` entries.
func (s *Server) computeReleaseDiagnostics(filePath string) []protocol.Diagnostic {
	rel := s.idx.Release()
	if rel == nil {
		return nil
	}
	diags := []protocol.Diagnostic{}
	for _, d := range analysis.CheckRelease(filePath, rel) {
		diags = append(diags, protocol.Diagnostic{
			Range:    toProtocolRange(d.Range),
			Severity: severityPtr(protocol.DiagnosticSeverity(d.Severity)),
			Source:   strPtr("chisel-releases-lsp"),
			Code:     diagCodePtr(DiagCodeInvalidRelease),
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
