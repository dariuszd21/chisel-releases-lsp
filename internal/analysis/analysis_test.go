package analysis_test

import (
	"os"
	"path/filepath"
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
				if len(d.Message) > 0 {
					if contains(d.Message, c.msgFrag) {
						found = true
					}
				}
			}
			if !found {
				t.Errorf("expected message fragment %q in diagnostics, got: %v", c.msgFrag, diags)
			}
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

func TestDetectCollisions(t *testing.T) {
	dir := t.TempDir()
	slicesDir := filepath.Join(dir, "slices")
	os.Mkdir(slicesDir, 0755)

	a := `package: pkga
slices:
  s:
    contents:
      /shared/path:
      /only/a:
`
	b := `package: pkgb
slices:
  s:
    contents:
      /shared/path:
      /only/b:
`
	os.WriteFile(filepath.Join(slicesDir, "pkga.yaml"), []byte(a), 0644)
	os.WriteFile(filepath.Join(slicesDir, "pkgb.yaml"), []byte(b), 0644)

	idx, err := index.New(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	collisions := analysis.DetectCollisions(idx)
	if len(collisions) != 1 {
		t.Fatalf("expected 1 collision, got %d: %+v", len(collisions), collisions)
	}
	if collisions[0].Path != "/shared/path" {
		t.Errorf("collision path: %q", collisions[0].Path)
	}
}
