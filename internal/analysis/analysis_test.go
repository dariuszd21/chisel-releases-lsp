package analysis_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/canonical/chisel-releases-lsp/internal/analysis"
	"github.com/canonical/chisel-releases-lsp/internal/index"
	"github.com/canonical/chisel-releases-lsp/internal/parser"
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
