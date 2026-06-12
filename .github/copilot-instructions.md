# Copilot Instructions — chisel-releases-lsp

## Build & Test

`go` is installed as a snap; prefix every command with `snap run`:

```bash
# Build binary
snap run go build ./cmd/chisel-releases-lsp

# Run all tests
snap run go test ./...

# Run one package
snap run go test ./internal/lsp/...

# Run a single test by name
snap run go test -run TestComputeDiagnostics_UnknownRef ./internal/lsp/...

# Coverage
snap run go test -cover ./internal/lsp/...
```

**Full CI check (run all three before committing):**

```bash
# 1. Format all files in-place (gofmt is at /snap/go/current/bin/gofmt)
snap run go fmt ./...

# 2. Build + test
snap run go build ./...
snap run go test ./...

# 3. Lint (golangci-lint v2.12.2 installed at ~/go/bin/golangci-lint)
PATH="$PATH:/snap/bin" ~/go/bin/golangci-lint run ./...
```

This mirrors exactly what `.github/workflows/ci.yml` runs.

---

## Architecture

```
parser  →  index  →  analysis  (stateless passes)
                 ↘  lsp        (glsp handlers, one file per feature)
```

### `internal/parser`
Position-aware YAML parser built on the `gopkg.in/yaml.v3` Node API. Every token carries a `Range{Start, End Position}` with **0-based line and byte-offset character** values (not UTF-16 — known limitation). Key types: `SliceFile`, `SliceDef`, `EssentialRef`, `ContentEntry`.

`SliceRefFromToken(token)` splits a `pkg_slice` string using `strings.LastIndex("_")` to handle packages that contain underscores (e.g. `libc6-utils_libs` → `(libc6-utils, libs)`). Never use `strings.SplitN` with `"_"` to split these.

### `internal/index`
In-memory index of all `slices/*.yaml` files under the workspace root. Protected by `sync.RWMutex`. Two internal maps:
- `slices[pkg][sliceName]*IndexedSlice` — for lookup by identity
- `files[path]*SliceFile` — for lookup by file path

`index.New(root, onChange, onDelete)` starts an `fsnotify` watcher in a background goroutine. `AllFiles()`, `AllSliceRefs()`, and `FindReferences()` return **sorted** slices for deterministic output.

### `internal/analysis`
Stateless analysis passes that return structured diagnostics (not `protocol.Diagnostic` — those are constructed in `internal/lsp/diagnostics.go`):
- `ValidateGlobs` — validates `contents:` paths
- `DetectCollisions` — cross-file concrete-path conflicts
- `CheckPackageName` — filename stem must equal `package:` value
- `DetectDuplicateSlices` — same `pkg_slice` defined in more than one file
- `CheckCopyrightEssential` — each slice must reference `<pkg>_copyright` in effective essentials; copyright slice is exempt

### `internal/lsp`
glsp-based server. `Server` struct owns the index, open-document map, and a `Notifier`. Features are split into files: `completion.go`, `definition.go`, `hover.go`, `references.go`, `rename.go`, `symbol.go`, `codeaction.go`, `diagnostics.go`, `util.go`.

---

## Key Conventions

### Testing pattern

Handler functions take `*glsp.Context` (fixed by glsp) and **cannot** be unit-tested directly. Instead, each feature exposes an internal helper (e.g. `computeCodeActions`, `reindexAndPublish`) that takes plain arguments and is callable from tests.

The white-box bridge lives in `internal/lsp/export_test.go` (`package lsp`, file name ends in `_test.go`). Add `ExportXxx` wrapper functions there whenever a new internal helper needs test coverage. Tests live in `internal/lsp/lsp_test.go` (`package lsp_test`).

Common test helpers:
```go
// Create a temp release dir with files, get *index.Index + slices path
idx, slicesDir := setupLSPIndex(t, map[string]string{"libc6.yaml": content})

// Create a Server with a pre-populated index (no glsp server)
srv := lsp.NewWithIndex(idx)

// Inject open-document text (simulates textDocument/didOpen)
srv.SetDocForTest(filePath, content)

// Capture publishDiagnostics notifications
n := &recordingNotifier{}
```

### Notifier interface

All internal helpers that send LSP notifications take a `Notifier` interface (`util.go`), never a raw `*glsp.Context`. The `ctxNotifier{ctx}` adapter is nil-safe. The `recordingNotifier` test double captures calls.

`storeNotifier(ctx)` **must** be called in `initialize` before `index.New()` — the file-watcher goroutine calls `publishDiagnosticsBackground` which reads `s.notifier`.

### Diagnostic codes

All emitted diagnostics carry a string `Code` (constants in `util.go`):

| Constant | Value |
|----------|-------|
| `DiagCodePackageNameMismatch` | `"package-name-mismatch"` |
| `DiagCodeInvalidGlob` | `"invalid-glob"` |
| `DiagCodeSliceCollision` | `"slice-collision"` |
| `DiagCodeInvalidSliceRef` | `"invalid-slice-ref"` |
| `DiagCodeUnknownSliceRef` | `"unknown-slice-ref"` |

`textDocument/codeAction` uses `diag.Code.Value.(string)` to select actions — always set codes when adding new diagnostics.

### `resolveRefTarget`

Shared by `references.go` and `rename.go`. Handles two cursor contexts:
- Cursor on an essential ref (`- libc6_libs`) → `SliceRefFromToken` → `(pkg, slice)`
- Cursor on a slice definition key (`libs:`) → pair bare token with `sf.Package`

### Adding a new LSP feature

1. Create `internal/lsp/<feature>.go` with the handler function.
2. Register it in `Server.handler` in `New()` (`server.go`).
3. Declare the capability in `initialize()` (`server.go`).
4. Add `ExportXxx` wrappers to `export_test.go`.
5. Add tests to `lsp_test.go` using `NewWithIndex` + `SetDocForTest`.
6. **Update `README.md`**: add a row to the features table, describe the behaviour in the "How it works" section, and tick the item in the Roadmap. Do this in the same commit as the feature.

### Line deletion in code actions

`fullLineDeleteRange(lines, lineNum)` in `codeaction.go` produces the correct `protocol.Range` for removing an entire line including its newline. It handles the last-line-without-trailing-newline edge case. Use it (not a hand-rolled range) whenever a code action needs to delete a whole line.
