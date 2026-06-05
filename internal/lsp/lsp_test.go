package lsp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/canonical/chisel-releases-lsp/internal/index"
	"github.com/canonical/chisel-releases-lsp/internal/lsp"
	"github.com/canonical/chisel-releases-lsp/internal/parser"
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
