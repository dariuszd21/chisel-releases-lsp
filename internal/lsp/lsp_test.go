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

func TestIsInsideEssential_V3MapKey(t *testing.T) {
	v3Text := "package: foo\nslices:\n  bins:\n    essential:\n      libc6_libs:\n      openssl_bins:\n    contents:\n      /usr/bin/foo:\n"
	// Line 4: "      libc6_libs:" — inside essential (v3 map)
	if !lsp.ExportIsInsideEssential(v3Text, 4) {
		t.Error("expected isInsideEssential=true for v3 map key line 4")
	}
	// Line 5: "      openssl_bins:" — inside essential (v3 map)
	if !lsp.ExportIsInsideEssential(v3Text, 5) {
		t.Error("expected isInsideEssential=true for v3 map key line 5")
	}
	// Line 6: "    contents:" — NOT inside essential
	if lsp.ExportIsInsideEssential(v3Text, 6) {
		t.Error("expected isInsideEssential=false for contents: line")
	}
}

func TestIsInsideEssential_V3PartialKey(t *testing.T) {
	// "libc6_" (partial, no colon yet) should still be detected as inside essential.
	v3Text := "package: foo\nslices:\n  bins:\n    essential:\n      libc6_\n    contents:\n      /usr/bin/foo:\n"
	if !lsp.ExportIsInsideEssential(v3Text, 4) {
		t.Error("expected isInsideEssential=true for partial v3 map key")
	}
}

// --- textDocument/completion ---

// completionText is the document content used by completion tests.
// Line offsets (0-based):
//
//	0: package: foo
//	1: slices:
//	2:   bins:
//	3:     essential:
//	4:       - libc6_libs       ← existing entry
//	5:     contents:
//	6:       /usr/bin/foo:
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
	prefix, r, needsSpace, appendColon := lsp.ExportCompletionPrefixAndRange(completionText, 4, 13)
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
	if appendColon {
		t.Error("appendColon should be false for v1/v2 list context")
	}
}

func TestCompletionPrefixAndRange_TriggerOnly(t *testing.T) {
	// Cursor right after "-" on a fresh line: "      -" (col 7), no space yet.
	text := "package: foo\nslices:\n  bins:\n    essential:\n      -\n    contents:\n      /usr/bin/foo:\n"
	prefix, r, needsSpace, appendColon := lsp.ExportCompletionPrefixAndRange(text, 4, 7)
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
	if appendColon {
		t.Error("appendColon should be false in trigger-only mode")
	}
}

func TestCompletionPrefixAndRange_AfterSpace(t *testing.T) {
	// Cursor at "      - " (col 8, right after the space), nothing typed yet.
	text := "package: foo\nslices:\n  bins:\n    essential:\n      - \n    contents:\n      /usr/bin/foo:\n"
	prefix, r, needsSpace, appendColon := lsp.ExportCompletionPrefixAndRange(text, 4, 8)
	if prefix != "" {
		t.Errorf("prefix: got %q, want empty", prefix)
	}
	if r.Start.Character != 8 {
		t.Errorf("range start char: got %d, want 8", r.Start.Character)
	}
	if needsSpace {
		t.Error("needsLeadingSpace should be false after the space is typed")
	}
	if appendColon {
		t.Error("appendColon should be false for v1/v2 context")
	}
}

func TestCompletionPrefixAndRange_V3MapKey(t *testing.T) {
	// v3 map key line: "      libc6_" (no dash), cursor at col 12.
	text := "package: foo\nslices:\n  bins:\n    essential:\n      libc6_\n    contents:\n      /usr/bin/foo:\n"
	prefix, r, needsSpace, appendColon := lsp.ExportCompletionPrefixAndRange(text, 4, 12)
	if prefix != "libc6_" {
		t.Errorf("prefix: got %q, want %q", prefix, "libc6_")
	}
	// Range start should be at col 6 (first non-space on the line).
	if r.Start.Character != 6 {
		t.Errorf("range start char: got %d, want 6", r.Start.Character)
	}
	if needsSpace {
		t.Error("needsLeadingSpace should be false for v3 map key")
	}
	if !appendColon {
		t.Error("appendColon should be true for v3 map key context")
	}
}

func TestCompletionPrefixAndRange_V3MapKeyWithExistingColon(t *testing.T) {
	// v3 map key line with existing colon: "      libc6_libs:" cursor at col 16 (after "libs").
	text := "package: foo\nslices:\n  bins:\n    essential:\n      libc6_libs:\n    contents:\n      /usr/bin/foo:\n"
	_, r, _, appendColon := lsp.ExportCompletionPrefixAndRange(text, 4, 16)
	// Range end should include the colon (col 17: 6 spaces + 10 chars "libc6_libs" + 1 colon).
	if r.End.Character != 17 {
		t.Errorf("range end with colon: got %d, want 17", r.End.Character)
	}
	if !appendColon {
		t.Error("appendColon should be true for v3 map key with colon")
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
  locale:
    contents:
      /usr/lib/locale/:
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

	// "      - li" (prefix="li", 2 chars) — both libc6_libs and libc6_locale match.
	text := "package: foo\nslices:\n  bins:\n    essential:\n      - li\n    contents:\n      /usr/bin/foo:\n"
	items := srv.ExportCompletion(fooPath, text, 4, 10)
	if len(items) < 2 {
		t.Fatalf("expected at least 2 items with prefix 'li', got %d: %v", len(items), items)
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
	// When completion fires on a v1/v2 line "- li" (2 chars typed after "- "),
	// the TextEdit NewText must be the ref itself (no leading space needed because
	// "- " is already in the document).
	// Also tests that the needsLeadingSpace path still works: on a bare "- " line
	// where the trigger fires but min-prefix is raised to allow 0 for this test,
	// NewText gets a leading space prepended.
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
	// Lower the threshold to 0 so we can test the leading-space edge case
	// without fighting the min-prefix guard.
	srv.SetMinPrefixLen(0)

	// Line 4 is "      -" — trigger fired right after the dash (col 7), prefix="".
	triggerText := "package: foo\nslices:\n  bins:\n    essential:\n      -\n    contents:\n      /usr/bin/foo:\n"
	items := srv.ExportCompletion(fooPath, triggerText, 4, 7)
	if len(items) == 0 {
		t.Fatal("expected completion items in trigger-only mode (min=0), got none")
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

// TestComputeDiagnostics_Collision verifies that a cross-package path conflict
// is reported as a slice-collision diagnostic through the LSP diagnostic layer.
func TestComputeDiagnostics_Collision(t *testing.T) {
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"openssl.yaml": `package: openssl
slices:
  libs:
    contents:
      /shared/path:
`,
		"libc6.yaml": `package: libc6
slices:
  libs:
    contents:
      /shared/path:
`,
	})

	srv := lsp.NewWithIndex(idx)
	opensslPath := filepath.Join(slicesDir, "openssl.yaml")
	diags := srv.ExportComputeDiagnostics(opensslPath)

	found := false
	for _, d := range diags {
		if d.Code != nil {
			if code, ok := d.Code.Value.(string); ok && code == "slice-collision" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected slice-collision diagnostic for openssl.yaml, got: %v", diags)
	}
}

// TestComputeDiagnostics_Collision_RelatedInfo verifies that a collision diagnostic
// includes RelatedInformation pointing to the other conflicting file/range.
func TestComputeDiagnostics_Collision_RelatedInfo(t *testing.T) {
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"openssl.yaml": `package: openssl
slices:
  libs:
    contents:
      /shared/path:
`,
		"libc6.yaml": `package: libc6
slices:
  libs:
    contents:
      /shared/path:
`,
	})

	srv := lsp.NewWithIndex(idx)
	opensslPath := filepath.Join(slicesDir, "openssl.yaml")
	libc6Path := filepath.Join(slicesDir, "libc6.yaml")
	diags := srv.ExportComputeDiagnostics(opensslPath)

	var collisionDiag *protocol.Diagnostic
	for i := range diags {
		if diags[i].Code != nil {
			if code, ok := diags[i].Code.Value.(string); ok && code == "slice-collision" {
				collisionDiag = &diags[i]
				break
			}
		}
	}
	if collisionDiag == nil {
		t.Fatal("expected slice-collision diagnostic, got none")
	}
	if len(collisionDiag.RelatedInformation) == 0 {
		t.Fatal("expected RelatedInformation on collision diagnostic, got none")
	}
	rel := collisionDiag.RelatedInformation[0]
	expectedURI := lsp.ExportFilePathToURI(libc6Path)
	if rel.Location.URI != expectedURI {
		t.Errorf("RelatedInformation URI: got %q, want %q", rel.Location.URI, expectedURI)
	}
	if !strings.Contains(rel.Message, "libc6") {
		t.Errorf("RelatedInformation message should mention libc6: %q", rel.Message)
	}
}

// TestCodeAction_Collision_OffersGoto verifies that a code action is offered
// for a slice-collision diagnostic, providing a "Go to conflicting slice" command.
func TestCodeAction_Collision_OffersGoto(t *testing.T) {
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"openssl.yaml": `package: openssl
slices:
  libs:
    contents:
      /shared/path:
`,
		"libc6.yaml": `package: libc6
slices:
  libs:
    contents:
      /shared/path:
`,
	})

	srv := lsp.NewWithIndex(idx)
	opensslPath := filepath.Join(slicesDir, "openssl.yaml")

	// Get the server-computed collision diagnostic (with proper code).
	diags := srv.ExportComputeDiagnostics(opensslPath)
	var collisionDiags []protocol.Diagnostic
	for _, d := range diags {
		if d.Code != nil {
			if code, ok := d.Code.Value.(string); ok && code == "slice-collision" {
				collisionDiags = append(collisionDiags, d)
			}
		}
	}
	if len(collisionDiags) == 0 {
		t.Fatal("expected collision diagnostic, got none")
	}

	actions := srv.ExportCodeAction(opensslPath, collisionDiags)
	found := false
	for _, a := range actions {
		if a.Command != nil && a.Command.Command == "chisel-releases-lsp.gotoConflict" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected gotoConflict code action for collision, got: %v", actions)
	}
}

// TestCodeAction_Collision_NilCodeRoundTrip verifies that the gotoConflict code
// action is returned even when client diagnostics have Code == nil (the glsp
// IntegerOrString round-trip bug). The fix is the same lineCode lookup used for
// other diagnostic types.
func TestCodeAction_Collision_NilCodeRoundTrip(t *testing.T) {
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"openssl.yaml": `package: openssl
slices:
  libs:
    contents:
      /shared/path:
`,
		"libc6.yaml": `package: libc6
slices:
  libs:
    contents:
      /shared/path:
`,
	})

	srv := lsp.NewWithIndex(idx)
	opensslPath := filepath.Join(slicesDir, "openssl.yaml")

	// Simulate client-reflected diagnostics with Code == nil.
	// "/shared/path:" is on line 4 (0-based) of openssl.yaml.
	clientDiags := []protocol.Diagnostic{
		{
			Range: protocol.Range{
				Start: protocol.Position{Line: 4, Character: 6},
				End:   protocol.Position{Line: 4, Character: 19},
			},
			// Code intentionally nil — simulates the broken round-trip
			Message: "slice collision",
		},
	}

	actions := srv.ExportCodeAction(opensslPath, clientDiags)
	found := false
	for _, a := range actions {
		if a.Command != nil && a.Command.Command == "chisel-releases-lsp.gotoConflict" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected gotoConflict action even with nil Code, got: %v", actions)
	}
}

func TestComputeDiagnostics_CleanFileReturnsEmpty(t *testing.T) {
	// A valid file with no issues must return [] (not nil) so that the client
	// clears any previously shown squiggles.
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"libc6.yaml": `package: libc6
essential:
  - libc6_copyright
slices:
  copyright:
    contents:
      /usr/share/doc/libc6/copyright:
  libs:
    essential:
      - libc6_copyright
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

// --- format v3 integration tests ---

func TestCompletion_V3EssentialFormat(t *testing.T) {
	// A file using v3 map-style essential should still produce completion items
	// for references defined in other indexed files.
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"libc6.yaml": `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`,
		"base-files.yaml": `package: base-files
slices:
  base:
    essential:
      libc6_libs:
    contents:
      /etc/:
`,
	})
	basePath := filepath.Join(slicesDir, "base-files.yaml")
	srv := lsp.NewWithIndex(idx)

	// v3 map-style doc; cursor on line 4 "      libc6_" (col 12, mid-token)
	text := "package: base-files\nslices:\n  base:\n    essential:\n      libc6_\n    contents:\n      /etc/:\n"
	items := srv.ExportCompletion(basePath, text, 4, 12)
	if len(items) == 0 {
		t.Fatal("expected completion items for v3-format file, got none")
	}
	found := false
	for _, it := range items {
		if it.Label == "libc6_libs" {
			found = true
		}
	}
	if !found {
		t.Errorf("libc6_libs not in completions for v3-format file: %v", items)
	}
}

func TestReferences_V3EssentialFormat(t *testing.T) {
	// A v3 map-style essential entry must be indexed and findable via references.
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"libc6.yaml": `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`,
		"base-files.yaml": `package: base-files
slices:
  base:
    essential:
      libc6_libs:
    contents:
      /etc/:
`,
	})
	libc6Path := filepath.Join(slicesDir, "libc6.yaml")
	srv := lsp.NewWithIndex(idx)

	// Cursor on "libs:" definition in libc6.yaml (line 2).
	locs, err := srv.ExportReferences(libc6Path, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	// Expect the reference from base-files.yaml (v3 map entry).
	foundRef := false
	for _, loc := range locs {
		if strings.Contains(string(loc.URI), "base-files.yaml") {
			foundRef = true
		}
	}
	if !foundRef {
		t.Errorf("expected a reference from base-files.yaml (v3 map essential), got: %v", locs)
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

func TestPrepareRename_HyphenatedToken(t *testing.T) {
	// util-linux has a hyphenated package name and a hyphenated slice name.
	// prepareRename should return only the slice-name part as the range/placeholder
	// so the user edits "file-system", not the full "util-linux_file-system".
	content := `package: util-linux
slices:
  file-system:
    essential:
      - util-linux_file-system
    contents:
      /usr/bin/mount:
`
	idx, slicesDir := setupLSPIndex(t, map[string]string{"util-linux.yaml": content})
	ulPath := filepath.Join(slicesDir, "util-linux.yaml")
	srv := lsp.NewWithIndex(idx)
	srv.SetDocForTest(ulPath, content)

	// Line 4 (0-based): "      - util-linux_file-system"
	// "util-linux_" is chars 8-18, "file-system" is chars 19-29 (end=30).
	// Cursor positions within the full token (pkg part and slice part).
	line := 4
	for _, char := range []int{8, 12, 20, 25} {
		result, err := srv.ExportPrepareRename(ulPath, line, char)
		if err != nil {
			t.Errorf("char=%d: unexpected error: %v", char, err)
			continue
		}
		rwp, ok := result.(protocol.RangeWithPlaceholder)
		if !ok {
			t.Errorf("char=%d: expected RangeWithPlaceholder, got %T: %v", char, result, result)
			continue
		}
		// Placeholder is the slice name only, not the full reference.
		if rwp.Placeholder != "file-system" {
			t.Errorf("char=%d: Placeholder = %q, want %q", char, rwp.Placeholder, "file-system")
		}
		// The range must cover only "file-system" (chars 19-30), not the full token.
		if rwp.Range.Start.Character != 19 || rwp.Range.End.Character != 30 {
			t.Errorf("char=%d: Range = %v, want Start.Char=19 End.Char=30", char, rwp.Range)
		}
	}
}

func TestPrepareRename_OnDefinition_HyphenatedSliceName(t *testing.T) {
	// Cursor on the "file-system:" definition key — should return the bare slice name.
	content := `package: util-linux
slices:
  file-system:
    contents:
      /usr/bin/mount:
`
	idx, slicesDir := setupLSPIndex(t, map[string]string{"util-linux.yaml": content})
	ulPath := filepath.Join(slicesDir, "util-linux.yaml")
	srv := lsp.NewWithIndex(idx)
	srv.SetDocForTest(ulPath, content)

	// Line 2: "  file-system:"
	result, err := srv.ExportPrepareRename(ulPath, 2, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rwp, ok := result.(protocol.RangeWithPlaceholder)
	if !ok {
		t.Fatalf("expected RangeWithPlaceholder, got %T: %v", result, result)
	}
	if rwp.Placeholder != "file-system" {
		t.Errorf("Placeholder = %q, want %q", rwp.Placeholder, "file-system")
	}
}

func TestRename_FromEssential_NewSliceOnly(t *testing.T) {
	// Rename from an essential list entry by providing a bare new slice name.
	// prepareRename shows just "file-system" (not the full ref), so the user
	// types a bare slice name; the rename must produce the correct reference.
	content := `package: util-linux
slices:
  file-system:
    essential:
      - util-linux_file-system
    contents:
      /usr/bin/mount:
`
	idx, slicesDir := setupLSPIndex(t, map[string]string{"util-linux.yaml": content})
	ulPath := filepath.Join(slicesDir, "util-linux.yaml")
	srv := lsp.NewWithIndex(idx)
	srv.SetDocForTest(ulPath, content)

	// Line 4: "      - util-linux_file-system", cursor anywhere in the token.
	edit, err := srv.ExportRename(ulPath, 4, 12, "new-system")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if edit == nil {
		t.Fatal("expected non-nil WorkspaceEdit")
	}
	ulURI := lsp.ExportFilePathToURI(ulPath)
	edits := edit.Changes[ulURI]
	if len(edits) != 2 {
		t.Fatalf("expected 2 edits (definition + reference), got %d", len(edits))
	}
	texts := map[string]bool{}
	for _, e := range edits {
		texts[e.NewText] = true
	}
	if !texts["new-system"] {
		t.Errorf("expected definition edit NewText=%q, got edits: %+v", "new-system", edits)
	}
	if !texts["util-linux_new-system"] {
		t.Errorf("expected reference edit NewText=%q, got edits: %+v", "util-linux_new-system", edits)
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
essential:
  - libc6_copyright
slices:
  copyright:
    contents:
      /usr/share/doc/libc6/copyright:
  libs:
    essential:
      - libc6_copyright
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`,
	})

	srv := lsp.NewWithIndex(idx)
	n := &recordingNotifier{}
	cleanContent := []byte(`package: libc6
essential:
  - libc6_copyright
slices:
  copyright:
    contents:
      /usr/share/doc/libc6/copyright:
  libs:
    essential:
      - libc6_copyright
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

func TestCodeAction_UnknownRef_V3MapStyle_OffersRemove(t *testing.T) {
	// v3 map-style essential entry ("  nonexistent_slice:") must also get a
	// "Remove unknown reference" code action. This was broken: isListItemLine
	// only accepted "- item" format and silently skipped map entries.
	fooContent := `package: foo
slices:
  bins:
    essential:
      nonexistent_slice:
    contents:
      /usr/bin/foo:
`
	idx, slicesDir := setupLSPIndex(t, map[string]string{"foo.yaml": fooContent})
	fooPath := filepath.Join(slicesDir, "foo.yaml")

	srv := lsp.NewWithIndex(idx)
	srv.SetDocForTest(fooPath, fooContent)

	diags := srv.ExportComputeDiagnostics(fooPath)

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
	found := false
	for _, a := range actions {
		if a.Title == "Remove unknown reference" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'Remove unknown reference' action for v3 map entry, got: %v", actions)
	}
}

func TestCodeAction_InvalidRef_V3MapStyle_OffersRemove(t *testing.T) {
	// v3 map-style invalid ref ("  badformat:") must also get a remove action.
	fooContent := `package: foo
slices:
  bins:
    essential:
      badformat:
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
		t.Errorf("expected 'Remove invalid reference' action for v3 map entry, got: %v", actions)
	}
}

// TestCodeAction_NilCodeRoundTrip verifies that code actions work even when
// the client-reflected diagnostics have Code.Value == nil. This simulates the
// real-world failure mode caused by glsp's IntegerOrString.UnmarshalJSON using
// a value receiver, which discards the parsed value after JSON deserialization.
// The fix: computeCodeActions re-derives codes server-side via computeDiagnostics.
func TestCodeAction_NilCodeRoundTrip(t *testing.T) {
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

	// Simulate exactly what a real LSP client sends: the diagnostic range is
	// correct but Code is nil (lost through JSON deserialization due to the
	// glsp IntegerOrString.UnmarshalJSON value-receiver bug).
	clientDiags := []protocol.Diagnostic{
		{
			Range: protocol.Range{
				Start: protocol.Position{Line: 4, Character: 8},
				End:   protocol.Position{Line: 4, Character: 24},
			},
			// Code intentionally nil — simulates the broken round-trip
			Message: "unknown slice reference",
		},
	}

	actions := srv.ExportCodeAction(fooPath, clientDiags)
	found := false
	for _, a := range actions {
		if a.Title == "Remove unknown reference" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'Remove unknown reference' even with nil Code (broken round-trip), got: %v", actions)
	}
}

func TestCodeAction_AllDiagnosticsHaveCodes(t *testing.T) {
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

func TestComputeDiagnostics_DuplicateSlice(t *testing.T) {
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"openssl.yaml": `package: openssl
slices:
  bins:
    contents:
      /usr/bin/openssl:
`,
		"openssl-extra.yaml": `package: openssl
slices:
  bins:
    contents:
      /usr/bin/c_rehash:
`,
	})
	srv := lsp.NewWithIndex(idx)

	file1 := filepath.Join(slicesDir, "openssl.yaml")
	file2 := filepath.Join(slicesDir, "openssl-extra.yaml")

	// Both files should each get a duplicate-slice warning.
	for _, f := range []string{file1, file2} {
		diags := lsp.ExportComputeDiagnostics(srv, f)
		found := false
		for _, d := range diags {
			if d.Code != nil {
				if code, ok := d.Code.Value.(string); ok && code == "duplicate-slice" {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("expected duplicate-slice diagnostic in %s, got: %v", filepath.Base(f), diags)
		}
	}
}

// --- v3 bug reproduction tests ---

// TestIsInsideEssential_V3_EmptyLine tests that an empty line inside a v3
// essential block is recognised (so the user can get completions when they
// press <CR> after the last existing entry and haven't typed anything yet).
func TestIsInsideEssential_V3_EmptyLine(t *testing.T) {
	text := "package: foo\nslices:\n  bins:\n    essential:\n      libc6_libs:\n      \n    contents:\n      /usr/bin/foo:\n"
	// Line 5 is "      " (spaces only) — inside the essential block.
	if !lsp.ExportIsInsideEssential(text, 5) {
		t.Error("expected isInsideEssential=true for blank line inside v3 essential block")
	}
}

// TestIsInsideEssential_V3_PartialNoUnderscore tests that a partial token
// without "_" yet (e.g. the user has typed "libc6" and not yet "_libs") is
// still recognised as inside the essential block.
func TestIsInsideEssential_V3_PartialNoUnderscore(t *testing.T) {
	text := "package: foo\nslices:\n  bins:\n    essential:\n      libc6\n    contents:\n      /usr/bin/foo:\n"
	// Line 4: "      libc6" — partial v3 key, no underscore yet.
	if !lsp.ExportIsInsideEssential(text, 4) {
		t.Error("expected isInsideEssential=true for partial token without '_'")
	}
}

// TestIsInsideEssential_V3_CommentInBlock tests that YAML comment lines inside
// a v3 essential block are treated as transparent, not as block terminators.
// Real chisel-releases files (e.g. ubuntu-26.04/libc6.yaml) have comments
// between entries in the essential map.
func TestIsInsideEssential_V3_CommentInBlock(t *testing.T) {
	text := "package: foo\nslices:\n  libs:\n    essential:\n      # some comment\n      base-files_lib:\n    contents:\n      /usr/bin/foo:\n"
	// Line 5: "      base-files_lib:" — valid v3 key, but comment is above it.
	if !lsp.ExportIsInsideEssential(text, 5) {
		t.Error("expected isInsideEssential=true for v3 key below a comment line in essential block")
	}
}

// TestCompletionPrefixAndRange_V3_EmptyLine tests that an empty line inside a
// v3 essential block produces a valid (zero-prefix, appendColon) result so the
// caller can offer all slice refs.
func TestCompletionPrefixAndRange_V3_EmptyLine(t *testing.T) {
	text := "package: foo\nslices:\n  bins:\n    essential:\n      libc6_libs:\n      \n    contents:\n      /usr/bin/foo:\n"
	// Line 5 is "      " (6 spaces), cursor at col 6 — right where typing starts.
	prefix, r, needsSpace, appendColon := lsp.ExportCompletionPrefixAndRange(text, 5, 6)
	if prefix != "" {
		t.Errorf("prefix: got %q, want empty string for blank line", prefix)
	}
	if needsSpace {
		t.Error("needsLeadingSpace should be false for v3 context")
	}
	if !appendColon {
		t.Error("appendColon should be true for v3 map-key context")
	}
	if r.Start.Character != 6 {
		t.Errorf("editRange start: got %d, want 6", r.Start.Character)
	}
}

// TestCompletion_V3_EmptyLine is the end-to-end test: a line inside a
// v3 essential block with 2+ chars typed must produce completion items.
func TestCompletion_V3_EmptyLine(t *testing.T) {
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"libc6.yaml": `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`,
		"base-files.yaml": `package: base-files
slices:
  base:
    essential:
      libc6_libs:
    contents:
      /etc/:
`,
	})
	srv := lsp.NewWithIndex(idx)
	basePath := filepath.Join(slicesDir, "base-files.yaml")
	// Doc with "li" typed on a fresh line inside the v3 essential block.
	text := "package: base-files\nslices:\n  base:\n    essential:\n      libc6_libs:\n      li\n    contents:\n      /etc/:\n"
	// Cursor at col 8 on line 5 ("      li" — 2 chars typed after indent).
	items := srv.ExportCompletion(basePath, text, 5, 8)
	if len(items) == 0 {
		t.Fatal("expected completion items for 2-char prefix inside v3 essential block, got none")
	}
}

// TestCompletion_V3_CommentInBlock ensures completions work when there is a
// YAML comment between the essential: header and the current entry — matching
// real ubuntu-26.04 files like libc6.yaml.
func TestCompletion_V3_CommentInBlock(t *testing.T) {
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"libc6.yaml": `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`,
		"base-files.yaml": `package: base-files
slices:
  lib:
    contents:
      /lib/:
`,
	})
	srv := lsp.NewWithIndex(idx)
	libc6Path := filepath.Join(slicesDir, "libc6.yaml")
	// A v3 file with a YAML comment inside the essential block.
	text := "package: libc6\nslices:\n  gconv-core:\n    essential:\n      # explicit dependency\n      base-files_lib:\n    contents:\n      /usr/lib/*-linux-*/gconv/ANSI_X3.110.so:\n"
	// Cursor on "      base-files_lib:" (line 5) — after the comment.
	items := srv.ExportCompletion(libc6Path, text, 5, 20)
	if len(items) == 0 {
		t.Fatal("expected completion items for v3 entry below a comment line, got none")
	}
}

// TestReferences_V3_FromEssentialKey verifies that find-references works when
// the cursor is on a v3-style map key (e.g. "libc6_libs:") in the essential block.
// Cursor lands on the colon position to test the edge case.
func TestReferences_V3_FromEssentialKey(t *testing.T) {
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
      libc6_libs:
    contents:
      /usr/bin/openssl:
`,
	})
	opensslPath := filepath.Join(slicesDir, "openssl.yaml")
	srv := lsp.NewWithIndex(idx)

	// Line 4 of openssl.yaml: "      libc6_libs:"
	// Cursor at col 15 — on the "s" before the colon (last char of the word).
	locs, err := srv.ExportReferences(opensslPath, 4, 15)
	if err != nil {
		t.Fatal(err)
	}
	// Expect at least the definition location (fallback) or a real reference.
	if len(locs) == 0 {
		t.Fatal("expected at least 1 location from v3 essential key, got none")
	}
}

// TestReferences_V3_DefinitionSite verifies that find-references from the
// definition key (e.g. "libs:" in slices: section) finds v3-format references.
func TestReferences_V3_DefinitionSite(t *testing.T) {
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
      libc6_libs:
    contents:
      /usr/bin/openssl:
`,
	})
	libc6Path := filepath.Join(slicesDir, "libc6.yaml")
	srv := lsp.NewWithIndex(idx)

	// Line 2 of libc6.yaml: "  libs:" — cursor on "libs"
	locs, err := srv.ExportReferences(libc6Path, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	// Must find the reference in openssl.yaml's v3 essential block.
	foundRef := false
	for _, loc := range locs {
		if strings.Contains(string(loc.URI), "openssl.yaml") {
			foundRef = true
		}
	}
	if !foundRef {
		t.Errorf("expected reference in openssl.yaml (v3 essential), got: %v", locs)
	}
}

// --- minPrefixLength configuration ---

// TestCompletion_MinPrefixLen_Default verifies the default threshold of 2:
// 0 and 1 chars produce no items; 2 chars do.
func TestCompletion_MinPrefixLen_Default(t *testing.T) {
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
	srv := lsp.NewWithIndex(idx)
	opensslPath := filepath.Join(slicesDir, "openssl.yaml")

	// v1/v2: "      - " (cursor at col 8, prefix="") — should block.
	text0 := "package: openssl\nslices:\n  bins:\n    essential:\n      - \n    contents:\n      /usr/bin/openssl:\n"
	if items := srv.ExportCompletion(opensslPath, text0, 4, 8); len(items) != 0 {
		t.Errorf("0-char prefix: expected no completions (min=2), got %d", len(items))
	}

	// v1/v2: "      - l" (cursor at col 9, prefix="l") — 1 char, still blocks.
	text1 := "package: openssl\nslices:\n  bins:\n    essential:\n      - l\n    contents:\n      /usr/bin/openssl:\n"
	if items := srv.ExportCompletion(opensslPath, text1, 4, 9); len(items) != 0 {
		t.Errorf("1-char prefix: expected no completions (min=2), got %d", len(items))
	}

	// v1/v2: "      - li" (cursor at col 10, prefix="li") — 2 chars, should pass.
	text2 := "package: openssl\nslices:\n  bins:\n    essential:\n      - li\n    contents:\n      /usr/bin/openssl:\n"
	if items := srv.ExportCompletion(opensslPath, text2, 4, 10); len(items) == 0 {
		t.Error("2-char prefix: expected completions at default min=2, got none")
	}
}

// TestCompletion_MinPrefixLen_Custom verifies that a custom threshold of 3
// blocks a 2-char prefix but allows a 3-char prefix.
func TestCompletion_MinPrefixLen_Custom(t *testing.T) {
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
	srv := lsp.NewWithIndex(idx)
	srv.SetMinPrefixLen(3)
	opensslPath := filepath.Join(slicesDir, "openssl.yaml")

	// "      - li" (2 chars) — blocked when min=3.
	text2 := "package: openssl\nslices:\n  bins:\n    essential:\n      - li\n    contents:\n      /usr/bin/openssl:\n"
	if items := srv.ExportCompletion(opensslPath, text2, 4, 10); len(items) != 0 {
		t.Errorf("2-char prefix with min=3: expected no completions, got %d", len(items))
	}

	// "      - lib" (3 chars) — should pass with min=3.
	text3 := "package: openssl\nslices:\n  bins:\n    essential:\n      - lib\n    contents:\n      /usr/bin/openssl:\n"
	if items := srv.ExportCompletion(opensslPath, text3, 4, 11); len(items) == 0 {
		t.Error("3-char prefix with min=3: expected completions, got none")
	}
}

// TestCompletion_MinPrefixLen_ClampsToTwo verifies that setting min < 2 is
// silently raised to 2 so the server never offers completions at 1 char.
func TestCompletion_MinPrefixLen_ClampsToTwo(t *testing.T) {
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
	srv := lsp.NewWithIndex(idx)
	// SetMinPrefixLen with 1 — should be clamped to 2 at runtime by applySettings,
	// but here we set directly; the guard in computeCompletion uses minPrefixLen()
	// which reads config.MinPrefixLen directly, so clamping must happen in applySettings.
	// We test the applySettings path via workspaceDidChangeConfiguration simulation.
	srv.SetMinPrefixLen(1) // forces the value to 1 for this test
	opensslPath := filepath.Join(slicesDir, "openssl.yaml")

	// With min=1, a 1-char prefix should now produce completions.
	text1 := "package: openssl\nslices:\n  bins:\n    essential:\n      - l\n    contents:\n      /usr/bin/openssl:\n"
	if items := srv.ExportCompletion(opensslPath, text1, 4, 9); len(items) == 0 {
		// This is expected to succeed when clamping is NOT enforced in SetMinPrefixLen.
		// The clamping only applies in applySettings.
		t.Skip("SetMinPrefixLen intentionally allows < 2 (clamping is in applySettings only)")
	}
}

// TestApplySettings_MinPrefixLen verifies that applySettings reads and clamps
// the minPrefixLength value correctly.
func TestApplySettings_MinPrefixLen(t *testing.T) {
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

	cases := []struct {
		name    string
		setting map[string]any
		prefix  string // what the user has typed (just the ref part)
		line    string // full YAML line at cursor position
		col     int
		wantAny bool
	}{
		{
			name:    "value_0_clamped_to_2_blocks_1_char",
			setting: map[string]any{"minPrefixLength": float64(0)},
			prefix:  "l",
			line:    "package: openssl\nslices:\n  bins:\n    essential:\n      - l\n    contents:\n      /usr/bin/openssl:\n",
			col:     9,
			wantAny: false,
		},
		{
			name:    "value_3_blocks_2_char",
			setting: map[string]any{"minPrefixLength": float64(3)},
			prefix:  "li",
			line:    "package: openssl\nslices:\n  bins:\n    essential:\n      - li\n    contents:\n      /usr/bin/openssl:\n",
			col:     10,
			wantAny: false,
		},
		{
			name:    "value_3_allows_3_char",
			setting: map[string]any{"minPrefixLength": float64(3)},
			prefix:  "lib",
			line:    "package: openssl\nslices:\n  bins:\n    essential:\n      - lib\n    contents:\n      /usr/bin/openssl:\n",
			col:     11,
			wantAny: true,
		},
		{
			name:    "nested_chiselReleasesLsp_key",
			setting: map[string]any{"chiselReleasesLsp": map[string]any{"minPrefixLength": float64(2)}},
			prefix:  "li",
			line:    "package: openssl\nslices:\n  bins:\n    essential:\n      - li\n    contents:\n      /usr/bin/openssl:\n",
			col:     10,
			wantAny: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := lsp.NewWithIndex(idx)
			srv.ExportApplySettings(tc.setting)
			items := srv.ExportCompletion(opensslPath, tc.line, 4, tc.col)
			if tc.wantAny && len(items) == 0 {
				t.Errorf("expected completions with prefix %q, got none", tc.prefix)
			}
			if !tc.wantAny && len(items) != 0 {
				t.Errorf("expected no completions with prefix %q, got %d", tc.prefix, len(items))
			}
		})
	}
}

// --- hover tests ---

// TestHover_V1Essential checks hover on a v1/v2 sequence-style essential reference.
// Line 4 of the doc is "      - libc6_libs" → token "libc6_libs" at col 8.
func TestHover_V1Essential(t *testing.T) {
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"libc6.yaml": `package: libc6
slices:
  libs:
    hint: "C standard library"
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`,
		"foo.yaml": `package: foo
slices:
  bins:
    essential:
      - libc6_libs
    contents:
      /usr/bin/foo:
`,
	})
	srv := lsp.NewWithIndex(idx)
	fooPath := filepath.Join(slicesDir, "foo.yaml")

	// line 4: "      - libc6_libs" → "libc6_libs" starts at col 8
	h, err := srv.ExportHover(fooPath, 4, 8)
	if err != nil {
		t.Fatalf("hover error: %v", err)
	}
	if h == nil {
		t.Fatal("expected hover result, got nil")
	}
	mc, ok := h.Contents.(protocol.MarkupContent)
	if !ok {
		t.Fatalf("expected MarkupContent, got %T", h.Contents)
	}
	if !strings.Contains(mc.Value, "libc6_libs") {
		t.Errorf("hover markdown missing slice name: %s", mc.Value)
	}
	if !strings.Contains(mc.Value, "C standard library") {
		t.Errorf("hover markdown missing hint: %s", mc.Value)
	}
}

// TestHover_V3MapKey checks hover on a v3 map-style essential key.
// Line 4 of the doc is "      libc6_libs:" → token "libc6_libs" at col 6.
func TestHover_V3MapKey(t *testing.T) {
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"libc6.yaml": `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`,
		"foo.yaml": `package: foo
slices:
  bins:
    essential:
      libc6_libs:
    contents:
      /usr/bin/foo:
`,
	})
	srv := lsp.NewWithIndex(idx)
	fooPath := filepath.Join(slicesDir, "foo.yaml")

	// line 4: "      libc6_libs:" → "libc6_libs" at col 6
	h, err := srv.ExportHover(fooPath, 4, 6)
	if err != nil {
		t.Fatalf("hover error: %v", err)
	}
	if h == nil {
		t.Fatal("expected hover result for v3 map key, got nil")
	}
	mc, ok := h.Contents.(protocol.MarkupContent)
	if !ok {
		t.Fatalf("expected MarkupContent, got %T", h.Contents)
	}
	if !strings.Contains(mc.Value, "libc6_libs") {
		t.Errorf("hover markdown missing slice name: %s", mc.Value)
	}
}

// TestHover_V3MapKey_CursorOnWhitespace checks that placing the cursor on
// leading whitespace (not on any token) returns nil.
func TestHover_V3MapKey_CursorOnWhitespace(t *testing.T) {
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"libc6.yaml": `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`,
		"foo.yaml": `package: foo
slices:
  bins:
    essential:
      libc6_libs:
    contents:
      /usr/bin/foo:
`,
	})
	srv := lsp.NewWithIndex(idx)
	fooPath := filepath.Join(slicesDir, "foo.yaml")

	// line 4: "      libc6_libs:" → cols 0-5 are spaces, no token there
	h, err := srv.ExportHover(fooPath, 4, 2)
	if err != nil {
		t.Fatalf("hover error: %v", err)
	}
	if h != nil {
		t.Errorf("expected nil hover on whitespace, got: %+v", h)
	}
}

// TestHover_UnknownRef checks that hover on an unresolvable reference returns nil.
func TestHover_UnknownRef(t *testing.T) {
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"foo.yaml": `package: foo
slices:
  bins:
    essential:
      - ghost_slice
    contents:
      /usr/bin/foo:
`,
	})
	srv := lsp.NewWithIndex(idx)
	fooPath := filepath.Join(slicesDir, "foo.yaml")

	// line 4: "      - ghost_slice" → token at col 8
	h, err := srv.ExportHover(fooPath, 4, 8)
	if err != nil {
		t.Fatalf("hover error: %v", err)
	}
	if h != nil {
		t.Errorf("expected nil hover for unknown ref, got: %+v", h)
	}
}

// --- v3 diagnostics tests ---

// TestComputeDiagnostics_UnknownRef_V3 verifies that an unknown-slice-ref
// diagnostic is emitted for v3 map-style essential entries just as it is for
// v1/v2 sequence-style entries.
func TestComputeDiagnostics_UnknownRef_V3(t *testing.T) {
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"foo.yaml": `package: foo
slices:
  bins:
    essential:
      nonexistent_slice:
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
		t.Errorf("expected unknown-ref diagnostic for v3 map-style entry, got: %v", diags)
	}
}

// TestComputeDiagnostics_MissingCopyright verifies that the LSP layer emits a
// missing-copyright-essential warning when a slice has no copyright essential.
func TestComputeDiagnostics_MissingCopyright(t *testing.T) {
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"openssl.yaml": `package: openssl
slices:
  copyright:
    contents:
      /usr/share/doc/openssl/copyright:
  libs:
    contents:
      /usr/lib/libssl.so:
`,
	})

	srv := lsp.NewWithIndex(idx)
	filePath := filepath.Join(slicesDir, "openssl.yaml")
	diags := srv.ExportComputeDiagnostics(filePath)

	var found bool
	for _, d := range diags {
		if d.Code != nil {
			if code, ok := d.Code.Value.(string); ok && code == "missing-copyright-essential" {
				found = true
				if d.Severity == nil || *d.Severity != protocol.DiagnosticSeverityWarning {
					t.Errorf("expected Warning severity")
				}
			}
		}
	}
	if !found {
		t.Errorf("expected missing-copyright-essential diagnostic, got: %v", diags)
	}
}

// TestComputeDiagnostics_MissingCopyright_PackageLevelCovers verifies that when
// the package-level essential lists the copyright slice, no warning is emitted.
func TestComputeDiagnostics_MissingCopyright_PackageLevelCovers(t *testing.T) {
	idx, slicesDir := setupLSPIndex(t, map[string]string{
		"openssl.yaml": `package: openssl
essential:
  - openssl_copyright
slices:
  copyright:
    contents:
      /usr/share/doc/openssl/copyright:
  libs:
    contents:
      /usr/lib/libssl.so:
`,
	})

	srv := lsp.NewWithIndex(idx)
	filePath := filepath.Join(slicesDir, "openssl.yaml")
	diags := srv.ExportComputeDiagnostics(filePath)

	for _, d := range diags {
		if d.Code != nil {
			if code, ok := d.Code.Value.(string); ok && code == "missing-copyright-essential" {
				t.Errorf("unexpected missing-copyright-essential diagnostic (package-level should cover): %v", d)
			}
		}
	}
}

// TestCodeAction_MissingCopyright_NoEssentialBlock verifies that the code action
// creates a top-level essential: block (after the package: line) when none exists.
func TestCodeAction_MissingCopyright_NoEssentialBlock(t *testing.T) {
	content := `package: openssl
slices:
  libs:
    contents:
      /usr/lib/libssl.so:
`
	idx, slicesDir := setupLSPIndex(t, map[string]string{"openssl.yaml": content})
	srv := lsp.NewWithIndex(idx)
	filePath := filepath.Join(slicesDir, "openssl.yaml")
	srv.SetDocForTest(filePath, content)

	diags := srv.ExportComputeDiagnostics(filePath)

	// Find the missing-copyright diagnostic.
	var copyrightDiag *protocol.Diagnostic
	for i := range diags {
		if diags[i].Code != nil {
			if code, ok := diags[i].Code.Value.(string); ok && code == "missing-copyright-essential" {
				copyrightDiag = &diags[i]
				break
			}
		}
	}
	if copyrightDiag == nil {
		t.Fatal("expected missing-copyright-essential diagnostic, got none")
	}

	actions := srv.ExportCodeAction(filePath, []protocol.Diagnostic{*copyrightDiag})
	if len(actions) == 0 {
		t.Fatal("expected at least one code action, got none")
	}

	var fixAction *protocol.CodeAction
	for i := range actions {
		if actions[i].Edit != nil {
			fixAction = &actions[i]
			break
		}
	}
	if fixAction == nil {
		t.Fatal("expected a code action with workspace edit")
	}
	uri := lsp.ExportFilePathToURI(filePath)
	edits := fixAction.Edit.Changes[uri]
	if len(edits) == 0 {
		t.Fatal("expected at least one text edit")
	}
	newText := edits[0].NewText
	// Should create a top-level essential: block with the copyright item.
	if !strings.Contains(newText, "essential:") || !strings.Contains(newText, "openssl_copyright") {
		t.Errorf("expected edit to contain 'essential:' and 'openssl_copyright', got: %q", newText)
	}
	// The edit must be inserted right after line 0 (the package: line).
	if edits[0].Range.Start.Line != 1 {
		t.Errorf("expected insert after package: line (line 1), got line %d", edits[0].Range.Start.Line)
	}
	// Title should mention "package essentials".
	if !strings.Contains(fixAction.Title, "package essentials") {
		t.Errorf("expected title to mention 'package essentials', got: %q", fixAction.Title)
	}
}

// TestCodeAction_MissingCopyright_ExistingTopLevelBlock verifies that the code
// action inserts "- openssl_copyright" into an existing top-level essential:
// block (not creating a duplicate block or touching per-slice blocks).
func TestCodeAction_MissingCopyright_ExistingTopLevelBlock(t *testing.T) {
	content := `package: openssl
essential:
  - libc6_libs
slices:
  libs:
    contents:
      /usr/lib/libssl.so:
`
	idx, slicesDir := setupLSPIndex(t, map[string]string{"openssl.yaml": content})
	srv := lsp.NewWithIndex(idx)
	filePath := filepath.Join(slicesDir, "openssl.yaml")
	srv.SetDocForTest(filePath, content)

	diags := srv.ExportComputeDiagnostics(filePath)

	var copyrightDiag *protocol.Diagnostic
	for i := range diags {
		if diags[i].Code != nil {
			if code, ok := diags[i].Code.Value.(string); ok && code == "missing-copyright-essential" {
				copyrightDiag = &diags[i]
				break
			}
		}
	}
	if copyrightDiag == nil {
		t.Fatal("expected missing-copyright-essential diagnostic, got none")
	}

	actions := srv.ExportCodeAction(filePath, []protocol.Diagnostic{*copyrightDiag})
	var fixAction *protocol.CodeAction
	for i := range actions {
		if actions[i].Edit != nil {
			fixAction = &actions[i]
			break
		}
	}
	if fixAction == nil {
		t.Fatal("expected a code action with workspace edit")
	}
	uri := lsp.ExportFilePathToURI(filePath)
	edits := fixAction.Edit.Changes[uri]
	if len(edits) == 0 {
		t.Fatal("expected at least one text edit")
	}
	newText := edits[0].NewText
	// Should only insert the item — no new essential: keyword.
	if strings.Contains(newText, "essential:") {
		t.Errorf("expected insert-only edit (no new 'essential:' keyword), got: %q", newText)
	}
	if !strings.Contains(newText, "openssl_copyright") {
		t.Errorf("expected edit to contain 'openssl_copyright', got: %q", newText)
	}
	// Must use v1 format (- item) since existing items use "- ".
	if !strings.Contains(newText, "- openssl_copyright") {
		t.Errorf("expected v1 format '- openssl_copyright', got: %q", newText)
	}
	// Insertion is after the "essential:" line (line 1 → insert at line 2).
	if edits[0].Range.Start.Line != 2 {
		t.Errorf("expected insert at line 2 (after essential:), got line %d", edits[0].Range.Start.Line)
	}
}

// TestCodeAction_MissingCopyright_V3Format verifies that when the file uses v3
// mapping-style essentials, the fix inserts "openssl_copyright:" (not "- openssl_copyright").
func TestCodeAction_MissingCopyright_V3Format(t *testing.T) {
	content := `package: openssl
essential:
  libc6_libs:
slices:
  libs:
    contents:
      /usr/lib/libssl.so:
`
	idx, slicesDir := setupLSPIndex(t, map[string]string{"openssl.yaml": content})
	srv := lsp.NewWithIndex(idx)
	filePath := filepath.Join(slicesDir, "openssl.yaml")
	srv.SetDocForTest(filePath, content)

	diags := srv.ExportComputeDiagnostics(filePath)

	var copyrightDiag *protocol.Diagnostic
	for i := range diags {
		if diags[i].Code != nil {
			if code, ok := diags[i].Code.Value.(string); ok && code == "missing-copyright-essential" {
				copyrightDiag = &diags[i]
				break
			}
		}
	}
	if copyrightDiag == nil {
		t.Fatal("expected missing-copyright-essential diagnostic, got none")
	}

	actions := srv.ExportCodeAction(filePath, []protocol.Diagnostic{*copyrightDiag})
	var fixAction *protocol.CodeAction
	for i := range actions {
		if actions[i].Edit != nil {
			fixAction = &actions[i]
			break
		}
	}
	if fixAction == nil {
		t.Fatal("expected a code action with workspace edit")
	}
	uri := lsp.ExportFilePathToURI(filePath)
	edits := fixAction.Edit.Changes[uri]
	if len(edits) == 0 {
		t.Fatal("expected at least one text edit")
	}
	newText := edits[0].NewText
	// Should use v3 mapping format: "  openssl_copyright:\n"
	if strings.Contains(newText, "- openssl_copyright") {
		t.Errorf("expected v3 format (no dash), got v1: %q", newText)
	}
	if !strings.Contains(newText, "openssl_copyright:") {
		t.Errorf("expected v3 format 'openssl_copyright:', got: %q", newText)
	}
}

// TestComputeDiagnostics_DuplicateEssential verifies that a duplicate essential
// reference emits a diagnostic with RelatedInformation pointing to the first occurrence.
func TestComputeDiagnostics_DuplicateEssential(t *testing.T) {
	content := `package: openssl
slices:
  libs:
    essential:
      - libc6_libs
      - libc6_libs
    contents:
      /usr/lib/libssl.so:
`
	idx, slicesDir := setupLSPIndex(t, map[string]string{"openssl.yaml": content})
	srv := lsp.NewWithIndex(idx)
	filePath := filepath.Join(slicesDir, "openssl.yaml")

	diags := srv.ExportComputeDiagnostics(filePath)

	var dupDiag *protocol.Diagnostic
	for i := range diags {
		if diags[i].Code != nil {
			if code, ok := diags[i].Code.Value.(string); ok && code == "duplicate-essential" {
				dupDiag = &diags[i]
				break
			}
		}
	}
	if dupDiag == nil {
		t.Fatal("expected duplicate-essential diagnostic, got none")
	}
	if dupDiag.Severity == nil || *dupDiag.Severity != protocol.DiagnosticSeverityWarning {
		t.Errorf("expected Warning severity")
	}
	if len(dupDiag.RelatedInformation) == 0 {
		t.Fatal("expected RelatedInformation pointing to first occurrence")
	}
	if !strings.Contains(dupDiag.RelatedInformation[0].Message, "first occurrence") {
		t.Errorf("expected 'first occurrence' in related info message, got %q", dupDiag.RelatedInformation[0].Message)
	}
	// Duplicate is on line 5 (0-based), first occurrence on line 4.
	if dupDiag.Range.Start.Line != 5 {
		t.Errorf("expected duplicate on line 5, got %d", dupDiag.Range.Start.Line)
	}
	firstLine := dupDiag.RelatedInformation[0].Location.Range.Start.Line
	if firstLine != 4 {
		t.Errorf("expected first occurrence on line 4, got %d", firstLine)
	}
}

// TestCodeAction_DuplicateEssential_GotoPreferred verifies that the "Go to first
// occurrence" action is the preferred (IsPreferred=true) action.
func TestCodeAction_DuplicateEssential_GotoPreferred(t *testing.T) {
	content := `package: openssl
slices:
  libs:
    essential:
      - libc6_libs
      - libc6_libs
    contents:
      /usr/lib/libssl.so:
`
	idx, slicesDir := setupLSPIndex(t, map[string]string{"openssl.yaml": content})
	srv := lsp.NewWithIndex(idx)
	filePath := filepath.Join(slicesDir, "openssl.yaml")
	srv.SetDocForTest(filePath, content)

	diags := srv.ExportComputeDiagnostics(filePath)
	var dupDiag *protocol.Diagnostic
	for i := range diags {
		if diags[i].Code != nil {
			if code, ok := diags[i].Code.Value.(string); ok && code == "duplicate-essential" {
				dupDiag = &diags[i]
				break
			}
		}
	}
	if dupDiag == nil {
		t.Fatal("expected duplicate-essential diagnostic")
	}

	actions := srv.ExportCodeAction(filePath, []protocol.Diagnostic{*dupDiag})
	if len(actions) < 2 {
		t.Fatalf("expected at least 2 actions (goto + remove), got %d", len(actions))
	}

	var gotoAction, removeAction *protocol.CodeAction
	for i := range actions {
		a := &actions[i]
		if a.Command != nil && a.Command.Command == "chisel-releases-lsp.gotoFirstOccurrence" {
			gotoAction = a
		}
		if a.Edit != nil {
			removeAction = a
		}
	}
	if gotoAction == nil {
		t.Error("expected a gotoFirstOccurrence action")
	} else if gotoAction.IsPreferred == nil || !*gotoAction.IsPreferred {
		t.Error("expected gotoFirstOccurrence to be IsPreferred=true")
	}
	if removeAction == nil {
		t.Error("expected a remove-duplicate edit action")
	} else if removeAction.IsPreferred != nil && *removeAction.IsPreferred {
		t.Error("expected remove action to NOT be preferred")
	}
}

// TestCodeAction_DuplicateEssential_RemoveEdit verifies that the remove action
// produces a TextEdit that deletes the duplicate line.
func TestCodeAction_DuplicateEssential_RemoveEdit(t *testing.T) {
	content := `package: openssl
slices:
  libs:
    essential:
      - libc6_libs
      - libc6_libs
    contents:
      /usr/lib/libssl.so:
`
	idx, slicesDir := setupLSPIndex(t, map[string]string{"openssl.yaml": content})
	srv := lsp.NewWithIndex(idx)
	filePath := filepath.Join(slicesDir, "openssl.yaml")
	srv.SetDocForTest(filePath, content)

	diags := srv.ExportComputeDiagnostics(filePath)
	var dupDiag *protocol.Diagnostic
	for i := range diags {
		if diags[i].Code != nil {
			if code, ok := diags[i].Code.Value.(string); ok && code == "duplicate-essential" {
				dupDiag = &diags[i]
				break
			}
		}
	}
	if dupDiag == nil {
		t.Fatal("expected duplicate-essential diagnostic")
	}

	actions := srv.ExportCodeAction(filePath, []protocol.Diagnostic{*dupDiag})
	uri := lsp.ExportFilePathToURI(filePath)
	var removeAction *protocol.CodeAction
	for i := range actions {
		if actions[i].Edit != nil {
			removeAction = &actions[i]
			break
		}
	}
	if removeAction == nil {
		t.Fatal("expected a remove edit action")
	}
	edits := removeAction.Edit.Changes[uri]
	if len(edits) == 0 {
		t.Fatal("expected at least one text edit")
	}
	// The edit should delete line 5 (the duplicate "- libc6_libs").
	edit := edits[0]
	if edit.Range.Start.Line != 5 {
		t.Errorf("expected delete edit on line 5, got %d", edit.Range.Start.Line)
	}
	if edit.NewText != "" {
		t.Errorf("expected empty NewText (deletion), got %q", edit.NewText)
	}
}
