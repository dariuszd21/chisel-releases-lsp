package lsp

import (
	"fmt"
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/canonical/chisel-releases-lsp/internal/analysis"
	"github.com/canonical/chisel-releases-lsp/internal/parser"
)

// publishDiagnosticsForFile computes and sends diagnostics for a single file.
func (s *Server) publishDiagnosticsForFile(ctx *glsp.Context, filePath string) {
	if s.idx == nil {
		return
	}
	sf := s.idx.FileSliceFile(filePath)
	if sf == nil {
		return
	}

	var diags []protocol.Diagnostic

	// 1. Glob validation
	for _, d := range analysis.ValidateGlobs(filePath, sf) {
		diags = append(diags, protocol.Diagnostic{
			Range:    toProtocolRange(d.Range),
			Severity: severityPtr(protocol.DiagnosticSeverity(d.Severity)),
			Source:   strPtr("chisel-releases-lsp"),
			Message:  d.Message,
		})
	}

	// 2. Slice collision detection (cross-file; report only entries in this file)
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

	// 3. Validate essential references exist in the index.
	for _, ref := range collectEssentialRefs(sf) {
		pkg, slice := splitSliceRef(ref.Value)
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

	publishDiagnostics(ctx, filePathToURI(filePath), diags)
}

// collectEssentialRefs gathers all essential refs from a SliceFile (top-level + per-slice).
func collectEssentialRefs(sf *parser.SliceFile) []parser.EssentialRef {
	refs := append([]parser.EssentialRef{}, sf.Essential...)
	for _, sd := range sf.Slices {
		refs = append(refs, sd.Essential...)
	}
	return refs
}

func splitSliceRef(s string) (pkg, slice string) {
	idx := strings.LastIndex(s, "_")
	if idx <= 0 || idx == len(s)-1 {
		return "", ""
	}
	return s[:idx], s[idx+1:]
}

