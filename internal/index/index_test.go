package index_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/canonical/chisel-releases-lsp/internal/index"
	"github.com/canonical/chisel-releases-lsp/internal/parser"
)

const openssl = `
package: openssl
slices:
  bins:
    essential:
      - libc6_libs
    contents:
      /usr/bin/openssl:
  config:
    contents:
      /etc/ssl/openssl.cnf:
`

const libc6 = `
package: libc6
slices:
  libs:
    contents:
      /lib/x86_64-linux-gnu/libc.so.6:
`

func writeRelease(t *testing.T, files map[string]string) string {
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
	return dir
}

func TestIndexLoad(t *testing.T) {
	dir := writeRelease(t, map[string]string{
		"openssl.yaml": openssl,
		"libc6.yaml":   libc6,
	})

	idx, err := index.New(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	is := idx.LookupSlice("openssl", "bins")
	if is == nil {
		t.Fatal("openssl_bins not found")
	}
	if is.Def.Name != "bins" {
		t.Errorf("slice name: got %q", is.Def.Name)
	}

	refs := idx.AllSliceRefs()
	want := map[string]bool{
		"openssl_bins": true, "openssl_config": true, "libc6_libs": true,
	}
	for _, r := range refs {
		delete(want, r)
	}
	if len(want) != 0 {
		t.Errorf("missing refs: %v", want)
	}
}

func TestAllSliceRefsSorted(t *testing.T) {
	dir := writeRelease(t, map[string]string{
		"openssl.yaml": openssl,
		"libc6.yaml":   libc6,
	})

	idx, err := index.New(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	refs := idx.AllSliceRefs()
	if !sort.StringsAreSorted(refs) {
		t.Errorf("AllSliceRefs not sorted: %v", refs)
	}
}

func TestUpdateFile(t *testing.T) {
	dir := writeRelease(t, map[string]string{"openssl.yaml": openssl})

	idx, err := index.New(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	// openssl_bins exists; openssl_extra does not yet.
	if idx.LookupSlice("openssl", "extra") != nil {
		t.Fatal("extra slice should not exist before update")
	}

	// Update with new in-memory content.
	sf := parseYAML(t, `
package: openssl
slices:
  extra:
    contents:
      /usr/bin/extra:
`)

	absPath := filepath.Join(dir, "slices", "openssl.yaml")
	idx.UpdateFile(absPath, sf)

	if idx.LookupSlice("openssl", "extra") == nil {
		t.Error("extra slice not found after UpdateFile")
	}
	// Old slices removed.
	if idx.LookupSlice("openssl", "bins") != nil {
		t.Error("bins slice should be gone after UpdateFile")
	}
}

func TestAllFilesAndFileSliceFile(t *testing.T) {
	dir := writeRelease(t, map[string]string{
		"openssl.yaml": openssl,
		"libc6.yaml":   libc6,
	})

	idx, err := index.New(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	files := idx.AllFiles()
	if len(files) != 2 {
		t.Fatalf("AllFiles: got %d, want 2", len(files))
	}
	for _, f := range files {
		sf := idx.FileSliceFile(f)
		if sf == nil {
			t.Errorf("FileSliceFile(%q) returned nil", f)
		}
	}
}

func TestIndexWatch(t *testing.T) {
	dir := writeRelease(t, map[string]string{"openssl.yaml": openssl})
	changed := make(chan string, 1)
	idx, err := index.New(dir, func(p string) { changed <- p }, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	// Write a new file
	newFile := filepath.Join(dir, "slices", "libc6.yaml")
	if err := os.WriteFile(newFile, []byte(libc6), 0644); err != nil {
		t.Fatal(err)
	}

	select {
	case <-changed:
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not fire")
	}

	if idx.LookupSlice("libc6", "libs") == nil {
		t.Error("libc6_libs not indexed after watch event")
	}
}

func TestIndexWatchDelete(t *testing.T) {
	dir := writeRelease(t, map[string]string{
		"openssl.yaml": openssl,
		"libc6.yaml":   libc6,
	})
	deleted := make(chan string, 1)
	idx, err := index.New(dir, nil, func(p string) { deleted <- p })
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if idx.LookupSlice("libc6", "libs") == nil {
		t.Fatal("libc6_libs should exist before delete")
	}

	if err := os.Remove(filepath.Join(dir, "slices", "libc6.yaml")); err != nil {
		t.Fatal(err)
	}

	select {
	case <-deleted:
	case <-time.After(3 * time.Second):
		t.Fatal("delete watcher did not fire")
	}

	if idx.LookupSlice("libc6", "libs") != nil {
		t.Error("libc6_libs should be gone after file deletion")
	}
}

// parseYAML is a test helper that parses YAML string into a SliceFile.
func parseYAML(t *testing.T, yaml string) *parser.SliceFile {
	t.Helper()
	sf, err := parser.ParseBytes([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	return sf
}

func TestAllFilesSorted(t *testing.T) {
dir := writeRelease(t, map[string]string{
"openssl.yaml": openssl,
"libc6.yaml":   libc6,
})

idx, err := index.New(dir, nil, nil)
if err != nil {
t.Fatal(err)
}
defer idx.Close()

files := idx.AllFiles()
if !sort.StringsAreSorted(files) {
t.Errorf("AllFiles() not sorted: %v", files)
}
if len(files) != 2 {
t.Errorf("expected 2 files, got %d: %v", len(files), files)
}
}
