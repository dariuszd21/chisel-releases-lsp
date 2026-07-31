package parser_test

import (
	"testing"

	"github.com/dariuszd21/chisel-releases-lsp/internal/parser"
)

const releaseV4 = `
format: v4
archives:
  ubuntu:
    version: 26.04
    components: [main, universe]
    suites: [plucky]
stores:
  bin:
    kind: bin
    version: 26.10
    default-prefix: "bin-"
`

func TestParseRelease_V4WithStores(t *testing.T) {
	rel, err := parser.ParseReleaseBytes([]byte(releaseV4))
	if err != nil {
		t.Fatal(err)
	}
	if rel.Format != "v4" {
		t.Errorf("Format: got %q, want %q", rel.Format, "v4")
	}
	if _, ok := rel.Archives["ubuntu"]; !ok {
		t.Errorf("archives: missing 'ubuntu', got %v", rel.ArchiveOrder)
	}
	st := rel.LookupStore("bin")
	if st == nil {
		t.Fatalf("store 'bin' not parsed, got %v", rel.StoreOrder)
	}
	if st.Kind != "bin" || st.Version != "26.10" || st.DefaultPrefix != "bin-" {
		t.Errorf("store: got %+v", st)
	}
	// The format value range must point at the scalar so diagnostics land on it.
	if rel.FormatRange.Start.Line != 1 {
		t.Errorf("format line: got %d, want 1", rel.FormatRange.Start.Line)
	}
}

func TestParseRelease_V2Archives(t *testing.T) {
	rel, err := parser.ParseReleaseBytes([]byte("format: v1\nv2-archives:\n  ubuntu:\n    version: 24.04\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rel.Archives["ubuntu"]; !ok {
		t.Errorf("v2-archives not collected: %v", rel.ArchiveOrder)
	}
}

func TestParseRelease_Empty(t *testing.T) {
	rel, err := parser.ParseReleaseBytes(nil)
	if err != nil {
		t.Fatal(err)
	}
	if rel.Format != "" || len(rel.Stores) != 0 {
		t.Errorf("expected empty release, got %+v", rel)
	}
}

func TestFormatAtLeast(t *testing.T) {
	tests := []struct {
		format string
		atV3   bool
		atV4   bool
	}{
		{"", false, false},
		{"v1", false, false},
		{"v2", false, false},
		{"v3", true, false},
		{"v4", true, true},
		{"bogus", false, false},
	}
	for _, tc := range tests {
		rel := &parser.Release{Format: tc.format}
		if got := rel.FormatAtLeast(parser.FormatV3); got != tc.atV3 {
			t.Errorf("format %q FormatAtLeast(v3): got %v, want %v", tc.format, got, tc.atV3)
		}
		if got := rel.FormatAtLeast(parser.FormatV4); got != tc.atV4 {
			t.Errorf("format %q FormatAtLeast(v4): got %v, want %v", tc.format, got, tc.atV4)
		}
		if got := rel.SupportsStores(); got != tc.atV3 {
			t.Errorf("format %q SupportsStores: got %v, want %v", tc.format, got, tc.atV3)
		}
	}
	// A nil release must behave like the oldest format, never triggering
	// format-gated diagnostics in workspaces without a chisel.yaml.
	var nilRel *parser.Release
	if nilRel.FormatAtLeast(parser.FormatV1) {
		t.Error("nil release must not satisfy any format")
	}
}

func TestStoreSlicesDir(t *testing.T) {
	// In v3 store definitions are segregated so older Chisel never reads them.
	if got := (&parser.Release{Format: "v3"}).StoreSlicesDir(); got != "bin-slices" {
		t.Errorf("v3 StoreSlicesDir: got %q, want %q", got, "bin-slices")
	}
	// From v4 they live alongside regular definitions.
	if got := (&parser.Release{Format: "v4"}).StoreSlicesDir(); got != "slices" {
		t.Errorf("v4 StoreSlicesDir: got %q, want %q", got, "slices")
	}
	var nilRel *parser.Release
	if got := nilRel.StoreSlicesDir(); got != "bin-slices" {
		t.Errorf("nil StoreSlicesDir: got %q", got)
	}
}
