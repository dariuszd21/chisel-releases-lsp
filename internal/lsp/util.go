package lsp

import (
	"net/url"
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/dariuszd21/chisel-releases-lsp/internal/parser"
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

// filePathToURI converts an OS path to a file:// URI (RFC 3986-encoded).
func filePathToURI(p string) protocol.DocumentUri {
	if !strings.HasPrefix(p, "/") {
		return protocol.DocumentUri(p)
	}
	u := &url.URL{Scheme: "file", Path: p}
	return protocol.DocumentUri(u.String())
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

// Diagnostic code constants used to identify the kind of diagnostic and to
// select applicable code actions in textDocument/codeAction.
const (
	DiagCodePackageNameMismatch = "package-name-mismatch"
	DiagCodeInvalidGlob         = "invalid-glob"
	DiagCodeSliceCollision      = "slice-collision"
	DiagCodeInvalidSliceRef     = "invalid-slice-ref"
	DiagCodeUnknownSliceRef     = "unknown-slice-ref"
)

// diagCodePtr wraps a diagnostic code string into the protocol's IntegerOrString type.
func diagCodePtr(code string) *protocol.IntegerOrString {
	return &protocol.IntegerOrString{Value: code}
}

// Notifier abstracts the LSP notification channel so internal helpers can
// be tested without a live JSON-RPC connection.
type Notifier interface {
	Notify(method string, params any)
}

// ctxNotifier wraps a *glsp.Context to implement Notifier.
type ctxNotifier struct{ ctx *glsp.Context }

func (c ctxNotifier) Notify(m string, p any) {
	if c.ctx == nil {
		return // nil-safe: test exports call internal helpers with nil ctx
	}
	c.ctx.Notify(m, p)
}

// publishDiagnostics sends a textDocument/publishDiagnostics notification.
func publishDiagnostics(n Notifier, uri protocol.DocumentUri, diags []protocol.Diagnostic) {
	if n == nil {
		return
	}
	n.Notify(protocol.ServerTextDocumentPublishDiagnostics, protocol.PublishDiagnosticsParams{
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

// looksLikeEssentialMapEntry reports whether a trimmed line looks like a v3
// essential map entry: a key that contains "_" (the pkg_slice separator) before
// the first colon, which distinguishes it from section headers like "contents:".
//
// Handles both complete entries ("libc6_libs:") and partially-typed tokens
// ("libc6_" — no colon yet while the user is typing).
func looksLikeEssentialMapEntry(trimmed string) bool {
	colonIdx := strings.IndexByte(trimmed, ':')
	key := trimmed
	if colonIdx > 0 {
		key = trimmed[:colonIdx]
	}
	return strings.Contains(key, "_") && !strings.HasPrefix(key, "-")
}

// isInsideEssential reports whether the given line is inside an essential: list
// (v1/v2 sequence format) or map (v3 mapping format).
func isInsideEssential(text string, lineIdx int) bool {
	lines := strings.Split(text, "\n")
	if lineIdx >= len(lines) {
		return false
	}
	trimmed := strings.TrimSpace(lines[lineIdx])
	// Line must look like an essential entry: list item (v1/v2) or map key (v3).
	isListItem := strings.HasPrefix(trimmed, "- ") || trimmed == "-"
	isMapEntry := looksLikeEssentialMapEntry(trimmed)
	if !isListItem && !isMapEntry {
		return false
	}
	for i := lineIdx - 1; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			continue
		}
		// Continue scanning past other essential entries of either format.
		if strings.HasPrefix(t, "- ") {
			continue
		}
		if looksLikeEssentialMapEntry(t) {
			continue
		}
		return strings.HasPrefix(t, "essential:") || strings.HasPrefix(t, "v3-essential:")
	}
	return false
}

