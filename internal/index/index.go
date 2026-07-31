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

	"github.com/dariuszd21/chisel-releases-lsp/internal/parser"
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
	files map[string]*parser.SliceFile
	// pkgNames maps absolute file path → the unique package name of that file,
	// which is the `package:` value prefixed with the store's default-prefix for
	// store-backed packages (e.g. "curl" in a "bin" store becomes "bin-curl").
	pkgNames map[string]string
	// release holds the parsed chisel.yaml of the workspace root, or nil when
	// the root has no chisel.yaml (or it could not be parsed).
	release *parser.Release
	// root is the release root directory (the one holding chisel.yaml).
	root    string
	watcher *fsnotify.Watcher
	// onChange is called (on a goroutine) after a file is created or updated.
	onChange func(filePath string)
	// onDelete is called (on a goroutine) after a file is removed from the index.
	onDelete func(filePath string)
}

// New creates an Index for the given release root (directory containing
// `chisel.yaml` and `slices/`). In format v3 the additional `bin-slices/`
// directory, which holds store-backed package definitions, is indexed and
// watched as well.
// onChange is invoked after each file create/update; onDelete after each removal.
// Either callback may be nil.
func New(releaseRoot string, onChange func(filePath string), onDelete func(filePath string)) (*Index, error) {
	idx := &Index{
		slices:   make(map[string]map[string]*IndexedSlice),
		files:    make(map[string]*parser.SliceFile),
		pkgNames: make(map[string]string),
		root:     releaseRoot,
		onChange: onChange,
		onDelete: onDelete,
	}

	idx.loadRelease()

	for _, dir := range idx.sliceDirs() {
		if err := idx.loadAll(dir); err != nil {
			return nil, err
		}
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("creating watcher: %w", err)
	}
	idx.watcher = w

	for _, dir := range idx.sliceDirs() {
		if _, statErr := os.Stat(dir); statErr != nil {
			continue
		}
		if err := w.Add(dir); err != nil {
			return nil, fmt.Errorf("watching %s: %w", dir, err)
		}
	}
	// Watch the release root too: a change to chisel.yaml alters the format and
	// the store prefixes, and therefore the whole index.
	if _, statErr := os.Stat(releaseRoot); statErr == nil {
		if err := w.Add(releaseRoot); err != nil {
			return nil, fmt.Errorf("watching %s: %w", releaseRoot, err)
		}
	}

	go idx.watchLoop()
	return idx, nil
}

// sliceDirs returns the absolute directories that hold slice definition files.
// It is always "slices/", plus "bin-slices/" in the formats that keep
// store-backed definitions apart (v3).
func (idx *Index) sliceDirs() []string {
	dirs := []string{filepath.Join(idx.root, "slices")}
	if storeDir := idx.Release().StoreSlicesDir(); storeDir != "slices" {
		dirs = append(dirs, filepath.Join(idx.root, storeDir))
	}
	return dirs
}

// loadRelease parses chisel.yaml from the release root. A missing or malformed
// chisel.yaml is not fatal: the index then behaves as if the release used the
// oldest format, so no format-gated diagnostic is ever reported.
func (idx *Index) loadRelease() {
	rel, err := parser.ParseReleaseFile(idx.ReleaseFilePath())
	if err != nil {
		return
	}
	idx.mu.Lock()
	idx.release = rel
	idx.mu.Unlock()
}

// Release returns the parsed chisel.yaml of the release, or nil when the
// workspace root has none.
func (idx *Index) Release() *parser.Release {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.release
}

// Root returns the release root directory.
func (idx *Index) Root() string { return idx.root }

// ReleaseFilePath returns the absolute path of the release's chisel.yaml.
func (idx *Index) ReleaseFilePath() string {
	return filepath.Join(idx.root, "chisel.yaml")
}

// IsReleaseFile reports whether absPath is the release's chisel.yaml.
func (idx *Index) IsReleaseFile(absPath string) bool {
	return absPath == idx.ReleaseFilePath()
}

// ReloadRelease re-parses chisel.yaml and re-indexes every slice file, which is
// required because the format and the store prefixes both affect how package
// names resolve.
func (idx *Index) ReloadRelease() {
	idx.loadRelease()
	idx.reindexAllSliceFiles()
}

// UpdateRelease replaces the release definition with one parsed from content
// (used for the unsaved editor buffer of chisel.yaml) and re-indexes every
// slice file. It returns the parse error, if any, leaving the previous release
// in place in that case.
func (idx *Index) UpdateRelease(content []byte) error {
	rel, err := parser.ParseReleaseBytes(content)
	if err != nil {
		return err
	}
	idx.mu.Lock()
	idx.release = rel
	idx.mu.Unlock()
	idx.reindexAllSliceFiles()
	return nil
}

// reindexAllSliceFiles drops and reloads every slice file from disk. It also
// re-syncs the watches, because a format change moves store-backed definitions
// between "slices/" and "bin-slices/".
func (idx *Index) reindexAllSliceFiles() {
	idx.mu.Lock()
	paths := make([]string, 0, len(idx.files))
	for p := range idx.files {
		paths = append(paths, p)
	}
	for _, p := range paths {
		idx.removeFile(p)
	}
	idx.mu.Unlock()
	for _, dir := range idx.sliceDirs() {
		_ = idx.loadAll(dir)
	}
	idx.syncWatches()
}

// syncWatches makes the watcher observe exactly the current slice directories
// plus the release root. Adding an already-watched path is a no-op in fsnotify,
// so this is safe to call repeatedly.
func (idx *Index) syncWatches() {
	if idx.watcher == nil {
		return
	}
	want := map[string]bool{idx.root: true}
	for _, dir := range idx.sliceDirs() {
		want[dir] = true
	}
	for _, dir := range idx.watcher.WatchList() {
		if !want[dir] {
			_ = idx.watcher.Remove(dir)
		}
	}
	for dir := range want {
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		_ = idx.watcher.Add(dir)
	}
}

// PackageName returns the unique package name of an indexed file, which for
// store-backed packages includes the store's default-prefix. It falls back to
// the raw `package:` value when the file is not indexed.
func (idx *Index) PackageName(absPath string) string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	if name, ok := idx.pkgNames[absPath]; ok {
		return name
	}
	if sf, ok := idx.files[absPath]; ok {
		return sf.Package
	}
	return ""
}

// resolvePackageName returns the unique package name for a slice file: the
// `package:` value, prefixed with the store's default-prefix when the file
// declares a `store:` that the release defines. Caller must hold mu.
func (idx *Index) resolvePackageName(sf *parser.SliceFile) string {
	if sf.Package == "" || sf.Store == "" {
		return sf.Package
	}
	if idx.release == nil {
		return sf.Package
	}
	store := idx.release.Stores[sf.Store]
	if store == nil {
		return sf.Package
	}
	return store.DefaultPrefix + sf.Package
}

// Close stops the file watcher.
func (idx *Index) Close() {
	if idx.watcher != nil {
		_ = idx.watcher.Close()
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

// IndexedRef is one occurrence of a pkg_slice reference inside an essential list.
type IndexedRef struct {
	File  string       // absolute path to the .yaml file containing the reference
	Range parser.Range // position of the reference value within that file
}

// FindReferences returns all essential-list occurrences of pkg_slice across the
// entire index, sorted by file path then line number.  Both top-level essential
// (on the SliceFile) and per-slice essential entries are included.
func (idx *Index) FindReferences(pkg, sliceName string) []IndexedRef {
	target := pkg + "_" + sliceName
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	// Collect sorted file paths for deterministic output.
	paths := make([]string, 0, len(idx.files))
	for p := range idx.files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var refs []IndexedRef
	for _, filePath := range paths {
		sf := idx.files[filePath]
		for _, e := range sf.Essential {
			if e.Value == target {
				refs = append(refs, IndexedRef{File: filePath, Range: e.ValueRange})
			}
		}
		for _, sliceName := range sf.SliceOrder {
			sd := sf.Slices[sliceName]
			for _, e := range sd.Essential {
				if e.Value == target {
					refs = append(refs, IndexedRef{File: filePath, Range: e.ValueRange})
				}
			}
		}
	}
	return refs
}

// PackageExists reports whether any slice file in the index declares the given
// package name.
func (idx *Index) PackageExists(pkg string) bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	_, ok := idx.slices[pkg]
	return ok
}

// AllFiles returns all currently indexed file paths in sorted order.
func (idx *Index) AllFiles() []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	out := make([]string, 0, len(idx.files))
	for p := range idx.files {
		out = append(out, p)
	}
	sort.Strings(out)
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

// DeleteFile removes all index entries for absPath. It is a no-op when the
// file is not currently indexed.
func (idx *Index) DeleteFile(absPath string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.removeFile(absPath)
}

// applySliceFile replaces the index entries for absPath with sf. Caller must hold mu.
func (idx *Index) applySliceFile(absPath string, sf *parser.SliceFile) {
	idx.removeFile(absPath)
	idx.files[absPath] = sf
	pkgName := idx.resolvePackageName(sf)
	if pkgName == "" {
		return
	}
	idx.pkgNames[absPath] = pkgName
	if idx.slices[pkgName] == nil {
		idx.slices[pkgName] = make(map[string]*IndexedSlice)
	}
	for name, sd := range sf.Slices {
		idx.slices[pkgName][name] = &IndexedSlice{
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
	pkgName := idx.pkgNames[absPath]
	delete(idx.pkgNames, absPath)
	if pkgName == "" {
		return
	}
	pkgMap := idx.slices[pkgName]
	for name := range sf.Slices {
		delete(pkgMap, name)
	}
	if len(pkgMap) == 0 {
		delete(idx.slices, pkgName)
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

func (idx *Index) watchLoop() {
	for {
		select {
		case event, ok := <-idx.watcher.Events:
			if !ok {
				return
			}
			if !strings.HasSuffix(event.Name, ".yaml") {
				continue
			}
			if idx.IsReleaseFile(event.Name) {
				idx.ReloadRelease()
				if idx.onChange != nil {
					go idx.onChange(event.Name)
				}
				continue
			}
			// Ignore other YAML files sitting directly in the release root;
			// only slice directories hold slice definitions.
			if filepath.Dir(event.Name) == idx.root {
				continue
			}
			switch {
			case event.Has(fsnotify.Create) || event.Has(fsnotify.Write):
				_ = idx.IndexFile(event.Name)
				if idx.onChange != nil {
					go idx.onChange(event.Name)
				}
			case event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename):
				idx.mu.Lock()
				idx.removeFile(event.Name)
				idx.mu.Unlock()
				if idx.onDelete != nil {
					go idx.onDelete(event.Name)
				}
			}
		case _, ok := <-idx.watcher.Errors:
			if !ok {
				return
			}
		}
	}
}
