package lsp

import (
	"net/url"
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/canonical/chisel-releases-lsp/internal/parser"
)

// uriToPath converts a file:// URI to an OS path.
func uriToPath(uri string) (string, error) {
	if !strings.HasPrefix(uri, "file://") {
		return uri, nil // treat as plain path
	}
	u, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	return u.Path, nil
}

// filePathToURI converts an OS path to a file:// URI.
func filePathToURI(p string) protocol.DocumentUri {
	if !strings.HasPrefix(p, "/") {
		return protocol.DocumentUri(p)
	}
	return protocol.DocumentUri("file://" + p)
}

// toProtocolRange converts a parser.Range to a protocol.Range.
func toProtocolRange(r parser.Range) protocol.Range {
	return protocol.Range{
		Start: protocol.Position{Line: uint32(r.Start.Line), Character: uint32(r.Start.Character)},
		End:   protocol.Position{Line: uint32(r.End.Line), Character: uint32(r.End.Character)},
	}
}

// strPtr returns a pointer to s.
func strPtr(s string) *string { return &s }

// severityPtr returns a pointer to s.
func severityPtr(s protocol.DiagnosticSeverity) *protocol.DiagnosticSeverity { return &s }

// publishDiagnostics sends a textDocument/publishDiagnostics notification.
func publishDiagnostics(ctx *glsp.Context, uri protocol.DocumentUri, diags []protocol.Diagnostic) {
	if ctx == nil {
		return
	}
	ctx.Notify(protocol.ServerTextDocumentPublishDiagnostics, protocol.PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: diags,
	})
}

// wordAtPosition returns the word token under the given position in text.
func wordAtPosition(text string, line, char int) string {
	lines := strings.Split(text, "\n")
	if line >= len(lines) {
		return ""
	}
	l := lines[line]
	if char > len(l) {
		char = len(l)
	}
	start := char
	for start > 0 && isWordChar(l[start-1]) {
		start--
	}
	end := char
	for end < len(l) && isWordChar(l[end]) {
		end++
	}
	return l[start:end]
}

func isWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') || b == '_' || b == '-' || b == '.'
}

// isInsideEssential reports whether the given line is inside an essential: list.
func isInsideEssential(text string, lineIdx int) bool {
	lines := strings.Split(text, "\n")
	if lineIdx >= len(lines) {
		return false
	}
	trimmed := strings.TrimSpace(lines[lineIdx])
	if !strings.HasPrefix(trimmed, "- ") && trimmed != "-" {
		return false
	}
	for i := lineIdx - 1; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "- ") {
			continue
		}
		return strings.HasPrefix(t, "essential:")
	}
	return false
}

