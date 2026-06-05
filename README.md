# chisel-releases-lsp

A [Language Server Protocol](https://microsoft.github.io/language-server-protocol/) (LSP) server for [chisel](https://github.com/canonical/chisel) slice definition files.

`chisel-releases-lsp` is a tool-agnostic developer companion for working with [chisel-releases](https://github.com/canonical/chisel-releases). It plugs into any LSP-capable editor (Neovim, VS Code, Emacs, Helix, …) and provides real-time feedback while you write or review slice definitions.

---

## Features

| Feature | LSP method |
|---------|-----------|
| **Slice completions** — suggest `<pkg>_<slice>` in `essential:` lists | `textDocument/completion` |
| **Jump to definition** — go to the slice key in its `.yaml` file | `textDocument/definition` |
| **Glob pattern validation** — flag invalid patterns in `contents:` | `textDocument/publishDiagnostics` |
| **Slice collision detection** — warn when two packages claim the same path | `textDocument/publishDiagnostics` |
| **Unknown reference warnings** — warn on `essential:` entries that don't exist | `textDocument/publishDiagnostics` |
| **Hover documentation** — show a slice's contents and essential dependencies | `textDocument/hover` |

---

## Installation

```bash
go install github.com/canonical/chisel-releases-lsp/cmd/chisel-releases-lsp@latest
```

Or build from source:

```bash
git clone https://github.com/canonical/chisel-releases-lsp
cd chisel-releases-lsp
go build -o chisel-releases-lsp ./cmd/chisel-releases-lsp
```

---

## Usage

The server communicates over **stdio** and is started automatically by your editor's LSP client. Configure your editor to:

1. Run `chisel-releases-lsp` as the language server command.
2. Associate it with YAML files inside a `slices/` directory (or all `*.yaml` files in chisel-releases workspaces).
3. Set the workspace root to the chisel release directory (the one containing `chisel.yaml` and `slices/`).

### Neovim (with `nvim-lspconfig`)

```lua
local lspconfig = require('lspconfig')
local configs = require('lspconfig.configs')

if not configs.chisel_releases_lsp then
  configs.chisel_releases_lsp = {
    default_config = {
      cmd = { 'chisel-releases-lsp' },
      filetypes = { 'yaml' },
      root_dir = lspconfig.util.root_pattern('chisel.yaml', 'slices'),
      settings = {},
    },
  }
end

lspconfig.chisel_releases_lsp.setup {}
```

### VS Code

Install a generic LSP client extension (e.g. [vscode-glspc](https://github.com/tliron/vscode-glspc)) and configure it to run `chisel-releases-lsp` for YAML files rooted at a chisel release directory.

---

## How it works

The server loads all `slices/*.yaml` files from the workspace root into an in-memory index on startup, then watches the directory for changes. As you edit:

- **Completions** are offered whenever the cursor is inside an `essential:` list item.
- **Go to definition** resolves `<pkg>_<slice>` tokens to the exact line in `slices/<pkg>.yaml`.
- **Diagnostics** are published on open, change, and save:
  - Invalid glob patterns in `contents:` paths.
  - Cross-package path collisions (two packages claiming the same concrete path).
  - Unknown slice references in `essential:` lists.
- **Hover** renders a markdown summary of a slice's contents and its own essential dependencies.

---

## Slice definition format

A slice definition file lives at `slices/<package>.yaml`:

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

See the [chisel documentation](https://documentation.ubuntu.com/chisel/en/latest/) for the full schema reference.

---

## Development

```bash
# Run tests
go test ./...

# Build
go build ./cmd/chisel-releases-lsp
```

### Project layout

```
cmd/chisel-releases-lsp/   # Entry point
internal/
  parser/                  # Position-aware YAML parser (gopkg.in/yaml.v3 Node API)
  index/                   # In-memory slice index + fsnotify watcher
  analysis/                # Glob validation + collision detection
  lsp/                     # LSP method handlers (glsp)
```

---

## Roadmap

- [ ] Remote chisel-releases (pull from `canonical/chisel-releases` GitHub branches)
- [ ] TCP/socket transport in addition to stdio
- [ ] Schema validation for `chisel.yaml`
- [ ] Rename refactoring for slice names
