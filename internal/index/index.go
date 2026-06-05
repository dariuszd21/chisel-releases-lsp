// Package index maintains an in-memory index of all slice definitions within a
// chisel release directory (one that contains a `slices/` subdirectory). It
// watches the slices directory for changes and automatically re-indexes on any
// add/modify/delete event.
package index

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/canonical/chisel-releases-lsp/internal/parser"
	"github.com/fsnotify/fsnotify"
)

// IndexedSlice is a slice definition plus the path to its source file.
type IndexedSlice struct {
	File         string // absolute path to the .yaml file
	Def          *parser.SliceDef
	PackageRange parser.Range // range of the `package:` value in the file
}

// Index holds the in-memory view of all slices in a chisel release.
type Index struct {
	mu sync.RWMutex
	// slices maps pkg → slice-name → IndexedSlice
	slices map[string]map[string]*IndexedSlice
	// files maps absolute file path → *parser.SliceFile
	files   map[string]*parser.SliceFile
	watcher *fsnotify.Watcher
	// onChange is called after any re-index with the affected file path.
	onChange func(filePath string)
}

// New creates an Index for the given release root (directory containing `slices/`).
// onChange, if non-nil, is invoked (on a goroutine) after each file re-index.
func New(releaseRoot string, onChange func(filePath string)) (*Index, error) {
	idx := &Index{
		slices:   make(map[string]map[string]*IndexedSlice),
		files:    make(map[string]*parser.SliceFile),
		onChange: onChange,
	}

	slicesDir := filepath.Join(releaseRoot, "slices")
	if err := idx.loadAll(slicesDir); err != nil {
		return nil, err
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("creating watcher: %w", err)
	}
	idx.watcher = w

	if _, statErr := os.Stat(slicesDir); statErr == nil {
		if err := w.Add(slicesDir); err != nil {
			return nil, fmt.Errorf("watching %s: %w", slicesDir, err)
		}
	}

	go idx.watchLoop(slicesDir)
	return idx, nil
}

// Close stops the file watcher.
func (idx *Index) Close() {
	if idx.watcher != nil {
		idx.watcher.Close()
	}
}

// LookupSlice returns the IndexedSlice for pkg+slice, or nil if not found.
func (idx *Index) LookupSlice(pkg, slice string) *IndexedSlice {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	if pkgMap, ok := idx.slices[pkg]; ok {
		return pkgMap[slice]
	}
	return nil
}

// AllSliceRefs returns all known "pkg_slice" strings in alphabetical order.
func (idx *Index) AllSliceRefs() []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	var refs []string
	for pkg, sliceMap := range idx.slices {
		for sliceName := range sliceMap {
			refs = append(refs, pkg+"_"+sliceName)
		}
	}
	sort.Strings(refs)
	return refs
}

// AllFiles returns all currently indexed file paths.
func (idx *Index) AllFiles() []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	out := make([]string, 0, len(idx.files))
	for p := range idx.files {
		out = append(out, p)
	}
	return out
}

// FileSliceFile returns the parsed SliceFile for an absolute file path.
func (idx *Index) FileSliceFile(absPath string) *parser.SliceFile {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.files[absPath]
}

// UpdateFile updates the index with an already-parsed SliceFile (used for
// live/unsaved content from the editor).
func (idx *Index) UpdateFile(absPath string, sf *parser.SliceFile) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.applySliceFile(absPath, sf)
}

// IndexFile (re-)parses and indexes a single file.
func (idx *Index) IndexFile(absPath string) error {
	sf, err := parser.ParseFile(absPath)
	if err != nil {
		// On parse error, remove stale data so diagnostics reflect the broken state.
		idx.mu.Lock()
		idx.removeFile(absPath)
		idx.mu.Unlock()
		return err
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.applySliceFile(absPath, sf)
	return nil
}

// applySliceFile replaces the index entries for absPath with sf. Caller must hold mu.
func (idx *Index) applySliceFile(absPath string, sf *parser.SliceFile) {
	idx.removeFile(absPath)
	idx.files[absPath] = sf
	if sf.Package == "" {
		return
	}
	if idx.slices[sf.Package] == nil {
		idx.slices[sf.Package] = make(map[string]*IndexedSlice)
	}
	for name, sd := range sf.Slices {
		idx.slices[sf.Package][name] = &IndexedSlice{
			File:         absPath,
			Def:          sd,
			PackageRange: sf.PackageRange,
		}
	}
}

// removeFile removes all index entries associated with a file. Caller must hold mu.
func (idx *Index) removeFile(absPath string) {
	sf, ok := idx.files[absPath]
	if !ok {
		return
	}
	delete(idx.files, absPath)
	if sf.Package == "" {
		return
	}
	pkgMap := idx.slices[sf.Package]
	for name := range sf.Slices {
		delete(pkgMap, name)
	}
	if len(pkgMap) == 0 {
		delete(idx.slices, sf.Package)
	}
}

func (idx *Index) loadAll(slicesDir string) error {
	entries, err := os.ReadDir(slicesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no slices dir yet
		}
		return fmt.Errorf("reading slices dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		absPath := filepath.Join(slicesDir, e.Name())
		if err := idx.IndexFile(absPath); err != nil {
			// Log and continue – a single bad file shouldn't block the whole index.
			fmt.Fprintf(os.Stderr, "chisel-releases-lsp: index warning: %s: %v\n", absPath, err)
		}
	}
	return nil
}

func (idx *Index) watchLoop(slicesDir string) {
	for {
		select {
		case event, ok := <-idx.watcher.Events:
			if !ok {
				return
			}
			if !strings.HasSuffix(event.Name, ".yaml") {
				continue
			}
			switch {
			case event.Has(fsnotify.Create) || event.Has(fsnotify.Write):
				_ = idx.IndexFile(event.Name)
			case event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename):
				idx.mu.Lock()
				idx.removeFile(event.Name)
				idx.mu.Unlock()
			}
			if idx.onChange != nil {
				go idx.onChange(event.Name)
			}
		case _, ok := <-idx.watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

