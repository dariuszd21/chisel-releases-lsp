package analysis_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dariuszd21/chisel-releases-lsp/internal/analysis"
	"github.com/dariuszd21/chisel-releases-lsp/internal/index"
	"github.com/dariuszd21/chisel-releases-lsp/internal/parser"
)

func TestValidateGlobs(t *testing.T) {
	cases := []struct {
		yaml    string
		wantErr bool
		msgFrag string
	}{
		{
			yaml: `package: p
slices:
  s:
    contents:
      /usr/bin/foo:
`,
			wantErr: false,
		},
		{
			yaml: `package: p
slices:
  s:
    contents:
      /usr/bin/*:
`,
			wantErr: false,
		},
		{
			yaml: `package: p
slices:
  s:
    contents:
      /usr/bin/**:
`,
			wantErr: false,
		},
		{
			yaml: `package: p
slices:
  s:
    contents:
      relative/path:
`,
			wantErr: true,
			msgFrag: "absolute",
		},
		{
			yaml: `package: p
slices:
  s:
    contents:
      /usr/bin/foo[bar:
`,
			wantErr: true,
			msgFrag: "invalid glob",
		},
	}

	for _, c := range cases {
		sf, err := parser.ParseBytes([]byte(c.yaml))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		diags := analysis.ValidateGlobs("test.yaml", sf)
		if c.wantErr && len(diags) == 0 {
			t.Errorf("expected diagnostic for yaml:\n%s", c.yaml)
		}
		if !c.wantErr && len(diags) != 0 {
			t.Errorf("unexpected diagnostics for yaml:\n%s\ngot: %v", c.yaml, diags)
		}
		if c.wantErr && len(diags) > 0 && c.msgFrag != "" {
			found := false
			for _, d := range diags {
				if strings.Contains(d.Message, c.msgFrag) {
					found = true
				}
			}
			if !found {
				t.Errorf("expected message fragment %q in diagnostics, got: %v", c.msgFrag, diags)
			}
		}
	}
}

func TestValidateGlobs_StarStarMidSegment(t *testing.T) {
	yaml := `package: p
slices:
  s:
    contents:
      /foo**/bar:
`
	sf, err := parser.ParseBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	diags := analysis.ValidateGlobs("test.yaml", sf)
	if len(diags) == 0 {
		t.Error("expected diagnostic for mid-segment **, got none")
	}
}

func TestValidateGlobs_StarStarInPath(t *testing.T) {
	// /dir/**/file is valid — ** is a complete path segment.
	yaml := `package: p
slices:
  s:
    contents:
      /usr/share/**/README:
`
	sf, err := parser.ParseBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	diags := analysis.ValidateGlobs("test.yaml", sf)
	if len(diags) != 0 {
		t.Errorf("/usr/share/**/README should be valid, got diagnostics: %v", diags)
	}
}

func TestValidateGlobs_StarStarWithSuffix(t *testing.T) {
	// **.so in a segment is invalid — ** is mixed with other characters.
	yaml := `package: p
slices:
  s:
    contents:
      /usr/lib/**.so:
`
	sf, err := parser.ParseBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	diags := analysis.ValidateGlobs("test.yaml", sf)
	if len(diags) == 0 {
		t.Error("expected diagnostic for **.so segment, got none")
	}
}

func setupCollisionIndex(t *testing.T, files map[string]string) *index.Index {
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
	return idx
}

func TestDetectCollisions(t *testing.T) {
	idx := setupCollisionIndex(t, map[string]string{
		"pkga.yaml": `package: pkga
slices:
  s:
    contents:
      /shared/path:
      /only/a:
`,
		"pkgb.yaml": `package: pkgb
slices:
  s:
    contents:
      /shared/path:
      /only/b:
`,
	})

	collisions := analysis.DetectCollisions(idx)
	if len(collisions) != 1 {
		t.Fatalf("expected 1 collision, got %d: %+v", len(collisions), collisions)
	}
	if collisions[0].Path != "/shared/path" {
		t.Errorf("collision path: %q", collisions[0].Path)
	}
}

func TestDetectCollisions_SamePackageNoCollision(t *testing.T) {
	// Same path in two slices of the same package is allowed.
	idx := setupCollisionIndex(t, map[string]string{
		"pkga.yaml": `package: pkga
slices:
  s1:
    contents:
      /shared/path:
  s2:
    contents:
      /shared/path:
`,
	})

	collisions := analysis.DetectCollisions(idx)
	if len(collisions) != 0 {
		t.Errorf("expected no collisions for same-package paths, got: %+v", collisions)
	}
}

func TestDetectCollisions_GlobNotCollision(t *testing.T) {
	// Glob paths must not trigger collision detection.
	idx := setupCollisionIndex(t, map[string]string{
		"pkga.yaml": `package: pkga
slices:
  s:
    contents:
      /usr/bin/*:
`,
		"pkgb.yaml": `package: pkgb
slices:
  s:
    contents:
      /usr/bin/*:
`,
	})

	collisions := analysis.DetectCollisions(idx)
	if len(collisions) != 0 {
		t.Errorf("expected no collisions for glob paths, got: %+v", collisions)
	}
}

// --- CheckPackageName ---

func mustParseYAML(t *testing.T, yaml string) *parser.SliceFile {
	t.Helper()
	sf, err := parser.ParseBytes([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	return sf
}

func TestCheckPackageName_Match(t *testing.T) {
	sf := mustParseYAML(t, `package: openssl
slices:
  bins:
    contents:
      /usr/bin/openssl:
`)
	d := analysis.CheckPackageName("/releases/slices/openssl.yaml", sf)
	if d != nil {
		t.Errorf("expected no diagnostic for matching name, got: %v", d)
	}
}

func TestCheckPackageName_Mismatch(t *testing.T) {
	sf := mustParseYAML(t, `package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`)
	d := analysis.CheckPackageName("/releases/slices/openssl.yaml", sf)
	if d == nil {
		t.Fatal("expected diagnostic for mismatched package name, got nil")
	}
	if !strings.Contains(d.Message, "libc6") || !strings.Contains(d.Message, "openssl.yaml") {
		t.Errorf("diagnostic message should mention both names, got: %q", d.Message)
	}
	if d.Severity != analysis.SeverityWarning {
		t.Errorf("expected Warning severity, got %v", d.Severity)
	}
}

func TestCheckPackageName_EmptyPackage(t *testing.T) {
	sf := mustParseYAML(t, `slices:
  bins:
    contents:
      /usr/bin/foo:
`)
	d := analysis.CheckPackageName("/releases/slices/foo.yaml", sf)
	if d != nil {
		t.Errorf("expected no diagnostic when package is empty, got: %v", d)
	}
}

// --- DetectDuplicateSlices ---

func setupIndex(t *testing.T, files map[string]string) (*index.Index, string) {
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

func TestDetectDuplicateSlices_NoDuplicates(t *testing.T) {
	idx, _ := setupIndex(t, map[string]string{
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
	dups := analysis.DetectDuplicateSlices(idx)
	if len(dups) != 0 {
		t.Errorf("expected no duplicates, got: %v", dups)
	}
}

func TestDetectDuplicateSlices_Detected(t *testing.T) {
	// Two files with the same package name and overlapping slice names.
	// (This would also trigger CheckPackageName on one of them.)
	idx, slicesDir := setupIndex(t, map[string]string{
		"openssl.yaml": `package: openssl
slices:
  bins:
    contents:
      /usr/bin/openssl:
  config:
    contents:
      /etc/ssl/openssl.cnf:
`,
		"openssl-extra.yaml": `package: openssl
slices:
  bins:
    contents:
      /usr/bin/c_rehash:
`,
	})

	dups := analysis.DetectDuplicateSlices(idx)
	if len(dups) != 1 {
		t.Fatalf("expected 1 duplicate, got %d: %v", len(dups), dups)
	}
	d := dups[0]
	if d.Pkg != "openssl" || d.SliceName != "bins" {
		t.Errorf("expected openssl:bins duplicate, got %q:%q", d.Pkg, d.SliceName)
	}
	// Both files must be represented.
	files := map[string]bool{d.File1: true, d.File2: true}
	if !files[filepath.Join(slicesDir, "openssl.yaml")] || !files[filepath.Join(slicesDir, "openssl-extra.yaml")] {
		t.Errorf("duplicate files: got %q and %q", d.File1, d.File2)
	}
	// Ranges must be non-zero (pointing at the slice name key).
	if d.Range1.Start.Line == 0 && d.Range1.Start.Character == 0 &&
		d.Range1.End.Line == 0 && d.Range1.End.Character == 0 {
		t.Error("Range1 is zero — expected it to point at the slice name key")
	}
}

func TestDetectDuplicateSlices_DeterministicOrder(t *testing.T) {
	// Multiple duplicates should be sorted by Pkg then SliceName.
	idx, _ := setupIndex(t, map[string]string{
		"a.yaml": `package: a
slices:
  z:
    contents:
      /usr/z:
  a:
    contents:
      /usr/a:
`,
		"b.yaml": `package: a
slices:
  z:
    contents:
      /usr/z2:
  a:
    contents:
      /usr/a2:
`,
	})
	dups := analysis.DetectDuplicateSlices(idx)
	if len(dups) != 2 {
		t.Fatalf("expected 2 duplicates, got %d: %v", len(dups), dups)
	}
	if dups[0].SliceName > dups[1].SliceName {
		t.Errorf("expected sorted by SliceName, got [%s, %s]", dups[0].SliceName, dups[1].SliceName)
	}
}

func TestCheckCopyrightEssential_MissingInSlice(t *testing.T) {
	sf, err := parser.ParseBytes([]byte(`package: openssl
slices:
  libs:
    contents:
      /usr/lib/libssl.so:
  bins:
    essential:
      - libc6_libs
    contents:
      /usr/bin/openssl:
`))
	if err != nil {
		t.Fatal(err)
	}
	diags := analysis.CheckCopyrightEssential("openssl.yaml", sf)
	// Both libs and bins are missing openssl_copyright
	if len(diags) != 2 {
		t.Fatalf("expected 2 diagnostics, got %d: %v", len(diags), diags)
	}
	for _, d := range diags {
		if !strings.Contains(d.Message, "openssl_copyright") {
			t.Errorf("unexpected message: %s", d.Message)
		}
		if d.Severity != analysis.SeverityWarning {
			t.Errorf("expected Warning severity, got %v", d.Severity)
		}
	}
}

func TestCheckCopyrightEssential_SliceLevelCoversIt(t *testing.T) {
	sf, err := parser.ParseBytes([]byte(`package: openssl
slices:
  libs:
    essential:
      - openssl_copyright
    contents:
      /usr/lib/libssl.so:
`))
	if err != nil {
		t.Fatal(err)
	}
	diags := analysis.CheckCopyrightEssential("openssl.yaml", sf)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %d: %v", len(diags), diags)
	}
}

func TestCheckCopyrightEssential_PackageLevelCoversAll(t *testing.T) {
	sf, err := parser.ParseBytes([]byte(`package: openssl
essential:
  - openssl_copyright
slices:
  libs:
    contents:
      /usr/lib/libssl.so:
  bins:
    contents:
      /usr/bin/openssl:
`))
	if err != nil {
		t.Fatal(err)
	}
	diags := analysis.CheckCopyrightEssential("openssl.yaml", sf)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics (package-level covers all), got %d: %v", len(diags), diags)
	}
}

func TestCheckCopyrightEssential_CopyrightSliceExempt(t *testing.T) {
	sf, err := parser.ParseBytes([]byte(`package: openssl
slices:
  copyright:
    contents:
      /usr/share/doc/openssl/copyright:
`))
	if err != nil {
		t.Fatal(err)
	}
	diags := analysis.CheckCopyrightEssential("openssl.yaml", sf)
	if len(diags) != 0 {
		t.Fatalf("copyright slice should be exempt, got %d diagnostics: %v", len(diags), diags)
	}
}

func TestCheckCopyrightEssential_NoPackage(t *testing.T) {
	sf, err := parser.ParseBytes([]byte(`slices:
  libs:
    contents:
      /usr/lib/foo:
`))
	if err != nil {
		t.Fatal(err)
	}
	diags := analysis.CheckCopyrightEssential("foo.yaml", sf)
	if len(diags) != 0 {
		t.Fatalf("no package → no diagnostics, got %d: %v", len(diags), diags)
	}
}
