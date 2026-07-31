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

// storeRelease returns a release with a single "bin" store, at the given format.
func storeRelease(t *testing.T, format string) *parser.Release {
	t.Helper()
	rel, err := parser.ParseReleaseBytes([]byte("format: " + format + `
stores:
  bin:
    kind: bin
    version: 26.10
    default-prefix: "bin-"
`))
	if err != nil {
		t.Fatal(err)
	}
	return rel
}

func mustParseSlices(t *testing.T, yaml string) *parser.SliceFile {
	t.Helper()
	sf, err := parser.ParseBytes([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	return sf
}

// messages joins diagnostic messages so tests can assert on substrings.
func messages(diags []analysis.Diagnostic) string {
	out := make([]string, 0, len(diags))
	for _, d := range diags {
		out = append(out, d.Message)
	}
	return strings.Join(out, " | ")
}

func TestCheckRelease_UnknownFormat(t *testing.T) {
	rel, err := parser.ParseReleaseBytes([]byte("format: v9\n"))
	if err != nil {
		t.Fatal(err)
	}
	diags := analysis.CheckRelease("chisel.yaml", rel)
	if len(diags) != 1 || !strings.Contains(diags[0].Message, `unknown format "v9"`) {
		t.Fatalf("got %q", messages(diags))
	}
	if diags[0].Severity != analysis.SeverityError {
		t.Errorf("severity: got %v, want error", diags[0].Severity)
	}
}

func TestCheckRelease_V4IsKnown(t *testing.T) {
	// v4 is the newest format and must not be reported as unknown.
	if diags := analysis.CheckRelease("chisel.yaml", storeRelease(t, "v4")); len(diags) != 0 {
		t.Fatalf("expected no diagnostics for format v4, got %q", messages(diags))
	}
}

func TestCheckRelease_StoresRequireV3(t *testing.T) {
	diags := analysis.CheckRelease("chisel.yaml", storeRelease(t, "v2"))
	if len(diags) != 1 || !strings.Contains(diags[0].Message, "'stores' is unsupported before format v3") {
		t.Fatalf("got %q", messages(diags))
	}
}

func TestCheckRelease_StoreFieldValidation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		msgFrag string
	}{{
		name:    "missing kind",
		yaml:    "format: v4\nstores:\n  bin:\n    version: 26.10\n    default-prefix: bin-\n",
		msgFrag: "missing the 'kind' field",
	}, {
		name:    "unknown kind",
		yaml:    "format: v4\nstores:\n  bin:\n    kind: snap\n    version: 26.10\n    default-prefix: bin-\n",
		msgFrag: `unknown kind "snap"`,
	}, {
		name:    "missing version",
		yaml:    "format: v4\nstores:\n  bin:\n    kind: bin\n    default-prefix: bin-\n",
		msgFrag: "missing the 'version' field",
	}, {
		name:    "missing default-prefix",
		yaml:    "format: v4\nstores:\n  bin:\n    kind: bin\n    version: 26.10\n",
		msgFrag: "missing the 'default-prefix' field",
	}}
	for _, tc := range tests {
		rel, err := parser.ParseReleaseBytes([]byte(tc.yaml))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		diags := analysis.CheckRelease("chisel.yaml", rel)
		if !strings.Contains(messages(diags), tc.msgFrag) {
			t.Errorf("%s: got %q, want mention of %q", tc.name, messages(diags), tc.msgFrag)
		}
	}
}

func TestCheckStoreFields_Valid(t *testing.T) {
	sf := mustParseSlices(t, "package: curl\nstore: bin\ndefault-track: \"3.0\"\nslices:\n  bins:\n")
	// Format v4 keeps store definitions in slices/, so this path is correct.
	diags := analysis.CheckStoreFields("/r/slices/curl.yaml", sf, storeRelease(t, "v4"))
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %q", messages(diags))
	}
}

func TestCheckStoreFields_NoStoreFieldsIsNoOp(t *testing.T) {
	sf := mustParseSlices(t, "package: curl\nslices:\n  bins:\n")
	if diags := analysis.CheckStoreFields("/r/slices/curl.yaml", sf, storeRelease(t, "v4")); diags != nil {
		t.Fatalf("expected nil, got %q", messages(diags))
	}
}

func TestCheckStoreFields_Errors(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		format  string
		path    string
		msgFrag string
	}{{
		name:    "store before v3",
		yaml:    "package: curl\nstore: bin\ndefault-track: \"3.0\"\n",
		format:  "v2",
		path:    "/r/slices/curl.yaml",
		msgFrag: "'store' is unsupported before format v3",
	}, {
		name:    "default-track before v3",
		yaml:    "package: curl\ndefault-track: \"3.0\"\n",
		format:  "v1",
		path:    "/r/slices/curl.yaml",
		msgFrag: "'default-track' is unsupported before format v3",
	}, {
		name:    "default-track without store",
		yaml:    "package: curl\ndefault-track: \"3.0\"\n",
		format:  "v4",
		path:    "/r/slices/curl.yaml",
		msgFrag: "'default-track' requires 'store'",
	}, {
		name:    "store without default-track",
		yaml:    "package: curl\nstore: bin\n",
		format:  "v4",
		path:    "/r/slices/curl.yaml",
		msgFrag: "'store' requires 'default-track'",
	}, {
		name:    "undefined store",
		yaml:    "package: curl\nstore: nope\ndefault-track: \"3.0\"\n",
		format:  "v4",
		path:    "/r/slices/curl.yaml",
		msgFrag: `store "nope" is not defined in chisel.yaml`,
	}, {
		name:    "store and archive together",
		yaml:    "package: curl\nstore: bin\narchive: ubuntu\ndefault-track: \"3.0\"\n",
		format:  "v4",
		path:    "/r/slices/curl.yaml",
		msgFrag: "'store' and 'archive' are mutually exclusive",
	}, {
		name:    "default-track with a risk",
		yaml:    "package: curl\nstore: bin\ndefault-track: 3.0/stable\n",
		format:  "v4",
		path:    "/r/slices/curl.yaml",
		msgFrag: "must not contain '/'",
	}, {
		name:    "v3 store definition outside bin-slices",
		yaml:    "package: curl\nstore: bin\ndefault-track: \"3.0\"\n",
		format:  "v3",
		path:    "/r/slices/curl.yaml",
		msgFrag: "must live in bin-slices/ for format v3",
	}, {
		name:    "v4 store definition outside slices",
		yaml:    "package: curl\nstore: bin\ndefault-track: \"3.0\"\n",
		format:  "v4",
		path:    "/r/bin-slices/curl.yaml",
		msgFrag: "must live in slices/ for format v4",
	}}

	for _, tc := range tests {
		sf := mustParseSlices(t, tc.yaml)
		diags := analysis.CheckStoreFields(tc.path, sf, storeRelease(t, tc.format))
		if !strings.Contains(messages(diags), tc.msgFrag) {
			t.Errorf("%s: got %q, want mention of %q", tc.name, messages(diags), tc.msgFrag)
		}
	}
}

// TestCheckStoreFields_V3InBinSlices verifies the format-v3 layout, where store
// definitions live in bin-slices/ so that Chisel versions without store support
// never read them.
func TestCheckStoreFields_V3InBinSlices(t *testing.T) {
	sf := mustParseSlices(t, "package: curl\nstore: bin\ndefault-track: \"3.0\"\n")
	if diags := analysis.CheckStoreFields("/r/bin-slices/curl.yaml", sf, storeRelease(t, "v3")); len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %q", messages(diags))
	}
}

func TestCheckChannels_Valid(t *testing.T) {
	sf := mustParseSlices(t, `
package: curl
store: bin
default-track: "3.0"
slices:
  bins:
    essential:
      libc6_libs:
        channel: 3.0/edge
    contents:
      /usr/bin/curl: {channel: [3.0/*, 2.0/!stable]}
`)
	diags := analysis.CheckChannels("/r/slices/curl.yaml", sf, storeRelease(t, "v4"))
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %q", messages(diags))
	}
}

func TestCheckChannels_Errors(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		format  string
		msgFrag string
	}{{
		name: "channel before v3",
		yaml: `
package: curl
slices:
  bins:
    contents:
      /usr/bin/curl: {channel: 3.0/edge}
`,
		format:  "v2",
		msgFrag: "'channel' is unsupported before format v3",
	}, {
		name: "channel on a non-store package",
		yaml: `
package: curl
slices:
  bins:
    contents:
      /usr/bin/curl: {channel: 3.0/edge}
`,
		format:  "v4",
		msgFrag: `'channel' on path "/usr/bin/curl" requires the package to be in a store`,
	}, {
		name: "channel on a non-store essential",
		yaml: `
package: curl
slices:
  bins:
    essential:
      libc6_libs:
        channel: 3.0/edge
`,
		format:  "v4",
		msgFrag: `'channel' on essential "libc6_libs" requires the package to be in a store`,
	}, {
		name: "invalid pattern",
		yaml: `
package: curl
store: bin
default-track: "3.0"
slices:
  bins:
    contents:
      /usr/bin/curl: {channel: 3.0/whatever}
`,
		format:  "v4",
		msgFrag: `invalid channel "3.0/whatever": unknown risk "whatever"`,
	}, {
		name: "repeated track",
		yaml: `
package: curl
store: bin
default-track: "3.0"
slices:
  bins:
    contents:
      /usr/bin/curl: {channel: [3.0/edge, 3.0/stable]}
`,
		format:  "v4",
		msgFrag: `track "3.0" is repeated`,
	}}

	for _, tc := range tests {
		sf := mustParseSlices(t, tc.yaml)
		diags := analysis.CheckChannels("/r/slices/curl.yaml", sf, storeRelease(t, tc.format))
		if !strings.Contains(messages(diags), tc.msgFrag) {
			t.Errorf("%s: got %q, want mention of %q", tc.name, messages(diags), tc.msgFrag)
		}
	}
}

// TestCheckChannels_PackageLevelEssential verifies that a channel on the
// package-level essential block is validated too.
func TestCheckChannels_PackageLevelEssential(t *testing.T) {
	sf := mustParseSlices(t, `
package: curl
store: bin
default-track: "3.0"
essential:
  libc6_libs:
    channel: 3.0/bogus
slices:
  bins:
`)
	diags := analysis.CheckChannels("/r/slices/curl.yaml", sf, storeRelease(t, "v4"))
	if !strings.Contains(messages(diags), `unknown risk "bogus"`) {
		t.Fatalf("got %q", messages(diags))
	}
}

// TestDetectCollisions_IgnoresChannel verifies that path conflicts are computed
// irrespective of `channel:`, exactly as chisel does for `arch:`. Even though
// 3.0/stable and 3.0/edge are never cut together, chisel still reports the
// conflict, so the LSP must too.
func TestDetectCollisions_IgnoresChannel(t *testing.T) {
	dir := t.TempDir()
	slicesDir := filepath.Join(dir, "slices")
	if err := os.MkdirAll(slicesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(slicesDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "chisel.yaml"), []byte(`format: v4
stores:
  bin:
    kind: bin
    version: 26.10
    default-prefix: "bin-"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	write("curl.yaml", `package: curl
store: bin
default-track: "3.0"
slices:
  bins:
    contents:
      /usr/bin/tool: {channel: 3.0/stable}
`)
	write("wget.yaml", `package: wget
store: bin
default-track: "3.0"
slices:
  bins:
    contents:
      /usr/bin/tool: {channel: 3.0/edge}
`)

	idx, err := index.New(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	cols := analysis.DetectCollisions(idx)
	if len(cols) != 1 {
		t.Fatalf("expected 1 collision despite disjoint channels, got %d", len(cols))
	}
	if cols[0].Path != "/usr/bin/tool" {
		t.Errorf("collision path: got %q, want %q", cols[0].Path, "/usr/bin/tool")
	}
}
