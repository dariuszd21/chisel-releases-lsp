package lsp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dariuszd21/chisel-releases-lsp/internal/index"
	"github.com/dariuszd21/chisel-releases-lsp/internal/lsp"
	"github.com/dariuszd21/chisel-releases-lsp/internal/parser"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// We test the LSP utility helpers directly since the full server requires stdio.

const (
	textWithEssential = `package: foo
slices:
  bins:
    essential:
      - libc6_libs
      - openssl_config
    contents:
      /usr/bin/foo:
`
	textNoEssential = `package: foo
slices:
  bins:
    contents:
      /usr/bin/foo:
`
	textTopLevelEssential = `package: foo
essential:
  - libc6_libs
slices:
  bins:
    contents:
      /usr/bin/foo:
`
)

func TestWordAtPosition(t *testing.T) {
	cases := []struct {
		text string
		line int
		char int
		want string
	}{
		{"  - libc6_libs\n", 0, 6, "libc6_libs"},
		{"  - libc6_libs\n", 0, 4, "libc6_libs"},
		{"  - libc6_libs\n", 0, 14, "libc6_libs"},
		{"  - \n", 0, 4, ""},
	}
	for _, c := range cases {
		got := lsp.ExportWordAtPosition(c.text, c.line, c.char)
		if got != c.want {
			t.Errorf("wordAtPosition(%q, %d, %d) = %q, want %q", c.text, c.line, c.char, got, c.want)
		}
	}
}

func TestIsInsideEssential(t *testing.T) {
	cases := []struct {
		text string
		line int
		want bool
	}{
		{textWithEssential, 4, true},  // "      - libc6_libs"
		{textWithEssential, 5, true},  // "      - openssl_config"
		{textWithEssential, 7, false}, // "    contents:"
		{textNoEssential, 4, false},   // contents path
	}
	for _, c := range cases {
		got := lsp.ExportIsInsideEssential(c.text, c.line)
		if got != c.want {
			t.Errorf("isInsideEssential(line=%d) = %v, want %v", c.line, got, c.want)
		}
	}
}

func TestIsInsideEssential_TopLevel(t *testing.T) {
	// Line 2 is "  - libc6_libs" under the top-level essential: block.
	got := lsp.ExportIsInsideEssential(textTopLevelEssential, 2)
	if !got {
		t.Error("expected isInsideEssential=true for top-level essential ref, got false")
	}
}

// --- textDocument/completion ---

// completionText is the document content used by completion tests.
// Line offsets (0-based):
//   0: package: foo
//   1: slices:
//   2:   bins:
//   3:     essential:
//   4:       - libc6_libs       ← existing entry
//   5:     contents:
//   6:       /usr/bin/foo:
const completionText = `package: foo
slices:
  bins:
    essential:
      - libc6_libs
    contents:
      /usr/bin/foo:
`

func TestCompletionPrefixAndRange_WithPrefix(t *testing.T) {
	// Cursor at "libc6" (partial) on line 4, char 13 — after "      - libc6"
	prefix, r, needsSpace := lsp.ExportCompletionPrefixAndRange(completionText, 4, 13)
	if prefix != "libc6" {
		t.Errorf("prefix: got %q, want %q", prefix, "libc6")
	}
	// Range start must be right after "- " (col 8), end = col 13
	if r.Start.Character != 8 {
		t.Errorf("range start char: got %d, want 8", r.Start.Character)
	}
	if r.End.Character != 18 { // word "libc6_libs" ends at col 18
		t.Errorf("range end char: got %d, want 18 (end of word)", r.End.Character)
	}
	if needsSpace {
		t.Error("needsLeadingSpace should be false when '- ' marker present")
	}
}

func TestCompletionPrefixAndRange_TriggerOnly(t *testing.T) {
	// Cursor right after "-" on a fresh line: "      -" (col 7), no space yet.
	text := "package: foo\nslices:\n  bins:\n    essential:\n      -\n    contents:\n      /usr/bin/foo:\n"
	prefix, r, needsSpace := lsp.ExportCompletionPrefixAndRange(text, 4, 7)
	if prefix != "" {
		t.Errorf("prefix: got %q, want empty", prefix)
	}
	// Start = col 7 (right after "-"), End = col 7 (zero-width insert)
	if r.Start.Character != 7 || r.End.Character != 7 {
		t.Errorf("range: got {%d,%d}, want {7,7}", r.Start.Character, r.End.Character)
	}
	// Trigger-only: inserting the ref without a space would give "      -ref" — caller
	// must prepend " ".
	if !needsSpace {
		t.Error("needsLeadingSpace should be true in trigger-only mode")
	}
}

func TestCompletionPrefixAndRange_AfterSpace(t *testing.T) {
	// Cursor at "      - " (col 8, right after the space), nothing typed yet.
	text := "package: foo\nslices:\n  bins:\n    essential:\n      - \n    contents:\n      /usr/bin/foo:\n"
	prefix, r, needsSpace := lsp.ExportCompletionPrefixAndRange(text, 4, 8)
	if prefix != "" {
		t.Errorf("prefix: got %q, want empty", prefix)
	}
	if r.Start.Character != 8 {
		t.Errorf("range start char: got %d, want 8", r.Start.Character)
	}
	if needsSpace {
		t.Error("needsLeadingSpace should be false after the space is typed")
	}
}

func TestCompletion_ReturnsItems(t *testing.T) {
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"libc6.yaml": `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`,
		"openssl.yaml": `package: openssl
slices:
  bins:
    contents:
      /usr/bin/openssl:
`,
	})
	fooPath := filepath.Join(slicesDir, "foo.yaml")
	srv := lsp.NewWithIndex(idx)

	// Line 4, col 13: cursor mid-word "      - libc6" → prefix = "libc6"
	items := srv.ExportCompletion(fooPath, completionText, 4, 13)
	if len(items) == 0 {
		t.Fatal("expected completion items, got none")
	}

	labels := make(map[string]bool)
	for _, it := range items {
		labels[it.Label] = true
	}
	if !labels["libc6_libs"] {
		t.Errorf("expected libc6_libs in completions; got: %v", items)
	}
	if labels["openssl_bins"] {
		t.Errorf("openssl_bins should be filtered out by prefix 'libc6'; got: %v", items)
	}
}

func TestCompletion_AllItemsWhenNoPrefix(t *testing.T) {
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"libc6.yaml": `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`,
		"openssl.yaml": `package: openssl
slices:
  bins:
    contents:
      /usr/bin/openssl:
`,
	})
	fooPath := filepath.Join(slicesDir, "foo.yaml")
	srv := lsp.NewWithIndex(idx)

	// Cursor at "      - " (after the space, col 8) — no prefix, all items offered.
	text := "package: foo\nslices:\n  bins:\n    essential:\n      - \n    contents:\n      /usr/bin/foo:\n"
	items := srv.ExportCompletion(fooPath, text, 4, 8)
	if len(items) < 2 {
		t.Fatalf("expected at least 2 items with empty prefix, got %d: %v", len(items), items)
	}
}

func TestCompletion_NilOutsideEssential(t *testing.T) {
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"libc6.yaml": `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`,
	})
	fooPath := filepath.Join(slicesDir, "foo.yaml")
	srv := lsp.NewWithIndex(idx)

	// Line 5 (contents:) is not inside essential.
	items := srv.ExportCompletion(fooPath, completionText, 5, 5)
	if items != nil {
		t.Errorf("expected nil outside essential block, got %v", items)
	}
}

func TestCompletion_TextEditReplacesOnlyValue(t *testing.T) {
	// Verify that each completion item carries a TextEdit whose range starts
	// AFTER the "- " marker so the dash is never clobbered by the editor.
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"libc6.yaml": `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`,
	})
	fooPath := filepath.Join(slicesDir, "foo.yaml")
	srv := lsp.NewWithIndex(idx)

	// Cursor at col 13 (within "libc6_libs"), line 4.
	items := srv.ExportCompletion(fooPath, completionText, 4, 13)
	if len(items) == 0 {
		t.Fatal("no completion items")
	}
	for _, it := range items {
		te, ok := it.TextEdit.(protocol.TextEdit)
		if !ok {
			t.Errorf("item %q: TextEdit is not a protocol.TextEdit (got %T)", it.Label, it.TextEdit)
			continue
		}
		// Range must start at or after column 8 (right after "- ").
		if te.Range.Start.Character < 8 {
			t.Errorf("item %q: TextEdit range start col %d < 8 (would clobber '- ')",
				it.Label, te.Range.Start.Character)
		}
		if te.NewText != it.Label {
			t.Errorf("item %q: TextEdit NewText %q != Label", it.Label, te.NewText)
		}
	}
}

func TestCompletion_TriggerOnly_TextEditHasLeadingSpace(t *testing.T) {
	// When completion fires on bare "-" (no space typed yet), the TextEdit
	// NewText must begin with " " so the result is "- ref" not "-ref".
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"libc6.yaml": `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`,
	})
	fooPath := filepath.Join(slicesDir, "foo.yaml")
	srv := lsp.NewWithIndex(idx)

	// Line 4 is "      -" — trigger fired right after the dash (col 7).
	triggerText := "package: foo\nslices:\n  bins:\n    essential:\n      -\n    contents:\n      /usr/bin/foo:\n"
	items := srv.ExportCompletion(fooPath, triggerText, 4, 7)
	if len(items) == 0 {
		t.Fatal("expected completion items in trigger-only mode, got none")
	}
	for _, it := range items {
		te, ok := it.TextEdit.(protocol.TextEdit)
		if !ok {
			t.Errorf("item %q: TextEdit is not a protocol.TextEdit (got %T)", it.Label, it.TextEdit)
			continue
		}
		if !strings.HasPrefix(te.NewText, " ") {
			t.Errorf("item %q: NewText %q should start with ' ' to produce valid YAML",
				it.Label, te.NewText)
		}
		// Label should NOT have the leading space (it's just the slice ref name).
		if strings.HasPrefix(it.Label, " ") {
			t.Errorf("item %q: Label should not have a leading space", it.Label)
		}
	}
}

func TestCompletion_TopLevelEssential(t *testing.T) {
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"libc6.yaml": `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`,
	})
	fooPath := filepath.Join(slicesDir, "foo.yaml")
	srv := lsp.NewWithIndex(idx)

	// Top-level essential: line 2 is "  - libc6_libs"
	text := "package: foo\nessential:\n  - libc6\nslices:\n  bins:\n    contents:\n      /usr/bin/foo:\n"
	// cursor at col 9 (within "libc6")
	items := srv.ExportCompletion(fooPath, text, 2, 9)
	if len(items) == 0 {
		t.Fatal("expected items for top-level essential, got none")
	}
	found := false
	for _, it := range items {
		if it.Label == "libc6_libs" {
			found = true
		}
	}
	if !found {
		t.Errorf("libc6_libs not in top-level essential completions: %v", items)
	}
}

func TestRenderSliceMarkdown(t *testing.T) {
	dir := t.TempDir()
	slicesDir := filepath.Join(dir, "slices")
	if err := os.Mkdir(slicesDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slicesDir, "libc6.yaml"), []byte(`
package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`), 0644); err != nil {
		t.Fatal(err)
	}

	idx, err := index.New(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	is := idx.LookupSlice("libc6", "libs")
	if is == nil {
		t.Fatal("libc6_libs not indexed")
	}

	md := lsp.ExportRenderSliceMarkdown("libc6", "libs", is.Def)
	if md == "" {
		t.Error("empty markdown")
	}
	if !strings.Contains(md, "libc6_libs") {
		t.Errorf("markdown missing slice name: %q", md)
	}
	if !strings.Contains(md, "/lib/x86_64-linux-gnu/libc.so.6") {
		t.Errorf("markdown missing content path: %q", md)
	}
}

func TestURIToPath(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"file:///usr/share/foo/bar.yaml", "/usr/share/foo/bar.yaml"},
		{"/plain/path.yaml", "/plain/path.yaml"},
	}
	for _, c := range cases {
		got, err := lsp.ExportURIToPath(c.input)
		if err != nil {
			t.Errorf("uriToPath(%q) error: %v", c.input, err)
			continue
		}
		if got != c.want {
			t.Errorf("uriToPath(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestFilePathToURI(t *testing.T) {
	got := lsp.ExportFilePathToURI("/usr/share/foo/bar.yaml")
	want := protocol.DocumentUri("file:///usr/share/foo/bar.yaml")
	if got != want {
		t.Errorf("filePathToURI got %q, want %q", got, want)
	}
	// Relative paths pass through unchanged.
	rel := lsp.ExportFilePathToURI("relative/path.yaml")
	if strings.HasPrefix(string(rel), "file://") {
		t.Errorf("relative path should not get file:// prefix, got %q", rel)
	}
	// Paths with spaces must be percent-encoded.
	got2 := lsp.ExportFilePathToURI("/usr/share/my project/foo.yaml")
	if !strings.Contains(string(got2), "%20") {
		t.Errorf("space not percent-encoded in URI: %q", got2)
	}
}

func TestToProtocolRange(t *testing.T) {
	r := parser.Range{
		Start: parser.Position{Line: 3, Character: 5},
		End:   parser.Position{Line: 3, Character: 12},
	}
	got := lsp.ExportToProtocolRange(r)
	if got.Start.Line != 3 || got.Start.Character != 5 {
		t.Errorf("Start: got {%d,%d}, want {3,5}", got.Start.Line, got.Start.Character)
	}
	if got.End.Line != 3 || got.End.Character != 12 {
		t.Errorf("End: got {%d,%d}, want {3,12}", got.End.Line, got.End.Character)
	}
}

func TestCollectEssentialRefs(t *testing.T) {
	yaml := `package: foo
essential:
  - libc6_libs
slices:
  bins:
    essential:
      - openssl_config
    contents:
      /usr/bin/foo:
`
	sf, err := parser.ParseBytes([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	refs := lsp.ExportCollectEssentialRefs(sf)
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d: %v", len(refs), refs)
	}
	values := map[string]bool{}
	for _, r := range refs {
		values[r.Value] = true
	}
	if !values["libc6_libs"] {
		t.Error("missing top-level essential ref libc6_libs")
	}
	if !values["openssl_config"] {
		t.Error("missing per-slice essential ref openssl_config")
	}
}

func setupLSPIndex(t *testing.T, files map[string]string) (*index.Index, string) {
	t.Helper()
	dir := t.TempDir()
	slicesDir := filepath.Join(dir, "slices")
	if err := os.Mkdir(slicesDir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(slicesDir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	idx, err := index.New(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	return idx, slicesDir
}

func TestComputeDiagnostics_GlobError(t *testing.T) {
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"libc6.yaml": `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`,
		"bad.yaml": `package: bad
slices:
  s:
    contents:
      relative/path:
`,
	})

	srv := lsp.NewWithIndex(idx)
	badPath := filepath.Join(slicesDir, "bad.yaml")
	diags := srv.ExportComputeDiagnostics(badPath)

	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, "absolute") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'absolute' diagnostic, got: %v", diags)
	}
}

func TestComputeDiagnostics_UnknownRef(t *testing.T) {
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"foo.yaml": `package: foo
slices:
  bins:
    essential:
      - nonexistent_slice
    contents:
      /usr/bin/foo:
`,
	})

	srv := lsp.NewWithIndex(idx)
	fooPath := filepath.Join(slicesDir, "foo.yaml")
	diags := srv.ExportComputeDiagnostics(fooPath)

	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, "unknown slice reference") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected unknown-ref diagnostic, got: %v", diags)
	}
}

func TestComputeDiagnostics_CleanFileReturnsEmpty(t *testing.T) {
	// A valid file with no issues must return [] (not nil) so that the client
	// clears any previously shown squiggles.
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"libc6.yaml": `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`,
	})

	srv := lsp.NewWithIndex(idx)
	libc6Path := filepath.Join(slicesDir, "libc6.yaml")
	diags := srv.ExportComputeDiagnostics(libc6Path)

	if diags == nil {
		t.Error("expected non-nil (empty) diagnostic slice for clean file, got nil")
	}
	if len(diags) != 0 {
		t.Errorf("expected zero diagnostics for clean file, got: %v", diags)
	}
}

func TestSliceDetail(t *testing.T) {
	yaml := `package: foo
slices:
  bins:
    essential:
      - libc6_libs
    contents:
      /usr/bin/foo:
      /usr/bin/bar:
`
	sf, err := parser.ParseBytes([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	detail := lsp.ExportSliceDetail(sf.Slices["bins"])
	if !strings.Contains(detail, "2 contents") {
		t.Errorf("expected '2 contents' in detail, got %q", detail)
	}
	if !strings.Contains(detail, "1 essential") {
		t.Errorf("expected '1 essential' in detail, got %q", detail)
	}
}

func TestDocumentSymbol(t *testing.T) {
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"openssl.yaml": `package: openssl
slices:
  bins:
    contents:
      /usr/bin/openssl:
  config:
    contents:
      /etc/ssl/openssl.cnf:
`,
	})
	srv := lsp.NewWithIndex(idx)
	syms, err := srv.ExportDocumentSymbol(filepath.Join(slicesDir, "openssl.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(syms) != 1 {
		t.Fatalf("expected 1 top-level symbol, got %d", len(syms))
	}
	pkg := syms[0]
	if pkg.Name != "openssl" {
		t.Errorf("package symbol name: got %q, want %q", pkg.Name, "openssl")
	}
	if pkg.Kind != 2 { // SymbolKindModule = 2
		t.Errorf("package symbol kind: got %d, want Module(2)", pkg.Kind)
	}
	if len(pkg.Children) != 2 {
		t.Fatalf("expected 2 slice children, got %d", len(pkg.Children))
	}
	// Children must be in file order: bins, config
	if pkg.Children[0].Name != "bins" || pkg.Children[1].Name != "config" {
		t.Errorf("slice children order: got [%q, %q]", pkg.Children[0].Name, pkg.Children[1].Name)
	}
	// Slices should use SymbolKindKey (20), not Function.
	for _, child := range pkg.Children {
		if child.Kind != 20 { // SymbolKindKey = 20
			t.Errorf("slice %q kind: got %d, want Key(20)", child.Name, child.Kind)
		}
	}
}

func TestWorkspaceSymbol(t *testing.T) {
	idx, _ := setupLSPIndex(t, map[string]string{
		"openssl.yaml": `package: openssl
slices:
  bins:
    contents:
      /usr/bin/openssl:
`,
		"libc6.yaml": `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`,
	})
	srv := lsp.NewWithIndex(idx)

	// Empty query returns all symbols.
	all, err := srv.ExportWorkspaceSymbol("")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Errorf("empty query: expected 2 symbols, got %d: %v", len(all), all)
	}

	// Filtered query.
	filtered, err := srv.ExportWorkspaceSymbol("ssl")
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 {
		t.Errorf("'ssl' query: expected 1 symbol, got %d: %v", len(filtered), filtered)
	}
	if filtered[0].Name != "openssl_bins" {
		t.Errorf("filtered symbol name: got %q", filtered[0].Name)
	}
	// Workspace symbols should use SymbolKindKey (20), not Function.
	for _, s := range all {
		if s.Kind != 20 { // SymbolKindKey = 20
			t.Errorf("workspace symbol %q kind: got %d, want Key(20)", s.Name, s.Kind)
		}
	}
}

func TestDidClose_RevertsIndexToDisk(t *testing.T) {
// Set up a real on-disk release with one package.
dir := t.TempDir()
slicesDir := filepath.Join(dir, "slices")
if err := os.MkdirAll(slicesDir, 0755); err != nil {
t.Fatal(err)
}
diskContent := `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`
filePath := filepath.Join(slicesDir, "libc6.yaml")
if err := os.WriteFile(filePath, []byte(diskContent), 0644); err != nil {
t.Fatal(err)
}

idx, err := index.New(dir, nil, nil)
if err != nil {
t.Fatal(err)
}
defer idx.Close()

// Simulate an unsaved edit: inject an extra slice into the index.
editedContent := diskContent + "  injected:\n    contents:\n      /tmp/evil:\n"
editedSF, err := parser.ParseBytes([]byte(editedContent))
if err != nil {
t.Fatal(err)
}
idx.UpdateFile(filePath, editedSF)

// Verify the injected slice is visible before close.
refs := idx.AllSliceRefs()
found := false
for _, r := range refs {
if r == "libc6_injected" {
found = true
}
}
if !found {
t.Fatalf("expected libc6_injected in index before close, got: %v", refs)
}

// Simulate textDocumentDidClose by reverting the index to disk.
srv := lsp.NewWithIndex(idx)
if err := srv.ExportRevertToDisk(filePath); err != nil {
t.Fatalf("ExportRevertToDisk: %v", err)
}

// Verify the injected slice is gone after revert.
refs = idx.AllSliceRefs()
for _, r := range refs {
if r == "libc6_injected" {
t.Errorf("libc6_injected still in index after close, refs: %v", refs)
}
}

// The legitimate slice should still be there.
found = false
for _, r := range refs {
if r == "libc6_libs" {
found = true
}
}
if !found {
t.Errorf("libc6_libs missing after revert, refs: %v", refs)
}
}

// --- textDocument/references ---

func TestReferences_FromEssentialEntry(t *testing.T) {
// openssl.yaml has "- libc6_libs" at a known line.
// Placing the cursor on that token should return a reference location.
idx, slicesDir := setupLSPIndex(t, map[string]string{
"libc6.yaml": `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`,
"openssl.yaml": `package: openssl
slices:
  bins:
    essential:
      - libc6_libs
    contents:
      /usr/bin/openssl:
`,
"curl.yaml": `package: curl
slices:
  bins:
    essential:
      - libc6_libs
      - openssl_bins
    contents:
      /usr/bin/curl:
`,
})

opensslPath := filepath.Join(slicesDir, "openssl.yaml")
// Line 4 (0-based) is "      - libc6_libs"
srv := lsp.NewWithIndex(idx)
srv.SetDocForTest(opensslPath, `package: openssl
slices:
  bins:
    essential:
      - libc6_libs
    contents:
      /usr/bin/openssl:
`)
locs, err := srv.ExportReferences(opensslPath, 4, 10)
if err != nil {
t.Fatal(err)
}
if len(locs) != 2 {
t.Fatalf("expected 2 references to libc6_libs, got %d: %v", len(locs), locs)
}
}

func TestReferences_FromSliceDefinition(t *testing.T) {
// Placing the cursor on the "libs:" key in libc6.yaml should find references
// to libc6_libs across all files.
idx, slicesDir := setupLSPIndex(t, map[string]string{
"libc6.yaml": `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`,
"openssl.yaml": `package: openssl
slices:
  bins:
    essential:
      - libc6_libs
    contents:
      /usr/bin/openssl:
`,
})

libc6Path := filepath.Join(slicesDir, "libc6.yaml")
// Line 2 (0-based) is "  libs:"
srv := lsp.NewWithIndex(idx)
locs, err := srv.ExportReferences(libc6Path, 2, 3)
if err != nil {
t.Fatal(err)
}
if len(locs) != 1 {
t.Fatalf("expected 1 reference to libc6_libs from definition, got %d: %v", len(locs), locs)
}
}

func TestReferences_NoResults(t *testing.T) {
idx, slicesDir := setupLSPIndex(t, map[string]string{
"libc6.yaml": `package: libc6
slices:
  copyright:
    contents:
      /usr/share/doc/libc6/copyright:
`,
})

libc6Path := filepath.Join(slicesDir, "libc6.yaml")
srv := lsp.NewWithIndex(idx)
// Line 2 is "  copyright:" — nobody externally references libc6_copyright,
// so the fallback returns the definition itself (1 location).
locs, err := srv.ExportReferences(libc6Path, 2, 3)
if err != nil {
t.Fatal(err)
}
// Fallback: the definition location is returned so the user always gets
// at least one result when calling Find References on a defined slice.
if len(locs) != 1 {
t.Errorf("expected 1 location (definition fallback), got %d: %v", len(locs), locs)
}
}

func TestReferences_IncludeDeclaration(t *testing.T) {
	// When includeDeclaration=true, the definition's NameRange should be
	// prepended to the results even when there are no other references.
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"libc6.yaml": `package: libc6
slices:
  copyright:
    contents:
      /usr/share/doc/libc6/copyright:
`,
	})

	libc6Path := filepath.Join(slicesDir, "libc6.yaml")
	content := `package: libc6
slices:
  copyright:
    contents:
      /usr/share/doc/libc6/copyright:
`
	srv := lsp.NewWithIndex(idx)
	// Line 2 is "  copyright:" — nobody references libc6_copyright.
	// Without includeDeclaration, result is empty.
	locs := srv.ExportReferencesWithDecl(libc6Path, content, 2, 3)
	if len(locs) != 1 {
		t.Fatalf("expected 1 location (definition) with includeDeclaration=true, got %d: %v", len(locs), locs)
	}
	// The returned location must point to libc6.yaml.
	if locs[0].URI != lsp.ExportFilePathToURI(libc6Path) {
		t.Errorf("definition location URI: got %s, want %s", locs[0].URI, lsp.ExportFilePathToURI(libc6Path))
	}
}

func TestReferences_IncludeDeclarationWithRefs(t *testing.T) {
	// When includeDeclaration=true, definition is first and references follow.
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"libc6.yaml": `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`,
		"openssl.yaml": `package: openssl
slices:
  bins:
    essential:
      - libc6_libs
    contents:
      /usr/bin/openssl:
`,
	})

	libc6Path := filepath.Join(slicesDir, "libc6.yaml")
	content := `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`
	srv := lsp.NewWithIndex(idx)
	// Line 2 is "  libs:" — definition cursor; openssl.yaml has 1 reference.
	locs := srv.ExportReferencesWithDecl(libc6Path, content, 2, 3)
	if len(locs) != 2 {
		t.Fatalf("expected 2 locations (1 definition + 1 ref), got %d: %v", len(locs), locs)
	}
	// First must be the definition (libc6.yaml).
	if locs[0].URI != lsp.ExportFilePathToURI(libc6Path) {
		t.Errorf("first location should be definition (libc6.yaml), got %s", locs[0].URI)
	}
}

// --- textDocument/rename ---

func TestRename_FromEssentialRef(t *testing.T) {
// openssl.yaml has "- libc6_libs". Rename libc6_libs → libc6_shared_libs
// from that essential entry. The definition in libc6.yaml and the reference
// in openssl.yaml should both be updated.
libc6Content := `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`
opensslContent := `package: openssl
slices:
  bins:
    essential:
      - libc6_libs
    contents:
      /usr/bin/openssl:
`
idx, slicesDir := setupLSPIndex(t, map[string]string{
"libc6.yaml":   libc6Content,
"openssl.yaml": opensslContent,
})

libc6Path := filepath.Join(slicesDir, "libc6.yaml")
opensslPath := filepath.Join(slicesDir, "openssl.yaml")

srv := lsp.NewWithIndex(idx)
srv.SetDocForTest(opensslPath, opensslContent)

// Line 4 (0-based) in openssl.yaml is "      - libc6_libs"
edit, err := srv.ExportRename(opensslPath, 4, 10, "libc6_shared_libs")
if err != nil {
t.Fatal(err)
}
if edit == nil {
t.Fatal("expected non-nil WorkspaceEdit")
}

libc6URI := lsp.ExportFilePathToURI(libc6Path)
opensslURI := lsp.ExportFilePathToURI(opensslPath)

defEdits := edit.Changes[libc6URI]
if len(defEdits) != 1 {
t.Errorf("expected 1 edit in libc6.yaml (definition), got %d", len(defEdits))
} else if defEdits[0].NewText != "shared_libs" {
t.Errorf("definition edit NewText: got %q, want %q", defEdits[0].NewText, "shared_libs")
}

refEdits := edit.Changes[opensslURI]
if len(refEdits) != 1 {
t.Errorf("expected 1 edit in openssl.yaml (reference), got %d", len(refEdits))
} else if refEdits[0].NewText != "libc6_shared_libs" {
t.Errorf("reference edit NewText: got %q, want %q", refEdits[0].NewText, "libc6_shared_libs")
}
}

func TestRename_FromDefinition_BareNewName(t *testing.T) {
// Cursor on the "libs:" definition key; user types bare "shared_libs".
libc6Content := `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`
opensslContent := `package: openssl
slices:
  bins:
    essential:
      - libc6_libs
    contents:
      /usr/bin/openssl:
`
idx, slicesDir := setupLSPIndex(t, map[string]string{
"libc6.yaml":   libc6Content,
"openssl.yaml": opensslContent,
})

libc6Path := filepath.Join(slicesDir, "libc6.yaml")

srv := lsp.NewWithIndex(idx)
// Line 2 (0-based) in libc6.yaml is "  libs:"
edit, err := srv.ExportRename(libc6Path, 2, 3, "shared_libs")
if err != nil {
t.Fatal(err)
}
if edit == nil {
t.Fatal("expected non-nil WorkspaceEdit")
}

libc6URI := lsp.ExportFilePathToURI(libc6Path)
opensslURI := lsp.ExportFilePathToURI(filepath.Join(slicesDir, "openssl.yaml"))

defEdits := edit.Changes[libc6URI]
if len(defEdits) != 1 || defEdits[0].NewText != "shared_libs" {
t.Errorf("definition: got %+v, want NewText=shared_libs", defEdits)
}

refEdits := edit.Changes[opensslURI]
if len(refEdits) != 1 || refEdits[0].NewText != "libc6_shared_libs" {
t.Errorf("reference: got %+v, want NewText=libc6_shared_libs", refEdits)
}
}

func TestRename_NewNameWithoutPackagePrefix(t *testing.T) {
// New name "openssl_libs" has no "libc6_" prefix, so it is used as-is
// as the bare slice name, resulting in definition = "openssl_libs" and
// essential refs = "libc6_openssl_libs".
content := `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`
idx, slicesDir := setupLSPIndex(t, map[string]string{"libc6.yaml": content})

srv := lsp.NewWithIndex(idx)
libc6Path := filepath.Join(slicesDir, "libc6.yaml")
// Line 2 is "  libs:" — cursor on definition
edit, err := srv.ExportRename(libc6Path, 2, 3, "openssl_libs")
if err != nil {
t.Fatal(err)
}
if edit == nil {
t.Fatal("expected non-nil WorkspaceEdit")
}
libc6URI := lsp.ExportFilePathToURI(libc6Path)
defEdits := edit.Changes[libc6URI]
if len(defEdits) != 1 || defEdits[0].NewText != "openssl_libs" {
t.Errorf("definition: got %+v, want NewText=openssl_libs", defEdits)
}
}

// --- recordingNotifier test helper ---

type recordingNotifier struct {
calls []notifyCall
}

type notifyCall struct {
method string
params any
}

func (r *recordingNotifier) Notify(method string, params any) {
r.calls = append(r.calls, notifyCall{method, params})
}

func (r *recordingNotifier) diagParams() []protocol.PublishDiagnosticsParams {
var out []protocol.PublishDiagnosticsParams
for _, c := range r.calls {
if c.method == "textDocument/publishDiagnostics" {
if p, ok := c.params.(protocol.PublishDiagnosticsParams); ok {
out = append(out, p)
}
}
}
return out
}

// --- reindexAndPublish tests ---

func TestReindexAndPublish_ParseError(t *testing.T) {
idx, slicesDir := setupLSPIndex(t, map[string]string{
"libc6.yaml": `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`,
})

srv := lsp.NewWithIndex(idx)
n := &recordingNotifier{}
badContent := []byte("package: libc6\nslices: [\ninvalid yaml")
srv.ExportReindexAndPublish(n, filepath.Join(slicesDir, "libc6.yaml"), badContent)

params := n.diagParams()
if len(params) != 1 {
t.Fatalf("expected 1 publishDiagnostics call, got %d", len(params))
}
if len(params[0].Diagnostics) == 0 {
t.Fatal("expected at least one diagnostic for parse error")
}
if !strings.Contains(params[0].Diagnostics[0].Message, "YAML parse error") {
t.Errorf("expected parse error message, got %q", params[0].Diagnostics[0].Message)
}
}

func TestReindexAndPublish_CleanFile(t *testing.T) {
idx, slicesDir := setupLSPIndex(t, map[string]string{
"libc6.yaml": `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`,
})

srv := lsp.NewWithIndex(idx)
n := &recordingNotifier{}
cleanContent := []byte(`package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`)
srv.ExportReindexAndPublish(n, filepath.Join(slicesDir, "libc6.yaml"), cleanContent)

params := n.diagParams()
if len(params) != 1 {
t.Fatalf("expected 1 publishDiagnostics call, got %d", len(params))
}
if len(params[0].Diagnostics) != 0 {
t.Errorf("expected empty diagnostics for clean file, got %v", params[0].Diagnostics)
}
}

func TestReindexAndPublish_GlobError(t *testing.T) {
idx, slicesDir := setupLSPIndex(t, map[string]string{
"libc6.yaml": `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`,
})

srv := lsp.NewWithIndex(idx)
n := &recordingNotifier{}
// relative path is invalid
contentWithBadGlob := []byte(`package: libc6
slices:
  libs:
    contents:
      relative/path:
`)
srv.ExportReindexAndPublish(n, filepath.Join(slicesDir, "libc6.yaml"), contentWithBadGlob)

params := n.diagParams()
if len(params) != 1 || len(params[0].Diagnostics) == 0 {
t.Fatalf("expected diagnostics for bad glob, got params=%v", params)
}
if !strings.Contains(params[0].Diagnostics[0].Message, "absolute") {
t.Errorf("expected 'absolute' in diagnostic, got %q", params[0].Diagnostics[0].Message)
}
}

func TestPublishDiagnosticsForFile_WithIssues(t *testing.T) {
idx, slicesDir := setupLSPIndex(t, map[string]string{
"libc6.yaml": `package: libc6
slices:
  libs:
    essential:
      - nonexistent_slice
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`,
})

srv := lsp.NewWithIndex(idx)
n := &recordingNotifier{}
srv.ExportPublishDiagnosticsForFile(n, filepath.Join(slicesDir, "libc6.yaml"))

params := n.diagParams()
if len(params) != 1 {
t.Fatalf("expected 1 publishDiagnostics call, got %d", len(params))
}
found := false
for _, d := range params[0].Diagnostics {
if strings.Contains(d.Message, "unknown slice reference") {
found = true
}
}
if !found {
t.Errorf("expected 'unknown slice reference' diagnostic, got %v", params[0].Diagnostics)
}
}

func TestRepublishOpenFiles_SendsDiagsForOpenDocs(t *testing.T) {
idx, slicesDir := setupLSPIndex(t, map[string]string{
"libc6.yaml": `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`,
"openssl.yaml": `package: openssl
slices:
  bins:
    essential:
      - libc6_libs
    contents:
      /usr/bin/openssl:
`,
})

opensslPath := filepath.Join(slicesDir, "openssl.yaml")
srv := lsp.NewWithIndex(idx)
// Mark openssl.yaml as open.
srv.SetDocForTest(opensslPath, `package: openssl
slices:
  bins:
    essential:
      - libc6_libs
    contents:
      /usr/bin/openssl:
`)

n := &recordingNotifier{}
// skipPath = libc6.yaml; only openssl.yaml (open) should get republished.
libc6Path := filepath.Join(slicesDir, "libc6.yaml")
srv.ExportRepublishOpenFiles(n, libc6Path)

params := n.diagParams()
if len(params) != 1 {
t.Fatalf("expected 1 republish notification (for openssl.yaml), got %d", len(params))
}
if params[0].URI != lsp.ExportFilePathToURI(opensslPath) {
t.Errorf("expected openssl.yaml URI, got %s", params[0].URI)
}
}

// --- textDocument/codeAction ---

func TestCodeAction_UnknownRef_OffersRemove(t *testing.T) {
	// foo.yaml references nonexistent_slice which doesn't exist in the index.
	// computeDiagnostics should emit an unknown-slice-ref diagnostic; passing it
	// back to computeCodeActions should produce a "Remove unknown reference" action
	// that deletes the offending line.
	fooContent := `package: foo
slices:
  bins:
    essential:
      - nonexistent_slice
    contents:
      /usr/bin/foo:
`
	idx, slicesDir := setupLSPIndex(t, map[string]string{"foo.yaml": fooContent})
	fooPath := filepath.Join(slicesDir, "foo.yaml")

	srv := lsp.NewWithIndex(idx)
	srv.SetDocForTest(fooPath, fooContent)

	diags := srv.ExportComputeDiagnostics(fooPath)

	// Find the unknown-ref diagnostic.
	var refDiags []protocol.Diagnostic
	for _, d := range diags {
		if d.Code != nil {
			if code, ok := d.Code.Value.(string); ok && code == "unknown-slice-ref" {
				refDiags = append(refDiags, d)
			}
		}
	}
	if len(refDiags) == 0 {
		t.Fatalf("expected unknown-slice-ref diagnostic, got %v", diags)
	}

	actions := srv.ExportCodeAction(fooPath, refDiags)
	if len(actions) == 0 {
		t.Fatal("expected at least one code action")
	}

	found := false
	for _, a := range actions {
		if a.Title == "Remove unknown reference" {
			found = true
			// Verify the edit deletes the whole line.
			if a.Edit == nil {
				t.Error("code action has no Edit")
				continue
			}
			uri := lsp.ExportFilePathToURI(fooPath)
			edits := a.Edit.Changes[uri]
			if len(edits) != 1 {
				t.Errorf("expected 1 TextEdit, got %d", len(edits))
				continue
			}
			if edits[0].NewText != "" {
				t.Errorf("expected NewText to be empty (deletion), got %q", edits[0].NewText)
			}
			if edits[0].Range.Start.Character != 0 {
				t.Errorf("edit range start character should be 0 (full line), got %d", edits[0].Range.Start.Character)
			}
		}
	}
	if !found {
		t.Errorf("no 'Remove unknown reference' action found in: %v", actions)
	}
}

func TestCodeAction_PackageMismatch_OffersFix(t *testing.T) {
	// wrong.yaml declares package: wrong but the filename is "foo.yaml".
	// The mismatch should trigger a "Fix package name to..." action.
	fooContent := `package: wrong
slices:
  bins:
    contents:
      /usr/bin/foo:
`
	idx, slicesDir := setupLSPIndex(t, map[string]string{"foo.yaml": fooContent})
	fooPath := filepath.Join(slicesDir, "foo.yaml")

	srv := lsp.NewWithIndex(idx)
	srv.SetDocForTest(fooPath, fooContent)

	diags := srv.ExportComputeDiagnostics(fooPath)

	var mismatchDiags []protocol.Diagnostic
	for _, d := range diags {
		if d.Code != nil {
			if code, ok := d.Code.Value.(string); ok && code == "package-name-mismatch" {
				mismatchDiags = append(mismatchDiags, d)
			}
		}
	}
	if len(mismatchDiags) == 0 {
		t.Fatalf("expected package-name-mismatch diagnostic, got %v", diags)
	}

	actions := srv.ExportCodeAction(fooPath, mismatchDiags)
	if len(actions) == 0 {
		t.Fatal("expected at least one code action")
	}

	found := false
	for _, a := range actions {
		if strings.Contains(a.Title, "Fix package name") && strings.Contains(a.Title, "foo") {
			found = true
			if a.Edit == nil {
				t.Error("code action has no Edit")
				continue
			}
			uri := lsp.ExportFilePathToURI(fooPath)
			edits := a.Edit.Changes[uri]
			if len(edits) != 1 || edits[0].NewText != "foo" {
				t.Errorf("expected NewText=%q, got edits=%v", "foo", edits)
			}
		}
	}
	if !found {
		t.Errorf("no 'Fix package name to ...' action found, got: %v", actions)
	}
}

func TestCodeAction_NoDiagnostics_ReturnsEmpty(t *testing.T) {
	cleanContent := `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`
	idx, slicesDir := setupLSPIndex(t, map[string]string{"libc6.yaml": cleanContent})
	libc6Path := filepath.Join(slicesDir, "libc6.yaml")

	srv := lsp.NewWithIndex(idx)
	srv.SetDocForTest(libc6Path, cleanContent)

	actions := srv.ExportCodeAction(libc6Path, []protocol.Diagnostic{})
	if len(actions) != 0 {
		t.Errorf("expected no actions for empty diagnostics, got %v", actions)
	}
}

func TestCodeAction_InvalidRef_OffersRemove(t *testing.T) {
	// "badformat" has no underscore — invalid slice reference format.
	fooContent := `package: foo
slices:
  bins:
    essential:
      - badformat
    contents:
      /usr/bin/foo:
`
	idx, slicesDir := setupLSPIndex(t, map[string]string{"foo.yaml": fooContent})
	fooPath := filepath.Join(slicesDir, "foo.yaml")

	srv := lsp.NewWithIndex(idx)
	srv.SetDocForTest(fooPath, fooContent)

	diags := srv.ExportComputeDiagnostics(fooPath)

	var invalidDiags []protocol.Diagnostic
	for _, d := range diags {
		if d.Code != nil {
			if code, ok := d.Code.Value.(string); ok && code == "invalid-slice-ref" {
				invalidDiags = append(invalidDiags, d)
			}
		}
	}
	if len(invalidDiags) == 0 {
		t.Fatalf("expected invalid-slice-ref diagnostic, got %v", diags)
	}

	actions := srv.ExportCodeAction(fooPath, invalidDiags)
	found := false
	for _, a := range actions {
		if a.Title == "Remove invalid reference" {
			found = true
		}
	}
	if !found {
		t.Errorf("no 'Remove invalid reference' action found, got: %v", actions)
	}
}

func TestCodeAction_AllDiagnosticsHaveCodes(t *testing.T) {
	// Verify that every diagnostic emitted by computeDiagnostics has a Code set.
	fooContent := `package: wrongname
slices:
  bins:
    essential:
      - nonexistent_slice
      - badformat
    contents:
      relative/path:
`
	idx, slicesDir := setupLSPIndex(t, map[string]string{"foo.yaml": fooContent})
	fooPath := filepath.Join(slicesDir, "foo.yaml")

	srv := lsp.NewWithIndex(idx)
	diags := srv.ExportComputeDiagnostics(fooPath)

	if len(diags) == 0 {
		t.Fatal("expected diagnostics, got none")
	}
	for _, d := range diags {
		if d.Code == nil {
			t.Errorf("diagnostic missing Code: %q", d.Message)
		}
	}
}
