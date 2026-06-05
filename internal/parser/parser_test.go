package parser_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dariuszd21/chisel-releases-lsp/internal/parser"
)

const sampleYAML = `
package: openssl

essential:
  - openssl_copyright

slices:
  bins:
    essential:
      - libc6_libs
      - libssl3t64_libs
    contents:
      /usr/bin/openssl:
      /usr/bin/c_rehash:

  config:
    contents:
      /etc/ssl/openssl.cnf:
      /etc/ssl/certs/:
`

func TestParseBytes(t *testing.T) {
	sf, err := parser.ParseBytes([]byte(sampleYAML))
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}

	if sf.Package != "openssl" {
		t.Errorf("Package: got %q, want %q", sf.Package, "openssl")
	}

	if len(sf.Essential) != 1 || sf.Essential[0].Value != "openssl_copyright" {
		t.Errorf("top-level essential: %v", sf.Essential)
	}

	if len(sf.Slices) != 2 {
		t.Errorf("slice count: got %d, want 2", len(sf.Slices))
	}

	bins := sf.Slices["bins"]
	if bins == nil {
		t.Fatal("bins slice not found")
	}
	if len(bins.Essential) != 2 {
		t.Errorf("bins essential count: got %d, want 2", len(bins.Essential))
	}
	if len(bins.Contents) != 2 {
		t.Errorf("bins contents count: got %d, want 2", len(bins.Contents))
	}

	// Verify exact 0-based positions.
	// sampleYAML starts with a blank line, so:
	//   line 1: "package: openssl"  → "openssl" starts at col 9
	//   line 3: "essential:"
	//   line 4: "  - openssl_copyright"  → value at col 4
	//   line 6: "slices:"
	//   line 7: "  bins:"  → "bins" key at col 2
	//   line 8: "    essential:"
	//   line 9: "      - libc6_libs"  → value at col 8

	if sf.PackageRange.Start.Line != 1 {
		t.Errorf("package line: got %d, want 1", sf.PackageRange.Start.Line)
	}
	if sf.PackageRange.Start.Character != 9 {
		t.Errorf("package col: got %d, want 9", sf.PackageRange.Start.Character)
	}

	topRef := sf.Essential[0]
	if topRef.ValueRange.Start.Line != 4 {
		t.Errorf("top essential ref line: got %d, want 4", topRef.ValueRange.Start.Line)
	}

	if bins.NameRange.Start.Line != 7 {
		t.Errorf("bins name line: got %d, want 7", bins.NameRange.Start.Line)
	}
	if bins.NameRange.Start.Character != 2 {
		t.Errorf("bins name col: got %d, want 2", bins.NameRange.Start.Character)
	}

	libsRef := bins.Essential[0]
	if libsRef.ValueRange.Start.Line != 9 {
		t.Errorf("libc6_libs ref line: got %d, want 9", libsRef.ValueRange.Start.Line)
	}

	opensslBin := bins.Contents[0]
	if opensslBin.Path != "/usr/bin/openssl" {
		t.Errorf("first content path: got %q", opensslBin.Path)
	}
	if opensslBin.PathRange.Start.Line != 12 {
		t.Errorf("openssl path line: got %d, want 12", opensslBin.PathRange.Start.Line)
	}
}

func TestParseBytes_Empty(t *testing.T) {
	sf, err := parser.ParseBytes([]byte(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sf == nil || sf.Slices == nil {
		t.Fatal("nil SliceFile for empty document")
	}
}

func TestParseBytes_InvalidYAML(t *testing.T) {
	_, err := parser.ParseBytes([]byte(":\tbroken:yaml::"))
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestParseBytes_NonMappingRoot(t *testing.T) {
	_, err := parser.ParseBytes([]byte("- list\n- items\n"))
	if err == nil {
		t.Fatal("expected error for non-mapping root")
	}
}

func TestParseFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	if err := os.WriteFile(path, []byte(sampleYAML), 0644); err != nil {
		t.Fatal(err)
	}
	sf, err := parser.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if sf.Package != "openssl" {
		t.Errorf("Package: got %q", sf.Package)
	}
}

func TestParseFile_Missing(t *testing.T) {
	_, err := parser.ParseFile("/nonexistent/path/file.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestSliceRefFromToken(t *testing.T) {
	cases := []struct{ token, pkg, slice string }{
		{"libc6_libs", "libc6", "libs"},
		{"openssl_copyright", "openssl", "copyright"},
		{"linux-libc-dev_headers", "linux-libc-dev", "headers"},
		{"nounderscore", "", ""},
		{"_leading", "", ""},
		{"trailing_", "", ""},
	}
	for _, c := range cases {
		pkg, slice := parser.SliceRefFromToken(c.token)
		if pkg != c.pkg || slice != c.slice {
			t.Errorf("SliceRefFromToken(%q): got (%q,%q), want (%q,%q)", c.token, pkg, slice, c.pkg, c.slice)
		}
	}
}
