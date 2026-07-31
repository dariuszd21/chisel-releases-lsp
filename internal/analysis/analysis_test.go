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
			// [ is a literal character in chisel paths, not a glob metachar.
			// /usr/bin/[ is a real path (the GNU [ command).
			yaml: `package: p
slices:
  s:
    contents:
      /usr/bin/[:
`,
			wantErr: false,
		},
		{
			// Bracket characters anywhere in a path are always literal.
			yaml: `package: p
slices:
  s:
    contents:
      /usr/bin/foo[bar:
`,
			wantErr: false,
		},
		{
			// Closed bracket is also literal.
			yaml: `package: p
slices:
  s:
    contents:
      /usr/bin/foo[bar]:
`,
			wantErr: false,
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
	// Per chisel spec, ** matches zero or more characters including /.
	// There is no requirement for ** to be a standalone path segment.
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
	if len(diags) != 0 {
		t.Errorf("/foo**/bar is a valid chisel path, got unexpected diagnostics: %v", diags)
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
	// Per chisel spec, ** matches zero or more characters including /.
	// /usr/lib/**.so is valid: it matches .so files at any depth under /usr/lib/.
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
	if len(diags) != 0 {
		t.Errorf("/usr/lib/**.so is a valid chisel path, got unexpected diagnostics: %v", diags)
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

func TestDetectCollisions_IdenticalGlobIsCollision(t *testing.T) {
	// Two packages with the exact same glob pattern dispute every file it matches.
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
	if len(collisions) != 1 {
		t.Errorf("expected 1 collision for identical glob in two packages, got %d: %+v", len(collisions), collisions)
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
	diags := analysis.CheckCopyrightEssential("openssl.yaml", sf, "")
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
	diags := analysis.CheckCopyrightEssential("openssl.yaml", sf, "")
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
	diags := analysis.CheckCopyrightEssential("openssl.yaml", sf, "")
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
	diags := analysis.CheckCopyrightEssential("openssl.yaml", sf, "")
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
	diags := analysis.CheckCopyrightEssential("foo.yaml", sf, "")
	if len(diags) != 0 {
		t.Fatalf("no package → no diagnostics, got %d: %v", len(diags), diags)
	}
}

func TestCheckDuplicateEssentials_WithinSlice(t *testing.T) {
	sf, err := parser.ParseBytes([]byte(`package: openssl
slices:
  libs:
    essential:
      - libc6_libs
      - libc6_libs
    contents:
      /usr/lib/libssl.so:
`))
	if err != nil {
		t.Fatal(err)
	}
	dups := analysis.CheckDuplicateEssentials("openssl.yaml", sf)
	if len(dups) != 1 {
		t.Fatalf("expected 1 duplicate, got %d: %v", len(dups), dups)
	}
	if dups[0].DupRef.Value != "libc6_libs" {
		t.Errorf("expected DupRef libc6_libs, got %q", dups[0].DupRef.Value)
	}
	if dups[0].SliceName != "libs" {
		t.Errorf("expected SliceName libs, got %q", dups[0].SliceName)
	}
}

func TestCheckDuplicateEssentials_PackageLevel(t *testing.T) {
	sf, err := parser.ParseBytes([]byte(`package: openssl
essential:
  - libc6_libs
  - libc6_libs
slices:
  libs:
    contents:
      /usr/lib/libssl.so:
`))
	if err != nil {
		t.Fatal(err)
	}
	dups := analysis.CheckDuplicateEssentials("openssl.yaml", sf)
	if len(dups) != 1 {
		t.Fatalf("expected 1 duplicate, got %d: %v", len(dups), dups)
	}
	if dups[0].SliceName != "" {
		t.Errorf("expected empty SliceName (package-level), got %q", dups[0].SliceName)
	}
}

func TestCheckDuplicateEssentials_CrossBlockRedundant(t *testing.T) {
	// Same ref in package-level AND slice-level is redundant — the slice inherits it.
	sf, err := parser.ParseBytes([]byte(`package: openssl
essential:
  - libc6_libs
slices:
  libs:
    essential:
      - libc6_libs
    contents:
      /usr/lib/libssl.so:
`))
	if err != nil {
		t.Fatal(err)
	}
	dups := analysis.CheckDuplicateEssentials("openssl.yaml", sf)
	if len(dups) != 1 {
		t.Fatalf("expected 1 cross-block duplicate, got %d: %v", len(dups), dups)
	}
	if dups[0].SliceName != "libs" {
		t.Errorf("expected SliceName libs, got %q", dups[0].SliceName)
	}
	if dups[0].DupRef.Value != "libc6_libs" {
		t.Errorf("expected DupRef libc6_libs, got %q", dups[0].DupRef.Value)
	}
	// FirstRef should point to the package-level occurrence.
	if dups[0].FirstRef.Value != "libc6_libs" {
		t.Errorf("expected FirstRef libc6_libs, got %q", dups[0].FirstRef.Value)
	}
}

func TestCheckDuplicateEssentials_NoDuplicates(t *testing.T) {
	sf, err := parser.ParseBytes([]byte(`package: openssl
slices:
  libs:
    essential:
      - libc6_libs
      - zlib1g_libs
    contents:
      /usr/lib/libssl.so:
`))
	if err != nil {
		t.Fatal(err)
	}
	dups := analysis.CheckDuplicateEssentials("openssl.yaml", sf)
	if len(dups) != 0 {
		t.Errorf("expected no duplicates, got %d: %v", len(dups), dups)
	}
}

// --- CheckLexicalOrder ---

func TestCheckLexicalOrder_ContentsSorted(t *testing.T) {
	sf := mustParseYAML(t, `package: p
slices:
  s:
    contents:
      /usr/bin/a:
      /usr/bin/b:
      /usr/bin/c:
`)
	if diags := analysis.CheckLexicalOrder("p.yaml", sf); len(diags) != 0 {
		t.Errorf("expected no diagnostics for sorted contents, got: %v", diags)
	}
}

func TestCheckLexicalOrder_ContentsOutOfOrder(t *testing.T) {
	sf := mustParseYAML(t, `package: p
slices:
  s:
    contents:
      /usr/bin/z:
      /usr/bin/a:
`)
	diags := analysis.CheckLexicalOrder("p.yaml", sf)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, "/usr/bin/a") || !strings.Contains(diags[0].Message, "/usr/bin/z") {
		t.Errorf("message should mention both paths, got: %q", diags[0].Message)
	}
	if diags[0].Severity != analysis.SeverityWarning {
		t.Errorf("expected Warning severity, got %v", diags[0].Severity)
	}
}

func TestCheckLexicalOrder_ContentsOnlyOneEntry(t *testing.T) {
	sf := mustParseYAML(t, `package: p
slices:
  s:
    contents:
      /usr/bin/a:
`)
	if diags := analysis.CheckLexicalOrder("p.yaml", sf); len(diags) != 0 {
		t.Errorf("expected no diagnostics for single-entry contents, got: %v", diags)
	}
}

func TestCheckLexicalOrder_EssentialSliceLevelOutOfOrder(t *testing.T) {
	sf := mustParseYAML(t, `package: p
slices:
  s:
    essential:
      - zlib1g_libs
      - libc6_libs
    contents:
      /usr/bin/a:
`)
	diags := analysis.CheckLexicalOrder("p.yaml", sf)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, "libc6_libs") || !strings.Contains(diags[0].Message, "zlib1g_libs") {
		t.Errorf("message should mention both refs, got: %q", diags[0].Message)
	}
}

func TestCheckLexicalOrder_EssentialPackageLevelOutOfOrder(t *testing.T) {
	sf := mustParseYAML(t, `package: p
essential:
  - zlib1g_libs
  - libc6_libs
slices:
  s:
    contents:
      /usr/bin/a:
`)
	diags := analysis.CheckLexicalOrder("p.yaml", sf)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, "package-level") {
		t.Errorf("message should mention package-level, got: %q", diags[0].Message)
	}
}

func TestCheckLexicalOrder_MultipleViolations(t *testing.T) {
	// Both the slice essential and contents blocks are out of order.
	sf := mustParseYAML(t, `package: p
slices:
  s:
    essential:
      - zlib1g_libs
      - libc6_libs
    contents:
      /usr/bin/z:
      /usr/bin/a:
`)
	diags := analysis.CheckLexicalOrder("p.yaml", sf)
	if len(diags) != 2 {
		t.Fatalf("expected 2 diagnostics (one per block), got %d: %v", len(diags), diags)
	}
}

func TestCheckLexicalOrder_AlreadySorted(t *testing.T) {
	sf := mustParseYAML(t, `package: p
essential:
  - libc6_libs
  - zlib1g_libs
slices:
  s:
    essential:
      - libc6_libs
      - openssl_libs
    contents:
      /usr/bin/a:
      /usr/bin/b:
`)
	if diags := analysis.CheckLexicalOrder("p.yaml", sf); len(diags) != 0 {
		t.Errorf("expected no diagnostics for fully sorted file, got: %v", diags)
	}
}

func TestCheckLexicalOrder_V3EssentialOutOfOrder(t *testing.T) {
	// v3 map-style essential: keys should be subject to the same lexical check.
	sf := mustParseYAML(t, `package: p
slices:
  s:
    essential:
      zlib1g_libs:
      libc6_libs: {arch: amd64}
    contents:
      /usr/bin/a:
`)
	diags := analysis.CheckLexicalOrder("p.yaml", sf)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic for v3 out-of-order essential, got %d: %v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, "libc6_libs") || !strings.Contains(diags[0].Message, "zlib1g_libs") {
		t.Errorf("message should mention both refs, got: %q", diags[0].Message)
	}
}

func TestCheckLexicalOrder_V3EssentialSorted(t *testing.T) {
	sf := mustParseYAML(t, `package: p
slices:
  s:
    essential:
      libc6_libs:
      zlib1g_libs: {arch: amd64}
    contents:
      /usr/bin/a:
`)
	if diags := analysis.CheckLexicalOrder("p.yaml", sf); len(diags) != 0 {
		t.Errorf("expected no diagnostics for sorted v3 essential, got: %v", diags)
	}
}

// --- DetectCollisions prefer suppression ---

func TestDetectCollisions_PreferSuppresses(t *testing.T) {
	// One side carries prefer: pointing at the other — no collision expected.
	idx := setupCollisionIndex(t, map[string]string{
		"pkga.yaml": `package: pkga
slices:
  s:
    contents:
      /shared/path: {prefer: pkgb}
`,
		"pkgb.yaml": `package: pkgb
slices:
  s:
    contents:
      /shared/path:
`,
	})
	if collisions := analysis.DetectCollisions(idx); len(collisions) != 0 {
		t.Errorf("expected no collisions when prefer suppresses, got: %+v", collisions)
	}
}

func TestDetectCollisions_MutualPrefer(t *testing.T) {
	// Both sides prefer each other — the generic collision is still suppressed
	// (DetectCollisions treats it as acknowledged). ValidatePrefer catches
	// the contradiction as a separate Error diagnostic.
	idx := setupCollisionIndex(t, map[string]string{
		"pkga.yaml": `package: pkga
slices:
  s:
    contents:
      /shared/path: {prefer: pkgb}
`,
		"pkgb.yaml": `package: pkgb
slices:
  s:
    contents:
      /shared/path: {prefer: pkga}
`,
	})
	if collisions := analysis.DetectCollisions(idx); len(collisions) != 0 {
		t.Errorf("expected no collisions with mutual prefer (ValidatePrefer handles it), got: %+v", collisions)
	}
}

func TestDetectCollisions_PreferFanIn(t *testing.T) {
	// B→A and C→A: both B and C defer to A's file when A is present.
	// However, B and C are NOT in the same linear chain — they are not ordered
	// relative to each other. Without A, B and C conflict on the path, so the
	// B-C collision must still be reported.
	idx := setupCollisionIndex(t, map[string]string{
		"pkga.yaml": `package: pkga
slices:
  s:
    contents:
      /shared/path:
`,
		"pkgb.yaml": `package: pkgb
slices:
  s:
    contents:
      /shared/path: {prefer: pkga}
`,
		"pkgc.yaml": `package: pkgc
slices:
  s:
    contents:
      /shared/path: {prefer: pkga}
`,
	})
	collisions := analysis.DetectCollisions(idx)
	// B-A and C-A are suppressed by prefer; B-C is not.
	if len(collisions) != 1 {
		t.Errorf("expected 1 collision (B-C), got %d: %+v", len(collisions), collisions)
	}
}

func TestDetectCollisions_PreferTransitiveSuppresses(t *testing.T) {
	// Chain: C→B→A (C has prefer: B, B has prefer: A).
	// All three claim the same path. A is last in the chain, so A's file is
	// installed for all three. No collision should be reported for any pair.
	idx := setupCollisionIndex(t, map[string]string{
		"pkga.yaml": `package: pkga
slices:
  s:
    contents:
      /shared/path:
`,
		"pkgb.yaml": `package: pkgb
slices:
  s:
    contents:
      /shared/path: {prefer: pkga}
`,
		"pkgc.yaml": `package: pkgc
slices:
  s:
    contents:
      /shared/path: {prefer: pkgb}
`,
	})
	if collisions := analysis.DetectCollisions(idx); len(collisions) != 0 {
		t.Errorf("expected no collisions for transitive prefer chain C→B→A, got: %+v", collisions)
	}
}

func TestDetectCollisions_PreferWrongPackage(t *testing.T) {
	// prefer points at a third package, not the actual conflicting one — collision must fire.
	idx := setupCollisionIndex(t, map[string]string{
		"pkga.yaml": `package: pkga
slices:
  s:
    contents:
      /shared/path: {prefer: pkgc}
`,
		"pkgb.yaml": `package: pkgb
slices:
  s:
    contents:
      /shared/path:
`,
	})
	if collisions := analysis.DetectCollisions(idx); len(collisions) != 1 {
		t.Errorf("expected 1 collision when prefer targets wrong package, got: %d", len(collisions))
	}
}

// --- ValidatePrefer ---

func TestValidatePrefer_OnGlob(t *testing.T) {
	idx := setupCollisionIndex(t, map[string]string{
		"pkga.yaml": `package: pkga
slices:
  s:
    contents:
      /usr/lib/*.so: {prefer: pkgb}
`,
		"pkgb.yaml": `package: pkgb
slices:
  s:
    contents:
      /usr/lib/a.so:
`,
	})
	files := idx.AllFiles()
	var pkgaFile string
	for _, f := range files {
		if idx.FileSliceFile(f).Package == "pkga" {
			pkgaFile = f
		}
	}
	sf := idx.FileSliceFile(pkgaFile)
	diags := analysis.ValidatePrefer(pkgaFile, sf, idx)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic for prefer on glob, got %d: %v", len(diags), diags)
	}
	if diags[0].Severity != analysis.SeverityError {
		t.Errorf("expected Error severity, got %v", diags[0].Severity)
	}
	if !strings.Contains(diags[0].Message, "glob") {
		t.Errorf("message should mention glob, got: %q", diags[0].Message)
	}
}

func TestValidatePrefer_SamePackage(t *testing.T) {
	idx := setupCollisionIndex(t, map[string]string{
		"pkga.yaml": `package: pkga
slices:
  s:
    contents:
      /usr/lib/a.so: {prefer: pkga}
`,
	})
	files := idx.AllFiles()
	sf := idx.FileSliceFile(files[0])
	diags := analysis.ValidatePrefer(files[0], sf, idx)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic for prefer == own package, got %d: %v", len(diags), diags)
	}
	if diags[0].Severity != analysis.SeverityError {
		t.Errorf("expected Error severity, got %v", diags[0].Severity)
	}
	if !strings.Contains(diags[0].Message, "pkga") {
		t.Errorf("message should mention the package name, got: %q", diags[0].Message)
	}
}

func TestValidatePrefer_UnknownPackage(t *testing.T) {
	idx := setupCollisionIndex(t, map[string]string{
		"pkga.yaml": `package: pkga
slices:
  s:
    contents:
      /usr/lib/a.so: {prefer: nonexistent}
`,
	})
	files := idx.AllFiles()
	sf := idx.FileSliceFile(files[0])
	diags := analysis.ValidatePrefer(files[0], sf, idx)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic for unknown prefer target, got %d: %v", len(diags), diags)
	}
	if diags[0].Severity != analysis.SeverityWarning {
		t.Errorf("expected Warning severity, got %v", diags[0].Severity)
	}
	if !strings.Contains(diags[0].Message, "nonexistent") {
		t.Errorf("message should mention the unknown package, got: %q", diags[0].Message)
	}
}

func TestValidatePrefer_MutualPrefer(t *testing.T) {
	// Both packages prefer each other on the same path — this forms a cycle
	// in the prefer chain, which is invalid (prefer must be a linear sequence).
	idx := setupCollisionIndex(t, map[string]string{
		"pkga.yaml": `package: pkga
slices:
  s:
    contents:
      /shared/path: {prefer: pkgb}
`,
		"pkgb.yaml": `package: pkgb
slices:
  s:
    contents:
      /shared/path: {prefer: pkga}
`,
	})
	files := idx.AllFiles()
	var pkgaFile string
	for _, f := range files {
		if idx.FileSliceFile(f).Package == "pkga" {
			pkgaFile = f
		}
	}
	sf := idx.FileSliceFile(pkgaFile)
	diags := analysis.ValidatePrefer(pkgaFile, sf, idx)
	if len(diags) != 1 {
		t.Fatalf("expected 1 mutual-prefer diagnostic, got %d: %v", len(diags), diags)
	}
	if diags[0].Severity != analysis.SeverityError {
		t.Errorf("expected Error severity, got %v", diags[0].Severity)
	}
	if !strings.Contains(diags[0].Message, "cycle") {
		t.Errorf("message should mention cycle, got: %q", diags[0].Message)
	}
}

func TestValidatePrefer_Valid(t *testing.T) {
	idx := setupCollisionIndex(t, map[string]string{
		"pkga.yaml": `package: pkga
slices:
  s:
    contents:
      /usr/lib/a.so: {prefer: pkgb}
`,
		"pkgb.yaml": `package: pkgb
slices:
  s:
    contents:
      /usr/lib/a.so:
`,
	})
	files := idx.AllFiles()
	var pkgaFile string
	for _, f := range files {
		if idx.FileSliceFile(f).Package == "pkga" {
			pkgaFile = f
		}
	}
	sf := idx.FileSliceFile(pkgaFile)
	if diags := analysis.ValidatePrefer(pkgaFile, sf, idx); len(diags) != 0 {
		t.Errorf("expected no diagnostics for valid prefer, got: %v", diags)
	}
}

// --- globMatchesPath ---

func TestGlobMatchesPath(t *testing.T) {
	cases := []struct {
		glob string
		path string
		want bool
	}{
		// * does not cross /
		{"/usr/lib/*.so", "/usr/lib/libssl.so", true},
		{"/usr/lib/*.so", "/usr/lib/libssl.so.3", false},
		{"/usr/lib/*.so", "/usr/lib/sub/libssl.so", false},
		// ? matches one char (not /)
		{"/usr/bin/?", "/usr/bin/[", true},
		{"/usr/bin/?", "/usr/bin/ab", false},
		// ** crosses /
		{"/usr/lib/**", "/usr/lib/libssl.so", true},
		{"/usr/lib/**", "/usr/lib/sub/dir/libssl.so", true},
		{"/usr/share/**/README", "/usr/share/doc/openssl/README", true},
		{"/usr/share/**/README", "/usr/share/README", true},
		{"/usr/share/**/README", "/usr/share/doc/NOTREADME", false},
		// ** in mixed segment
		{"/usr/lib/**.so", "/usr/lib/libssl.so", true},
		{"/usr/lib/**.so", "/usr/lib/sub/libssl.so", true},
		{"/usr/lib/**.so", "/usr/lib/sub/libssl.so.3", false},
		// exact (no wildcards)
		{"/usr/bin/foo", "/usr/bin/foo", true},
		{"/usr/bin/foo", "/usr/bin/bar", false},
	}
	for _, c := range cases {
		got := analysis.GlobMatchesPath(c.glob, c.path)
		if got != c.want {
			t.Errorf("globMatchesPath(%q, %q) = %v, want %v", c.glob, c.path, got, c.want)
		}
	}
}

// --- DetectGlobCollisions ---

func TestDetectGlobCollisions_BasicMatch(t *testing.T) {
	idx := setupCollisionIndex(t, map[string]string{
		"pkga.yaml": `package: pkga
slices:
  s:
    contents:
      /usr/lib/*.so:
`,
		"pkgb.yaml": `package: pkgb
slices:
  s:
    contents:
      /usr/lib/libssl.so:
`,
	})
	cols := analysis.DetectGlobCollisions(idx)
	if len(cols) != 1 {
		t.Fatalf("expected 1 glob collision, got %d: %+v", len(cols), cols)
	}
	if cols[0].ExactPath != "/usr/lib/libssl.so" {
		t.Errorf("ExactPath: %q", cols[0].ExactPath)
	}
}

func TestDetectGlobCollisions_NoMatchDifferentDir(t *testing.T) {
	// * does not cross /
	idx := setupCollisionIndex(t, map[string]string{
		"pkga.yaml": `package: pkga
slices:
  s:
    contents:
      /usr/lib/*.so:
`,
		"pkgb.yaml": `package: pkgb
slices:
  s:
    contents:
      /usr/lib/sub/libssl.so:
`,
	})
	if cols := analysis.DetectGlobCollisions(idx); len(cols) != 0 {
		t.Errorf("expected no collision (* does not cross /), got: %+v", cols)
	}
}

func TestDetectGlobCollisions_DoubleStarCrossesDir(t *testing.T) {
	idx := setupCollisionIndex(t, map[string]string{
		"pkga.yaml": `package: pkga
slices:
  s:
    contents:
      /usr/lib/**:
`,
		"pkgb.yaml": `package: pkgb
slices:
  s:
    contents:
      /usr/lib/sub/libssl.so:
`,
	})
	if cols := analysis.DetectGlobCollisions(idx); len(cols) != 1 {
		t.Errorf("expected 1 collision (** crosses /), got %d: %+v", len(cols), cols)
	}
}

func TestDetectGlobCollisions_SamePackageSkipped(t *testing.T) {
	idx := setupCollisionIndex(t, map[string]string{
		"pkga.yaml": `package: pkga
slices:
  s:
    contents:
      /usr/lib/*.so:
      /usr/lib/libssl.so:
`,
	})
	if cols := analysis.DetectGlobCollisions(idx); len(cols) != 0 {
		t.Errorf("expected no cross-package collision for same package, got: %+v", cols)
	}
}

func TestDetectGlobCollisions_PreferSuppresses(t *testing.T) {
	idx := setupCollisionIndex(t, map[string]string{
		"pkga.yaml": `package: pkga
slices:
  s:
    contents:
      /usr/lib/*.so: {prefer: pkgb}
`,
		"pkgb.yaml": `package: pkgb
slices:
  s:
    contents:
      /usr/lib/libssl.so:
`,
	})
	if cols := analysis.DetectGlobCollisions(idx); len(cols) != 0 {
		t.Errorf("expected no collision when prefer suppresses, got: %+v", cols)
	}
}

// --- CheckRedundantPaths ---

func TestCheckRedundantPaths_Detected(t *testing.T) {
	sf := mustParseYAML(t, `package: p
slices:
  s:
    contents:
      /usr/lib/*.so:
      /usr/lib/libssl.so:
`)
	diags := analysis.CheckRedundantPaths("p.yaml", sf)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, "/usr/lib/libssl.so") {
		t.Errorf("message should mention redundant path, got: %q", diags[0].Message)
	}
	if diags[0].Severity != analysis.SeverityWarning {
		t.Errorf("expected Warning, got %v", diags[0].Severity)
	}
}

func TestCheckRedundantPaths_AttributesNotRedundant(t *testing.T) {
	// Exact path has attributes — NOT redundant even though glob covers it.
	sf := mustParseYAML(t, `package: p
slices:
  s:
    contents:
      /usr/lib/*.so:
      /usr/lib/libssl.so: {mode: 0755}
`)
	if diags := analysis.CheckRedundantPaths("p.yaml", sf); len(diags) != 0 {
		t.Errorf("entry with attributes should not be flagged as redundant, got: %v", diags)
	}
}

func TestCheckRedundantPaths_NoGlobs(t *testing.T) {
	sf := mustParseYAML(t, `package: p
slices:
  s:
    contents:
      /usr/lib/libssl.so:
      /usr/lib/libcrypto.so:
`)
	if diags := analysis.CheckRedundantPaths("p.yaml", sf); len(diags) != 0 {
		t.Errorf("no globs means no redundant paths, got: %v", diags)
	}
}

func TestCheckRedundantPaths_DifferentSliceNotRedundant(t *testing.T) {
	// Glob and exact are in different slices — not redundant.
	sf := mustParseYAML(t, `package: p
slices:
  libs:
    contents:
      /usr/lib/*.so:
  specific:
    contents:
      /usr/lib/libssl.so:
`)
	if diags := analysis.CheckRedundantPaths("p.yaml", sf); len(diags) != 0 {
		t.Errorf("cross-slice should not be flagged, got: %v", diags)
	}
}
