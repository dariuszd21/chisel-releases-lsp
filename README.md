# chisel-releases-lsp

A [Language Server Protocol](https://microsoft.github.io/language-server-protocol/) (LSP) server for [chisel](https://github.com/canonical/chisel) slice definition files.

`chisel-releases-lsp` is a tool-agnostic developer companion for working with [chisel-releases](https://github.com/canonical/chisel-releases). It plugs into any LSP-capable editor (Neovim, VS Code, Emacs, Helix, …) and provides real-time feedback while you write or review slice definitions.

---

## Features

| Feature | LSP method |
|---------|-----------|
| **Slice completions** — suggest `<pkg>_<slice>` in `essential:` lists | `textDocument/completion` |
| **Jump to definition** — go to the slice key in its `.yaml` file | `textDocument/definition` |
| **Find references** — list all `essential:` entries that reference a slice | `textDocument/references` |
| **Rename** — rename a slice across its definition and all references | `textDocument/rename` |
| **Quick fixes** — remove unknown/invalid references; fix package name mismatches | `textDocument/codeAction` |
| **Document symbols** — outline view showing the package and all its slices | `textDocument/documentSymbol` |
| **Workspace symbols** — search all `pkg_slice` names across the release | `workspace/symbol` |
| **Glob pattern validation** — flag invalid `contents:` paths | `textDocument/publishDiagnostics` |
| **Slice collision detection** — warn when two packages claim the same concrete path; suppressed when the packages are in the same linear `prefer:` chain | `textDocument/publishDiagnostics` |
| **Glob collision detection** — warn when a glob pattern in one package matches a concrete path declared in another | `textDocument/publishDiagnostics` |
| **Redundant path detection** — warn when an exact path in a slice is already covered by a glob in the same slice | `textDocument/publishDiagnostics` |
| **`prefer:` validation** — flag `prefer:` on globs, self-references, cycles, and unknown packages | `textDocument/publishDiagnostics` |
| **Lexical sort check** — warn when `contents:` paths or `essential:` entries are not in lexical order | `textDocument/publishDiagnostics` |
| **Unknown reference warnings** — warn on `essential:` entries that don't exist | `textDocument/publishDiagnostics` |
| **Package name check** — warn when `package:` value doesn't match the filename stem | `textDocument/publishDiagnostics` |
| **Hover documentation** — show a slice's contents and essential dependencies | `textDocument/hover` |
| **Duplicate slice detection** — warn when the same `pkg_slice` is defined in more than one file | `textDocument/publishDiagnostics` |
| **Missing copyright essential** — warn when a slice doesn't reference `<pkg>_copyright` | `textDocument/publishDiagnostics` |
| **Duplicate essential references** — warn when the same `pkg_slice` appears more than once in the same `essential:` block | `textDocument/publishDiagnostics` |

---

## Installation

```bash
go install github.com/dariuszd21/chisel-releases-lsp/cmd/chisel-releases-lsp@latest
```

Or build from source:

```bash
git clone https://github.com/dariuszd21/chisel-releases-lsp
cd chisel-releases-lsp
go build -o chisel-releases-lsp ./cmd/chisel-releases-lsp
```

---

## Usage

The server communicates over **stdio** and is started automatically by your editor's LSP client. Configure your editor to:

1. Run `chisel-releases-lsp` as the language server command.
2. Associate it with YAML files inside a `slices/` directory (or all `*.yaml` files in chisel-releases workspaces).
3. Set the workspace root to the chisel release directory (the one containing `chisel.yaml` and `slices/`).

### Neovim 0.11+

```lua
vim.lsp.config('chisel_releases_lsp', {
  cmd = { 'chisel-releases-lsp' },
  filetypes = { 'yaml' },
  root_markers = { 'chisel.yaml' },
})
vim.lsp.enable('chisel_releases_lsp')
```

To override settings, pass them in the same `vim.lsp.config` call:

```lua
vim.lsp.config('chisel_releases_lsp', {
  settings = { minPrefixLength = 3 },
})
```

### VS Code

Install the [crl-vscode](https://github.com/dariuszd21/crl-vscode) extension, which provides out-of-the-box integration with `chisel-releases-lsp` for YAML files in chisel release directories.

To override settings, add to `settings.json`:

```json
{
  "chiselReleasesLsp.minPrefixLength": 3
}
```

### Helix

Add the following to `~/.config/helix/languages.toml`:

```toml
[language-server.chisel-releases-lsp]
command = "chisel-releases-lsp"

# Optional: override default settings
config = { minPrefixLength = 3 }

[[language]]
name = "yaml"
language-servers = [
  "yaml-language-server",
  "chisel-releases-lsp"
]

roots = ["chisel.yaml"]
```

---

## How it works

The server loads all `slices/*.yaml` files from the workspace root into an in-memory index on startup, then watches the directory for changes. As you edit:

- **Completions** are offered whenever the cursor is inside an `essential:` list item.
- **Go to definition** resolves `<pkg>_<slice>` tokens to the exact line in `slices/<pkg>.yaml`.
- **Document symbols** (`Ctrl+Shift+O` in most editors) shows a package/slice outline for the current file.
- **Workspace symbols** (`Ctrl+T` / `@` in most editors) lets you fuzzy-search any `pkg_slice` name across the entire release.
- **Find references** (`Shift+F12` or equivalent) lists every `essential:` entry that names a given slice, across all files.
- **Rename** (`F2`) renames a slice key in its definition file and updates every `essential:` reference across the release.
- **Diagnostics** are published on open, change, and save:
  - Invalid `contents:` paths (must be absolute; `?`, `*`, `**` are the only wildcards).
  - Cross-package path collisions (two packages claiming the same concrete path). Each diagnostic includes `relatedInformation` pointing to the conflicting file. Collisions are suppressed when the two packages are part of the same linear `prefer:` chain — directly (`B` prefers `A`) or transitively (`C→B→A` suppresses all three pairs). Fan-in is not suppressed: if both `B` and `C` prefer `A` independently, the `B`-`C` collision is still reported because they are not ordered relative to each other and conflict if installed without `A`.
  - Glob-exact cross-package collisions: a glob pattern in one package that matches a concrete path declared in another. Both the glob and the exact-path side receive a diagnostic with `relatedInformation` pointing to the other file. Prefer-chain suppression applies. `*` patterns are checked only within the same directory; `**` patterns are checked across directories.
  - Redundant exact paths: an exact path in a `contents:` block that is already covered by a glob in the same slice and carries no special attributes (`mode`, `text`, `copy`, …). A *Remove redundant path* quick fix is offered.
  - Invalid `prefer:` usage — `prefer:` on a glob path (error), `prefer:` naming the same package (error), a direct prefer cycle where both packages prefer each other on the same path (error), or `prefer:` naming a package that does not exist in the release (warning).
  - Entries in `contents:` or `essential:` blocks that are not in lexical order (warning), with a *Sort entries lexically* quick fix.
  - Unknown or malformed slice references in `essential:` lists.
  - `package:` value that does not match the file's name stem (e.g. `openssl.yaml` must declare `package: openssl`).
  - Duplicate slice definitions — the same `pkg_slice` key declared in more than one file.
  - Missing copyright essential — a slice that doesn't reference `<pkg>_copyright` in its effective essentials (package-level or slice-level); the `copyright` slice itself is exempt.
  - Duplicate essential references — the same `pkg_slice` listed more than once in the same `essential:` block; diagnostics include `relatedInformation` pointing to the first occurrence.
- **Quick fixes** (lightbulb / `Ctrl+.`) offer one-click corrections for unknown/invalid references, package name mismatches, a *Go to conflicting slice* action for path collisions, *Add `<pkg>_copyright` to package essentials* for the missing-copyright diagnostic (inserts into the top-level `essential:` block, covering all slices at once; format matches the file's existing v1/v2 or v3 style), and *Sort entries lexically* for out-of-order `contents:` or `essential:` blocks.
- **Hover** renders a markdown summary of a slice's contents and its own essential dependencies.
- **v3 format** (`essential:` as a YAML mapping with optional per-entry arch filters) is fully supported alongside the classic v1/v2 sequence format.

---

## Slice definition format

A slice definition file lives at `slices/<package>.yaml`.

**v1/v2 format** — `essential:` as a sequence:

```yaml
package: mypkg

essential:
  - mypkg_copyright   # top-level: applied to all slices in this package

slices:
  bins:
    essential:
      - libc6_libs    # this slice depends on libc6_libs
    contents:
      /usr/bin/mybin:
      /usr/bin/glob*:

  config:
    contents:
      /etc/mypkg.conf: {text: "# default config"}
      /etc/mypkg.d/:   {make: true}
```

**v3 format** — `essential:` as a mapping with optional per-entry arch filters:

```yaml
package: mypkg

slices:
  bins:
    essential:
      libc6_libs:               # unconditional dependency
      libgcc-s1_libs:
        arch: amd64             # only on amd64
    contents:
      /usr/bin/mybin:
```

See the [chisel documentation](https://documentation.ubuntu.com/chisel/en/latest/) for the full schema reference.

---

## Development

```bash
# Run tests
go test ./...

# Build
go build ./cmd/chisel-releases-lsp
```

> **Note (this repo):** `go` is installed as a snap. Prefix all commands with `snap run go …` and use `snap run go fmt ./...` instead of `gofmt`. See [`.github/copilot-instructions.md`](.github/copilot-instructions.md) for the exact CI check sequence.

### Project layout

```
cmd/chisel-releases-lsp/   # Entry point
internal/
  parser/                  # Position-aware YAML parser (gopkg.in/yaml.v3 Node API)
  index/                   # In-memory slice index + fsnotify watcher
  analysis/                # Content path validation + collision detection
  lsp/                     # LSP method handlers (glsp)
```

---

## Configuration

The server reads configuration from the `initializationOptions` object sent by
the editor during the LSP handshake, and updates settings at runtime via
`workspace/didChangeConfiguration`.

### Settings

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `minPrefixLength` | integer | `2` | Minimum number of characters the user must type in an `essential:` reference before completion items are offered.  The minimum accepted value is `2`; values below `2` are silently raised to `2`. |

Settings can be provided either at the top level or nested under a
`chiselReleasesLsp` key:

```json
// Flat (initializationOptions or settings root):
{ "minPrefixLength": 3 }

// Namespaced:
{ "chiselReleasesLsp": { "minPrefixLength": 3 } }
```

---

## Known Limitations

- **Character offsets use byte lengths, not UTF-16 code units.** The LSP specification requires character positions to be expressed in UTF-16 code units by default. `chisel-releases-lsp` currently uses byte lengths (`len(token)`). For ASCII-only package and slice names this makes no difference, but names containing multi-byte UTF-8 characters would produce off-by-one position errors in some editors.
- **`workspace/symbol` returns at most a few hundred items.** There is no pagination; for very large chisel-releases trees with hundreds of packages this could be slow. In practice canonical/chisel-releases has ~100 packages so this is not a problem today.
- **`prefer:` cycle detection is limited to direct cycles.** A direct cycle (package A prefers B, package B prefers A on the same path) is detected and reported as an error. Longer transitive cycles (A→B→C→A) are not yet detected.

---

## Roadmap

- [x] Rename refactoring for slice names
- [x] Duplicate slice detection (same `pkg_slice` in multiple files)
- [x] v3 map-style `essential:` format support
- [x] Collision `relatedInformation` + `gotoConflict` code action
- [x] Missing copyright essential diagnostic + quick fix
- [x] Duplicate essential reference diagnostic + goto/remove actions
- [x] Content path validation — validate using chisel's own rules (`?`, `*`, `**`; `[` and `]` are literal filename characters, not metacharacters)
- [x] Lexical sort check — warn when `contents:` paths or `essential:` entries are not in lexical order, with a quick fix to sort them
- [x] `prefer`-aware collision detection — suppress collision warnings when an entry carries `prefer: <package>` pointing at the conflicting package; validate that `prefer` values are not used on globs, reference a different package, and name a package that exists in the release
