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
| **Release validation** — flag unknown `format:` versions and malformed `stores:` entries in `chisel.yaml` | `textDocument/publishDiagnostics` |
| **`store:` / `default-track:` validation** — flag store fields used before format v3, unknown stores, `store:`+`archive:` conflicts, and misplaced definition files | `textDocument/publishDiagnostics` |
| **`channel:` validation** — flag malformed channel patterns, repeated tracks, and `channel:` on non-store packages | `textDocument/publishDiagnostics` |
| **`channel:` completions** — suggest `<track>/<risk>` patterns inside a `channel:` value | `textDocument/completion` |

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

The server reads `chisel.yaml` from the workspace root to learn the release format and the store definitions, then loads all `slices/*.yaml` files (plus `bin-slices/*.yaml` in format v3) into an in-memory index and watches those directories for changes. As you edit:

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
  - Release definition errors in `chisel.yaml` — an unknown `format:` version, `stores:` used before format v3, a store missing `kind`, `version` or `default-prefix`, or a store with an unknown `kind`.
  - Store field errors in a slice file — `store:` or `default-track:` used before format v3, `store:` naming a store that `chisel.yaml` does not define, `store:` combined with `archive:`, `store:` without `default-track:` (and vice versa), a `default-track:` that carries a risk, or a store-backed definition placed in the wrong directory for the release format.
  - Invalid `channel:` patterns — a malformed `<track>/<risk>` value, an unknown risk, a misused `*`/`!`/`,` operator, a track repeated within one field, or `channel:` on a package that is not in a store.
- **Quick fixes** (lightbulb / `Ctrl+.`) offer one-click corrections for unknown/invalid references, package name mismatches, a *Go to conflicting slice* action for path collisions, *Add `<pkg>_copyright` to package essentials* for the missing-copyright diagnostic (inserts into the top-level `essential:` block, covering all slices at once; format matches the file's existing v1/v2 or v3 style), *Sort entries lexically* for out-of-order `contents:` or `essential:` blocks, and *Change channel to …* for an invalid `channel:` risk.
- **Hover** renders a markdown summary of a slice's contents and its own essential dependencies, including the store and `default-track:` for store-backed packages and any per-entry `channel:` constraints.
- **v3 format** (`essential:` as a YAML mapping with optional per-entry arch filters) is fully supported alongside the classic v1/v2 sequence format.
- **v4 format** and store-backed packages are supported: `chisel.yaml` `stores:`, the package-level `store:` and `default-track:` fields, and the per-entry `channel:` field on `contents:` paths and `essential:` references.
  - Store-backed packages are indexed under their **unique prefixed name** — the store's `default-prefix` plus the `package:` value (e.g. `package: curl` in the `bin` store becomes `bin-curl`). Completion, go-to-definition, references, rename, hover and the document outline all use that prefixed name, because that is what slice references and `prefer:` values must name.
  - In **format v3** store-backed definitions live in `bin-slices/`, kept apart so that Chisel versions without store support (which only read `slices/`) never see the new fields. That directory is indexed and watched in addition to `slices/`. From **format v4** onwards they live in `slices/` alongside regular definitions, and `bin-slices/` is not read.
  - `chisel.yaml` is watched as well: changing `format:` or a store's `default-prefix` re-resolves every package name and re-runs all diagnostics immediately, without a restart.
  - Path conflicts are computed **irrespective of `channel:`**, exactly as chisel treats `arch:`. Using either field to partition the set of paths would permit combinations that are overly complex and brittle, so chisel is deliberately stricter — and the LSP matches it. A path claimed by one package on `3.0/stable` and by another on `3.0/edge` is still reported as a collision, even though the two are never cut together. `channel:` only affects which content is extracted for a concrete channel.

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

**v4 format** — store-backed packages with channel filtering:

```yaml
# chisel.yaml
format: v4
stores:
  bin:
    kind: bin
    version: 26.10
    default-prefix: "bin-"   # package: curl → unique name bin-curl
```

```yaml
# slices/curl.yaml  (bin-slices/curl.yaml in format v3)
package: curl                # the name in the store; unique name is bin-curl
store: bin                   # mutually exclusive with archive:
default-track: "3.0"         # required with store:, must be a bare track

slices:
  bins:
    essential:
      bin-curl_copyright:     # references use the prefixed unique name
      libssl3_libs:
        channel: 3.0/edge     # only when curl is cut from 3.0/edge
    contents:
      /usr/bin/curl:
      /usr/bin/curl-config:
        channel: [3.0/*, 2.0/!stable]

  copyright:
    contents:
      /usr/share/doc/curl/copyright:
```

Note that the `essential:` reference is `bin-curl_copyright`, not
`curl_copyright`: within a store-backed package, a slice's own siblings are
referenced by the package's **unique prefixed name** too.

A `channel:` value is a `<track>/<risk>` pattern, or a list of them. The track is
a literal; only the risk accepts operators:

| Pattern | Meaning |
|---------|---------|
| `3.0/edge` | exactly that channel |
| `3.0/*` | any risk of the `3.0` track |
| `3.0/!stable` | any risk of `3.0` except `stable` |
| `3.0/beta,edge` | only those risks of `3.0` |

Valid risks are `stable`, `candidate`, `beta` and `edge`. A track may appear at
most once per `channel:` field so the resulting channel set is unambiguous.

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
                           #   parser.go  — slice definition files
                           #   release.go — chisel.yaml (format, archives, stores)
  index/                   # In-memory slice index + fsnotify watcher
  analysis/                # Content path validation, collision detection,
                           # store field + channel pattern validation
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
- [x] Format v4 and store-backed packages — parse `chisel.yaml` (`format:`, `stores:`), resolve unique prefixed package names, index `bin-slices/` in format v3, and validate the `store:`, `default-track:` and `channel:` fields
