# Editor Integration Guide

`mdt` includes a Language Server Protocol (LSP) implementation that provides features like:

- Syntax highlighting and error reporting (Parser & Semantic)
- Auto-completion
- Go to Definition / References
- Hover documentation
- Symbol renaming
- Incremental synchronization (Robust)

The LSP server is started via the command:

```bash
mdt lsp
```

It communicates via **stdio**.

## VS Code

You can use a generic LSP extension like [Generic LSP Client](https://marketplace.visualstudio.com/items?itemName=summne.vscode-generic-lsp-client) or configure a custom task.

**Using "Run on Save" or similar extensions is an option, but for true LSP support:**

1.  Install the **"glspc"** (Generic LSP Client) extension or similar.
2.  Configure it in your `settings.json`:

```json
"glspc.languageServer configurations": [
    {
        "languageId": "marte",
        "command": "mdt",
        "args": ["lsp"],
        "rootUri": "${workspaceFolder}"
    }
]
```

3.  Associate `.marte` files with the language ID:

```json
"files.associations": {
    "*.marte": "marte"
}
```

## Neovim (Native LSP)

Add the following to your `init.lua` or `init.vim` (using `nvim-lspconfig`):

```lua
local lspconfig = require'lspconfig'
local configs = require'lspconfig.configs'

if not configs.marte then
  configs.marte = {
    default_config = {
      cmd = {'mdt', 'lsp'},
      filetypes = {'marte'},
      root_dir = lspconfig.util.root_pattern('.git', 'go.mod', '.marte_schema.cue'),
      settings = {},
    },
  }
end

lspconfig.marte.setup{}

-- Add filetype detection
vim.cmd([[
  autocmd BufNewFile,BufRead *.marte setfiletype marte
]])
```

## Helix

Add this to your `languages.toml` (usually in `~/.config/helix/languages.toml`):

```toml
[[language]]
name = "marte"
scope = "source.marte"
injection-regex = "marte"
file-types = ["marte"]
roots = [".git", ".marte_schema.cue"]
comment-token = "//"
indent = { tab-width = 2, unit = "  " }
language-servers = [ "mdt-lsp" ]

[language-server.mdt-lsp]
command = "mdt"
args = ["lsp"]
```

## Vim

### Using `vim-lsp`

```vim
if executable('mdt')
    au User lsp_setup call lsp#register_server({
        \ 'name': 'mdt-lsp',
        \ 'cmd': {server_info->['mdt', 'lsp']},
        \ 'whitelist': ['marte'],
        \ })
endif

au BufRead,BufNewFile *.marte set filetype=marte
```

### Using `ALE`

```vim
call ale#linter#define('marte', {
\   'name': 'mdt',
\   'lsp': 'stdio',
\   'executable': 'mdt',
\   'command': '%e lsp',
\   'project_root': function('ale#handlers#python#FindProjectRoot'),
\})
```

## Zed

Add to your `settings.json`:

```json
"lsp": {
    "marte": {
        "binary": {
            "path": "mdt",
            "arguments": ["lsp"]
        }
    }
}
```

## Kakoune (kak-lsp)

In your `kak-lsp.toml`:

```toml
[language.marte]
filetypes = ["marte"]
roots = [".git", ".marte_schema.cue"]
command = "mdt"
args = ["lsp"]
```

## Eclipse

1.  Install **LSP4E** plugin.
2.  Go to **Preferences > Language Servers**.
3.  Add a new Language Server:
    - **Content Type**: Text / Custom (Associate `*.marte` with a content type).
    - **Launch configuration**: Program.
    - **Command**: `mdt`
    - **Arguments**: `lsp`
    - **Input/Output**: Standard Input/Output.
