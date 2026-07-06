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

// --- Format v3 and back-compat tests ---

const v3YAML = `package: base-files

essential:
  base-files_copyright:

slices:
  base:
    hint: "Base filesystem hierarchy"
    essential:
      base-files_bin:
      base-files_etc: {arch: amd64}
    contents:
      /etc/:
`

func TestParseBytes_V3MapEssential(t *testing.T) {
	sf, err := parser.ParseBytes([]byte(v3YAML))
	if err != nil {
		t.Fatalf("ParseBytes v3: %v", err)
	}

	if sf.Package != "base-files" {
		t.Errorf("package: got %q", sf.Package)
	}

	// Package-level essential: map with 1 key.
	if len(sf.Essential) != 1 || sf.Essential[0].Value != "base-files_copyright" {
		t.Errorf("top-level essential: %v", sf.Essential)
	}

	base := sf.Slices["base"]
	if base == nil {
		t.Fatal("base slice not found")
	}

	// Hint field.
	if base.Hint != "Base filesystem hierarchy" {
		t.Errorf("hint: got %q, want %q", base.Hint, "Base filesystem hierarchy")
	}

	// Slice-level essential: 2 map entries (arch value on second is ignored).
	if len(base.Essential) != 2 {
		t.Fatalf("base essential count: got %d, want 2", len(base.Essential))
	}
	vals := map[string]bool{}
	for _, e := range base.Essential {
		vals[e.Value] = true
	}
	if !vals["base-files_bin"] || !vals["base-files_etc"] {
		t.Errorf("unexpected essential values: %v", base.Essential)
	}
}

func TestParseBytes_V3EssentialRange(t *testing.T) {
	// Verify that ValueRange for a v3 map key points at the key token, not the colon.
	// v3YAML line 3 (0-based) is "  base-files_copyright:" — key at col 2.
	sf, err := parser.ParseBytes([]byte(v3YAML))
	if err != nil {
		t.Fatal(err)
	}
	ref := sf.Essential[0]
	if ref.ValueRange.Start.Line != 3 {
		t.Errorf("top-level essential ref line: got %d, want 3", ref.ValueRange.Start.Line)
	}
	if ref.ValueRange.Start.Character != 2 {
		t.Errorf("top-level essential ref col: got %d, want 2", ref.ValueRange.Start.Character)
	}
	// End should cover the full token "base-files_copyright" (20 chars).
	wantEnd := ref.ValueRange.Start.Character + len("base-files_copyright")
	if ref.ValueRange.End.Character != wantEnd {
		t.Errorf("top-level essential ref end col: got %d, want %d", ref.ValueRange.End.Character, wantEnd)
	}
}

const v3EssentialBackCompatYAML = `package: mypkg

essential:
  - mypkg_slice4

v3-essential:
  mypkg_slice4: {arch: [amd64, i386]}

slices:
  myslice1:
    essential:
      - mypkg_slice2
    v3-essential:
      mypkg_slice3: {arch: arm64}
    contents:
      /usr/bin/mybin:
`

func TestParseBytes_V3EssentialBackCompat(t *testing.T) {
	sf, err := parser.ParseBytes([]byte(v3EssentialBackCompatYAML))
	if err != nil {
		t.Fatalf("ParseBytes back-compat: %v", err)
	}

	// Package-level: essential list + v3-essential map merged.
	// Both "mypkg_slice4" entries are included (one from each field).
	if len(sf.Essential) != 2 {
		t.Fatalf("package-level essential count: got %d, want 2", len(sf.Essential))
	}
	for _, e := range sf.Essential {
		if e.Value != "mypkg_slice4" {
			t.Errorf("unexpected package essential value: %q", e.Value)
		}
	}

	myslice := sf.Slices["myslice1"]
	if myslice == nil {
		t.Fatal("myslice1 not found")
	}
	// Slice-level: essential list + v3-essential map merged.
	if len(myslice.Essential) != 2 {
		t.Fatalf("slice essential count: got %d, want 2", len(myslice.Essential))
	}
	vals := map[string]bool{}
	for _, e := range myslice.Essential {
		vals[e.Value] = true
	}
	if !vals["mypkg_slice2"] || !vals["mypkg_slice3"] {
		t.Errorf("unexpected slice essential values: %v", myslice.Essential)
	}
}

func TestParseContents_PreferInline(t *testing.T) {
	sf, err := parser.ParseBytes([]byte(`package: p
slices:
  s:
    contents:
      /usr/lib/libssl.so.3: {prefer: openssl}
      /usr/bin/foo:
`))
	if err != nil {
		t.Fatal(err)
	}
	contents := sf.Slices["s"].Contents
	if len(contents) != 2 {
		t.Fatalf("expected 2 contents entries, got %d", len(contents))
	}
	if contents[0].Prefer != "openssl" {
		t.Errorf("expected Prefer=openssl, got %q", contents[0].Prefer)
	}
	if contents[0].PreferRange.Start.Line == 0 && contents[0].PreferRange.End.Line == 0 {
		t.Error("PreferRange should be non-zero for inline prefer")
	}
	if contents[1].Prefer != "" {
		t.Errorf("expected empty Prefer for entry without prefer, got %q", contents[1].Prefer)
	}
}

func TestParseContents_PreferBlock(t *testing.T) {
	sf, err := parser.ParseBytes([]byte(`package: p
slices:
  s:
    contents:
      /usr/lib/libssl.so.3:
        prefer: openssl
`))
	if err != nil {
		t.Fatal(err)
	}
	contents := sf.Slices["s"].Contents
	if len(contents) != 1 {
		t.Fatalf("expected 1 contents entry, got %d", len(contents))
	}
	if contents[0].Prefer != "openssl" {
		t.Errorf("expected Prefer=openssl, got %q", contents[0].Prefer)
	}
	// PreferRange should point at the value "openssl", not the key "prefer".
	if contents[0].PreferRange.Start.Character == 0 {
		t.Error("PreferRange should not start at column 0 (should point at the value)")
	}
}
