package index_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dariuszd21/chisel-releases-lsp/internal/index"
)

const chiselYamlV4 = `format: v4
stores:
  bin:
    kind: bin
    version: 26.10
    default-prefix: "bin-"
`

const chiselYamlV3 = `format: v3
stores:
  bin:
    kind: bin
    version: 26.10
    default-prefix: "bin-"
`

const curlStorePkg = `package: curl
store: bin
default-track: "3.0"
slices:
  bins:
    contents:
      /usr/bin/curl:
`

// writeReleaseDirs builds a release tree. files is keyed by a path relative to
// the release root, e.g. "chisel.yaml" or "bin-slices/curl.yaml".
func writeReleaseDirs(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		abs := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The index expects slices/ to exist even when it holds nothing.
	if err := os.MkdirAll(filepath.Join(dir, "slices"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestIndex_ReadsRelease(t *testing.T) {
	dir := writeReleaseDirs(t, map[string]string{"chisel.yaml": chiselYamlV4})
	idx, err := index.New(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	rel := idx.Release()
	if rel == nil {
		t.Fatal("Release() returned nil")
	}
	if rel.Format != "v4" {
		t.Errorf("format: got %q, want %q", rel.Format, "v4")
	}
	if rel.LookupStore("bin") == nil {
		t.Error("store 'bin' not loaded from chisel.yaml")
	}
}

// TestIndex_NoChiselYaml verifies the index still works in a bare slices/ tree.
func TestIndex_NoChiselYaml(t *testing.T) {
	dir := writeRelease(t, map[string]string{"libc6.yaml": libc6})
	idx, err := index.New(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	if idx.LookupSlice("libc6", "libs") == nil {
		t.Error("libc6_libs not indexed without a chisel.yaml")
	}
}

// TestIndex_StorePrefixV4 verifies that a store-backed package is indexed under
// its unique, prefixed name, which is what slice references use.
func TestIndex_StorePrefixV4(t *testing.T) {
	dir := writeReleaseDirs(t, map[string]string{
		"chisel.yaml":       chiselYamlV4,
		"slices/curl.yaml":  curlStorePkg,
		"slices/libc6.yaml": libc6,
	})
	idx, err := index.New(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if idx.LookupSlice("bin-curl", "bins") == nil {
		t.Errorf("bin-curl_bins not indexed; refs: %v", idx.AllSliceRefs())
	}
	// The unprefixed name must not resolve: it is not the unique package name.
	if idx.LookupSlice("curl", "bins") != nil {
		t.Error("unprefixed 'curl' must not be indexed for a store package")
	}
	if !idx.PackageExists("bin-curl") {
		t.Error("PackageExists(bin-curl) = false")
	}
	if got := idx.PackageName(filepath.Join(dir, "slices", "curl.yaml")); got != "bin-curl" {
		t.Errorf("PackageName: got %q, want %q", got, "bin-curl")
	}
	// A regular archive package keeps its plain name.
	if got := idx.PackageName(filepath.Join(dir, "slices", "libc6.yaml")); got != "libc6" {
		t.Errorf("PackageName for archive package: got %q, want %q", got, "libc6")
	}
}

// TestIndex_BinSlicesDirV3 verifies that in format v3 the segregated
// bin-slices/ directory is indexed too.
func TestIndex_BinSlicesDirV3(t *testing.T) {
	dir := writeReleaseDirs(t, map[string]string{
		"chisel.yaml":          chiselYamlV3,
		"bin-slices/curl.yaml": curlStorePkg,
		"slices/libc6.yaml":    libc6,
	})
	idx, err := index.New(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if idx.LookupSlice("bin-curl", "bins") == nil {
		t.Errorf("bin-curl_bins from bin-slices/ not indexed; refs: %v", idx.AllSliceRefs())
	}
	if idx.LookupSlice("libc6", "libs") == nil {
		t.Error("libc6_libs from slices/ not indexed")
	}
}

// TestIndex_BinSlicesIgnoredInV4 verifies that from format v4 onwards store
// definitions live in slices/, so bin-slices/ is not read.
func TestIndex_BinSlicesIgnoredInV4(t *testing.T) {
	dir := writeReleaseDirs(t, map[string]string{
		"chisel.yaml":          chiselYamlV4,
		"bin-slices/curl.yaml": curlStorePkg,
	})
	idx, err := index.New(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if idx.LookupSlice("bin-curl", "bins") != nil {
		t.Errorf("bin-slices/ must not be read in format v4; refs: %v", idx.AllSliceRefs())
	}
}

// TestIndex_UpdateRelease verifies that replacing the release definition with an
// unsaved buffer re-resolves package names, since the store prefix comes from it.
func TestIndex_UpdateRelease(t *testing.T) {
	dir := writeReleaseDirs(t, map[string]string{
		"chisel.yaml":      chiselYamlV4,
		"slices/curl.yaml": curlStorePkg,
	})
	idx, err := index.New(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if idx.LookupSlice("bin-curl", "bins") == nil {
		t.Fatal("bin-curl_bins not indexed initially")
	}

	// Change the store prefix; the package must be re-indexed under the new name.
	newYaml := `format: v4
stores:
  bin:
    kind: bin
    version: 26.10
    default-prefix: "store-"
`
	if err := idx.UpdateRelease([]byte(newYaml)); err != nil {
		t.Fatal(err)
	}
	if idx.LookupSlice("store-curl", "bins") == nil {
		t.Errorf("store-curl_bins not indexed after prefix change; refs: %v", idx.AllSliceRefs())
	}
	if idx.LookupSlice("bin-curl", "bins") != nil {
		t.Error("stale bin-curl entry left after prefix change")
	}
}

// TestIndex_UnknownStoreKeepsPlainName verifies that a package naming a store
// the release does not define keeps its plain name, so the rest of the index
// stays usable while the user is still editing chisel.yaml.
func TestIndex_UnknownStoreKeepsPlainName(t *testing.T) {
	dir := writeReleaseDirs(t, map[string]string{
		"chisel.yaml": "format: v4\n",
		"slices/curl.yaml": `package: curl
store: bin
default-track: "3.0"
slices:
  bins:
    contents:
      /usr/bin/curl:
`,
	})
	idx, err := index.New(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if idx.LookupSlice("curl", "bins") == nil {
		t.Errorf("curl_bins not indexed under its plain name; refs: %v", idx.AllSliceRefs())
	}
}

func TestIndex_IsReleaseFile(t *testing.T) {
	dir := writeReleaseDirs(t, map[string]string{"chisel.yaml": chiselYamlV4})
	idx, err := index.New(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if !idx.IsReleaseFile(filepath.Join(dir, "chisel.yaml")) {
		t.Error("IsReleaseFile(chisel.yaml) = false")
	}
	if idx.IsReleaseFile(filepath.Join(dir, "slices", "curl.yaml")) {
		t.Error("IsReleaseFile(slice file) = true")
	}
	if idx.Root() != dir {
		t.Errorf("Root: got %q, want %q", idx.Root(), dir)
	}
}
