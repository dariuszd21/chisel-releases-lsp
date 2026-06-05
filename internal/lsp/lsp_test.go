package lsp_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/chisel-releases-lsp/internal/index"
	"github.com/canonical/chisel-releases-lsp/internal/lsp"
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

func TestRenderSliceMarkdown(t *testing.T) {
	dir := t.TempDir()
	slicesDir := filepath.Join(dir, "slices")
	os.Mkdir(slicesDir, 0755)
	os.WriteFile(filepath.Join(slicesDir, "libc6.yaml"), []byte(`
package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`), 0644)

	idx, err := index.New(dir, nil)
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
	if !containsStr(md, "libc6_libs") {
		t.Errorf("markdown missing slice name: %q", md)
	}
	if !containsStr(md, "/lib/x86_64-linux-gnu/libc.so.6") {
		t.Errorf("markdown missing content path: %q", md)
	}
}

func containsStr(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}
