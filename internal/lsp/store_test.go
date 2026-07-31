package lsp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/dariuszd21/chisel-releases-lsp/internal/index"
	"github.com/dariuszd21/chisel-releases-lsp/internal/lsp"
)

const testChiselYamlV4 = `format: v4
stores:
  bin:
    kind: bin
    version: 26.10
    default-prefix: "bin-"
`

// setupStoreIndex builds a release tree whose file paths are relative to the
// release root (e.g. "chisel.yaml", "slices/curl.yaml", "bin-slices/curl.yaml")
// and returns the index plus the release root.
func setupStoreIndex(t *testing.T, files map[string]string) (*index.Index, string) {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		abs := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "slices"), 0o755); err != nil {
		t.Fatal(err)
	}
	idx, err := index.New(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	return idx, dir
}

// diagMessages joins diagnostic messages for substring assertions.
func diagMessages(diags []protocol.Diagnostic) string {
	msgs := make([]string, 0, len(diags))
	for _, d := range diags {
		msgs = append(msgs, d.Message)
	}
	return strings.Join(msgs, " | ")
}

// hasDiagCode reports whether any diagnostic carries the given code.
func hasDiagCode(diags []protocol.Diagnostic, code string) bool {
	for _, d := range diags {
		if d.Code == nil {
			continue
		}
		if c, ok := d.Code.Value.(string); ok && c == code {
			return true
		}
	}
	return false
}

func TestComputeDiagnostics_StoreFieldErrors(t *testing.T) {
	idx, root := setupStoreIndex(t, map[string]string{
		"chisel.yaml": testChiselYamlV4,
		"slices/curl.yaml": `package: curl
store: bin
slices:
  bins:
    contents:
      /usr/bin/curl:
`,
	})
	srv := lsp.NewWithIndex(idx)
	diags := srv.ExportComputeDiagnostics(filepath.Join(root, "slices", "curl.yaml"))
	if !hasDiagCode(diags, lsp.DiagCodeInvalidStore) {
		t.Fatalf("expected an invalid-store diagnostic, got %q", diagMessages(diags))
	}
	if !strings.Contains(diagMessages(diags), "'store' requires 'default-track'") {
		t.Errorf("got %q", diagMessages(diags))
	}
}

func TestComputeDiagnostics_ValidStorePackage(t *testing.T) {
	idx, root := setupStoreIndex(t, map[string]string{
		"chisel.yaml": testChiselYamlV4,
		"slices/curl.yaml": `package: curl
store: bin
default-track: "3.0"
slices:
  copyright:
    contents:
      /usr/share/doc/curl/copyright:
  bins:
    essential:
      bin-curl_copyright:
    contents:
      /usr/bin/curl: {channel: 3.0/edge}
`,
	})
	srv := lsp.NewWithIndex(idx)
	diags := srv.ExportComputeDiagnostics(filepath.Join(root, "slices", "curl.yaml"))
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %q", diagMessages(diags))
	}
}

func TestComputeDiagnostics_InvalidChannel(t *testing.T) {
	idx, root := setupStoreIndex(t, map[string]string{
		"chisel.yaml": testChiselYamlV4,
		"slices/curl.yaml": `package: curl
store: bin
default-track: "3.0"
slices:
  bins:
    contents:
      /usr/bin/curl: {channel: 3.0/nope}
`,
	})
	srv := lsp.NewWithIndex(idx)
	diags := srv.ExportComputeDiagnostics(filepath.Join(root, "slices", "curl.yaml"))
	if !hasDiagCode(diags, lsp.DiagCodeInvalidChannel) {
		t.Fatalf("expected an invalid-channel diagnostic, got %q", diagMessages(diags))
	}
	if !strings.Contains(diagMessages(diags), `unknown risk "nope"`) {
		t.Errorf("got %q", diagMessages(diags))
	}
}

// TestComputeDiagnostics_CopyrightUsesPrefixedName verifies that the missing
// copyright check expects the prefixed reference for store-backed packages.
func TestComputeDiagnostics_CopyrightUsesPrefixedName(t *testing.T) {
	idx, root := setupStoreIndex(t, map[string]string{
		"chisel.yaml": testChiselYamlV4,
		"slices/curl.yaml": `package: curl
store: bin
default-track: "3.0"
slices:
  bins:
    contents:
      /usr/bin/curl:
`,
	})
	srv := lsp.NewWithIndex(idx)
	diags := srv.ExportComputeDiagnostics(filepath.Join(root, "slices", "curl.yaml"))
	if !strings.Contains(diagMessages(diags), `"bin-curl_copyright"`) {
		t.Fatalf("expected the prefixed copyright ref, got %q", diagMessages(diags))
	}
}

// TestComputeDiagnostics_CollisionIgnoresChannel verifies that path collisions
// are reported irrespective of `channel:`, exactly as chisel treats `arch:`.
// Using the channel to partition paths would allow brittle combinations, so
// chisel is deliberately stricter and reports the conflict even when the two
// entries could never be cut together.
func TestComputeDiagnostics_CollisionIgnoresChannel(t *testing.T) {
	idx, root := setupStoreIndex(t, map[string]string{
		"chisel.yaml": testChiselYamlV4,
		"slices/curl.yaml": `package: curl
store: bin
default-track: "3.0"
slices:
  bins:
    contents:
      /usr/bin/tool: {channel: 3.0/stable}
`,
		"slices/wget.yaml": `package: wget
store: bin
default-track: "3.0"
slices:
  bins:
    contents:
      /usr/bin/tool: {channel: 3.0/edge}
`,
	})
	srv := lsp.NewWithIndex(idx)
	diags := srv.ExportComputeDiagnostics(filepath.Join(root, "slices", "curl.yaml"))
	if !hasDiagCode(diags, lsp.DiagCodeSliceCollision) {
		t.Fatalf("collision must be reported regardless of channel, got %q", diagMessages(diags))
	}
	// The collision must name the other package by its unique prefixed name.
	if !strings.Contains(diagMessages(diags), "bin-wget_bins") {
		t.Errorf("collision should name bin-wget_bins, got %q", diagMessages(diags))
	}
}

// TestComputeDiagnostics_RedundantPathIgnoresChannel verifies the same rule for
// the intra-slice redundancy check: a differing `channel:` does not make an
// exact path non-redundant against a glob covering it.
func TestComputeDiagnostics_RedundantPathIgnoresChannel(t *testing.T) {
	idx, root := setupStoreIndex(t, map[string]string{
		"chisel.yaml": testChiselYamlV4,
		"slices/curl.yaml": `package: curl
store: bin
default-track: "3.0"
slices:
  bins:
    contents:
      /usr/share/curl/**: {channel: 3.0/stable}
      /usr/share/curl/x:
`,
	})
	srv := lsp.NewWithIndex(idx)
	diags := srv.ExportComputeDiagnostics(filepath.Join(root, "slices", "curl.yaml"))
	if !hasDiagCode(diags, lsp.DiagCodeRedundantPath) {
		t.Fatalf("redundant path must be reported regardless of channel, got %q", diagMessages(diags))
	}
}

func TestComputeDiagnostics_ReleaseFile(t *testing.T) {
	idx, root := setupStoreIndex(t, map[string]string{
		"chisel.yaml": `format: v9
stores:
  bin:
    kind: snap
    version: 26.10
    default-prefix: "bin-"
`,
	})
	srv := lsp.NewWithIndex(idx)
	diags := srv.ExportComputeDiagnostics(filepath.Join(root, "chisel.yaml"))
	if !hasDiagCode(diags, lsp.DiagCodeInvalidRelease) {
		t.Fatalf("expected invalid-release diagnostics, got %q", diagMessages(diags))
	}
	msgs := diagMessages(diags)
	if !strings.Contains(msgs, `unknown format "v9"`) || !strings.Contains(msgs, `unknown kind "snap"`) {
		t.Errorf("got %q", msgs)
	}
}

func TestComputeDiagnostics_ReleaseFileV4Clean(t *testing.T) {
	idx, root := setupStoreIndex(t, map[string]string{"chisel.yaml": testChiselYamlV4})
	srv := lsp.NewWithIndex(idx)
	diags := srv.ExportComputeDiagnostics(filepath.Join(root, "chisel.yaml"))
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics for a valid v4 release, got %q", diagMessages(diags))
	}
}

// TestDefinition_StorePrefixedRef verifies that go-to-definition resolves a
// prefixed reference to the store package's file.
func TestDefinition_StorePrefixedRef(t *testing.T) {
	idx, root := setupStoreIndex(t, map[string]string{
		"chisel.yaml":      testChiselYamlV4,
		"slices/curl.yaml": curlStoreYaml,
		"slices/app.yaml": `package: app
slices:
  bins:
    essential:
      bin-curl_bins:
    contents:
      /usr/bin/app:
`,
	})
	srv := lsp.NewWithIndex(idx)
	appPath := filepath.Join(root, "slices", "app.yaml")
	srv.SetDocForTest(appPath, `package: app
slices:
  bins:
    essential:
      bin-curl_bins:
    contents:
      /usr/bin/app:
`)
	// Cursor inside "bin-curl_bins" on line 4.
	locs, err := srv.ExportDefinition(appPath, 4, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) != 1 {
		t.Fatalf("expected 1 definition location, got %d", len(locs))
	}
	want := lsp.ExportFilePathToURI(filepath.Join(root, "slices", "curl.yaml"))
	if locs[0].URI != want {
		t.Errorf("URI: got %q, want %q", locs[0].URI, want)
	}
}

const curlStoreYaml = `package: curl
store: bin
default-track: "3.0"
slices:
  bins:
    contents:
      /usr/bin/curl:
`

// TestCompletion_StorePrefixedRefs verifies that completions offer the unique
// prefixed name of a store package.
func TestCompletion_StorePrefixedRefs(t *testing.T) {
	idx, root := setupStoreIndex(t, map[string]string{
		"chisel.yaml":      testChiselYamlV4,
		"slices/curl.yaml": curlStoreYaml,
	})
	srv := lsp.NewWithIndex(idx)
	text := `package: app
slices:
  bins:
    essential:
      bin-
`
	items := srv.ExportCompletion(filepath.Join(root, "slices", "app.yaml"), text, 4, 10)
	var labels []string
	for _, it := range items {
		labels = append(labels, it.Label)
	}
	if !strings.Contains(strings.Join(labels, ","), "bin-curl_bins") {
		t.Errorf("bin-curl_bins not offered, got %v", labels)
	}
}

func TestChannelCompletion_RiskAfterSlash(t *testing.T) {
	idx, root := setupStoreIndex(t, map[string]string{
		"chisel.yaml":      testChiselYamlV4,
		"slices/curl.yaml": curlStoreYaml,
	})
	srv := lsp.NewWithIndex(idx)
	curlPath := filepath.Join(root, "slices", "curl.yaml")
	text := `package: curl
store: bin
default-track: "3.0"
slices:
  bins:
    contents:
      /usr/bin/curl: {channel: 3.0/`
	items := srv.ExportCompletion(curlPath, text, 6, len("      /usr/bin/curl: {channel: 3.0/"))
	labels := map[string]protocol.Range{}
	for _, it := range items {
		labels[it.Label] = it.TextEdit.(protocol.TextEdit).Range
	}
	for _, risk := range []string{"stable", "candidate", "beta", "edge"} {
		if _, ok := labels[risk]; !ok {
			t.Errorf("risk %q not offered, got %v", risk, labels)
		}
	}
	// The edit must replace only the risk, leaving "3.0/" untouched.
	if r, ok := labels["edge"]; ok {
		wantStart := uint32(len("      /usr/bin/curl: {channel: 3.0/"))
		if r.Start.Character != wantStart {
			t.Errorf("edit start: got %d, want %d", r.Start.Character, wantStart)
		}
	}
}

func TestChannelCompletion_FullPatternFromDefaultTrack(t *testing.T) {
	idx, root := setupStoreIndex(t, map[string]string{
		"chisel.yaml":      testChiselYamlV4,
		"slices/curl.yaml": curlStoreYaml,
	})
	srv := lsp.NewWithIndex(idx)
	curlPath := filepath.Join(root, "slices", "curl.yaml")
	text := `package: curl
store: bin
default-track: "3.0"
slices:
  bins:
    contents:
      /usr/bin/curl: {channel: `
	items := srv.ExportCompletion(curlPath, text, 6, len("      /usr/bin/curl: {channel: "))
	var labels []string
	for _, it := range items {
		labels = append(labels, it.Label)
	}
	joined := strings.Join(labels, ",")
	// Suggestions are built from the file's own default-track.
	if !strings.Contains(joined, "3.0/stable") || !strings.Contains(joined, "3.0/*") {
		t.Errorf("expected 3.0/* patterns, got %v", labels)
	}
}

// TestChannelCompletion_NotInChannelValue verifies that channel completion does
// not hijack positions outside a channel: value.
func TestChannelValueBeforeCursor(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		char  int
		value string
		ok    bool
	}{{
		name:  "inline attribute",
		text:  "      /usr/bin/curl: {channel: 3.0/ed",
		char:  len("      /usr/bin/curl: {channel: 3.0/ed"),
		value: "3.0/ed",
		ok:    true,
	}, {
		name:  "block attribute",
		text:  "        channel: 3.0",
		char:  len("        channel: 3.0"),
		value: "3.0",
		ok:    true,
	}, {
		name: "after the mapping is closed",
		text: "      /usr/bin/curl: {channel: 3.0/edge} ",
		char: len("      /usr/bin/curl: {channel: 3.0/edge} "),
		ok:   false,
	}, {
		name: "another attribute follows",
		text: "      /usr/bin/curl: {channel: 3.0/edge, mode: 0644",
		char: len("      /usr/bin/curl: {channel: 3.0/edge, mode: 0644"),
		ok:   false,
	}, {
		name: "not a channel line",
		text: "      /usr/bin/curl:",
		char: len("      /usr/bin/curl:"),
		ok:   false,
	}}

	for _, tc := range tests {
		value, _, ok := lsp.ExportChannelValueBeforeCursor(tc.text, 0, tc.char)
		if ok != tc.ok {
			t.Errorf("%s: ok got %v, want %v", tc.name, ok, tc.ok)
			continue
		}
		if ok && value != tc.value {
			t.Errorf("%s: value got %q, want %q", tc.name, value, tc.value)
		}
	}
}

func TestChannelRiskFixes(t *testing.T) {
	lines := []string{"      /usr/bin/curl: {channel: 3.0/nope}"}
	start := uint32(len("      /usr/bin/curl: {channel: "))
	r := protocol.Range{
		Start: protocol.Position{Line: 0, Character: start},
		End:   protocol.Position{Line: 0, Character: start + uint32(len("3.0/nope"))},
	}
	fixes := lsp.ExportChannelRiskFixes(lines, r)
	if len(fixes) != 4 {
		t.Fatalf("expected 4 fixes, got %v", fixes)
	}
	// The track must be preserved and only the risk replaced.
	if fixes[0] != "3.0/stable" {
		t.Errorf("first fix: got %q, want %q", fixes[0], "3.0/stable")
	}

	// A pattern without a track offers nothing useful to keep.
	noTrack := []string{"      /usr/bin/curl: {channel: nope}"}
	nStart := uint32(len("      /usr/bin/curl: {channel: "))
	if got := lsp.ExportChannelRiskFixes(noTrack, protocol.Range{
		Start: protocol.Position{Line: 0, Character: nStart},
		End:   protocol.Position{Line: 0, Character: nStart + uint32(len("nope"))},
	}); got != nil {
		t.Errorf("expected no fixes without a track, got %v", got)
	}
}

func TestCodeAction_InvalidChannel(t *testing.T) {
	content := `package: curl
store: bin
default-track: "3.0"
slices:
  bins:
    contents:
      /usr/bin/curl: {channel: 3.0/nope}
`
	idx, root := setupStoreIndex(t, map[string]string{
		"chisel.yaml":      testChiselYamlV4,
		"slices/curl.yaml": content,
	})
	srv := lsp.NewWithIndex(idx)
	curlPath := filepath.Join(root, "slices", "curl.yaml")
	srv.SetDocForTest(curlPath, content)

	diags := srv.ExportComputeDiagnostics(curlPath)
	var chDiag protocol.Diagnostic
	for _, d := range diags {
		if d.Code != nil {
			if c, ok := d.Code.Value.(string); ok && c == lsp.DiagCodeInvalidChannel {
				chDiag = d
			}
		}
	}
	if chDiag.Message == "" {
		t.Fatalf("no invalid-channel diagnostic, got %q", diagMessages(diags))
	}

	actions := srv.ExportCodeAction(curlPath, []protocol.Diagnostic{chDiag})
	if len(actions) == 0 {
		t.Fatal("expected channel quick fixes, got none")
	}
	found := false
	for _, a := range actions {
		if strings.Contains(a.Title, `"3.0/edge"`) {
			found = true
			edits := a.Edit.Changes[lsp.ExportFilePathToURI(curlPath)]
			if len(edits) != 1 || edits[0].NewText != "3.0/edge" {
				t.Errorf("unexpected edit: %+v", edits)
			}
		}
	}
	if !found {
		t.Errorf("no fix offering 3.0/edge, got %d actions", len(actions))
	}
}

// TestReloadRelease_UpdatesFormatGating verifies that editing chisel.yaml is
// picked up: raising the format from v2 to v4 clears the "unsupported before
// format v3" diagnostics on a store-backed slice file.
func TestReloadRelease_UpdatesFormatGating(t *testing.T) {
	sliceContent := `package: curl
store: bin
default-track: "3.0"
slices:
  bins:
    contents:
      /usr/bin/curl:
`
	idx, root := setupStoreIndex(t, map[string]string{
		"chisel.yaml":      "format: v2\n",
		"slices/curl.yaml": sliceContent,
	})
	srv := lsp.NewWithIndex(idx)
	curlPath := filepath.Join(root, "slices", "curl.yaml")

	diags := srv.ExportComputeDiagnostics(curlPath)
	if !strings.Contains(diagMessages(diags), "unsupported before format v3") {
		t.Fatalf("expected a format-gate diagnostic at v2, got %q", diagMessages(diags))
	}

	if err := idx.UpdateRelease([]byte(testChiselYamlV4)); err != nil {
		t.Fatal(err)
	}
	diags = srv.ExportComputeDiagnostics(curlPath)
	if strings.Contains(diagMessages(diags), "unsupported before format v3") {
		t.Errorf("format gate should be lifted at v4, got %q", diagMessages(diags))
	}
}

// TestHover_StorePackage verifies that hover surfaces the store and the channel
// constraints, which decide what is actually installed.
func TestHover_StorePackage(t *testing.T) {
	idx, root := setupStoreIndex(t, map[string]string{
		"chisel.yaml": testChiselYamlV4,
		"slices/curl.yaml": `package: curl
store: bin
default-track: "3.0"
slices:
  bins:
    contents:
      /usr/bin/curl: {channel: 3.0/edge}
`,
		"slices/app.yaml": `package: app
slices:
  bins:
    essential:
      bin-curl_bins:
    contents:
      /usr/bin/app:
`,
	})
	srv := lsp.NewWithIndex(idx)
	appPath := filepath.Join(root, "slices", "app.yaml")
	text := `package: app
slices:
  bins:
    essential:
      bin-curl_bins:
    contents:
      /usr/bin/app:
`
	srv.SetDocForTest(appPath, text)
	hover, err := srv.ExportHover(appPath, 4, 12)
	if err != nil {
		t.Fatal(err)
	}
	if hover == nil {
		t.Fatal("expected hover content, got nil")
	}
	md := hover.Contents.(protocol.MarkupContent).Value
	if !strings.Contains(md, "**Store:** `bin`") {
		t.Errorf("hover should mention the store: %q", md)
	}
	if !strings.Contains(md, "default track `3.0`") {
		t.Errorf("hover should mention the default track: %q", md)
	}
	if !strings.Contains(md, "3.0/edge") {
		t.Errorf("hover should mention the channel: %q", md)
	}
}

// TestDocumentSymbol_StorePackage verifies the outline shows the unique
// prefixed package name, which is what references use.
func TestDocumentSymbol_StorePackage(t *testing.T) {
	idx, root := setupStoreIndex(t, map[string]string{
		"chisel.yaml":      testChiselYamlV4,
		"slices/curl.yaml": curlStoreYaml,
	})
	srv := lsp.NewWithIndex(idx)
	syms, err := srv.ExportDocumentSymbol(filepath.Join(root, "slices", "curl.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(syms) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(syms))
	}
	if syms[0].Name != "bin-curl" {
		t.Errorf("symbol name: got %q, want %q", syms[0].Name, "bin-curl")
	}
	if syms[0].Detail == nil || !strings.Contains(*syms[0].Detail, "store bin") {
		t.Errorf("symbol detail should mention the store: %v", syms[0].Detail)
	}
}
