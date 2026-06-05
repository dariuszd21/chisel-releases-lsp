package index_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/canonical/chisel-releases-lsp/internal/index"
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

	idx, err := index.New(dir, nil)
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

func TestIndexWatch(t *testing.T) {
	dir := writeRelease(t, map[string]string{"openssl.yaml": openssl})
	changed := make(chan string, 1)
	idx, err := index.New(dir, func(p string) { changed <- p })
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
