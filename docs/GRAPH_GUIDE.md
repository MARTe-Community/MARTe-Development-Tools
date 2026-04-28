# Signal Flow Graph Guide

`mdt graph` renders an interactive, browser-based visualisation of the GAM/DataSource signal flow for a MARTe project. It can also run alongside the LSP server (`mdt lsp --graph`) so the view follows the cursor in real time.

## Starting the graph

### Standalone mode

```bash
# Scan the current directory for .marte files
mdt graph

# Scan a specific project root
mdt graph -P path/to/project

# Filter to a single package
mdt graph -P path/to/project -p MyProject

# Explicit files
mdt graph src/a.marte src/b.marte

# Choose a fixed port (default: OS-assigned free port)
mdt graph -port 8080

# Override variables (same syntax as mdt check / build)
mdt graph -P . -vMY_VAR=42
```

The command starts an HTTP server, prints the URL, and opens it in the default browser. The graph reloads automatically whenever a `.marte` file in the scanned directory changes. Pan and zoom are preserved across reloads.

### LSP-integrated mode

Pass `--graph` to `mdt lsp` to run the graph server alongside the language server:

```bash
mdt lsp --graph
mdt lsp --graph --graph-port=9090
```

In this mode the graph:
- Rebuilds whenever the LSP finishes validation (i.e. on every file save).
- Follows the cursor: hovering over a GAM or DataSource in the editor sends a `focus` event that zooms the graph to that node.

If `--graph-port` is omitted, the OS assigns a free port.

## Interface overview

```
┌─ header bar ─────────────────────────────────────────────────────────────────┐
│ ☰  MARTe Signal Flow  │ legend │ State ▾  Thread ▾ │ ✕ Clear │ ⊞ Focus │   │
│                        │        │                   │ ◉ N     │ ← Full  │ ? │
├─ sidebar ──┬─ graph viewport ─────────────────────────────────────────────────┤
│ object     │                                                                  │
│ tree with  │      Graphviz DOT layout rendered as interactive SVG             │
│ signal     │      (pan with drag, zoom with scroll or +/− buttons)            │
│ details    │                                                                  │
└────────────┴──────────────────────────────────────────────────────────────────┘
```

### Legend

| Colour | Meaning |
|--------|---------|
| Blue border | DataSource (DS) |
| Purple border | IOGAM |
| Orange border | Message/Timing GAM |
| Dark border | Standard GAM |
| Blue edge | Read (DS → GAM) |
| Orange edge | Write (GAM → DS) |

Conditional nodes (inside `#if` blocks) are shown with a dashed border.

## Navigation

| Action | Result |
|--------|--------|
| `/` or ⌕ button | Open search overlay (nodes and signals) |
| `Home` / `h` | Reset view — fit entire graph |
| `+` / `−` buttons | Zoom in / out |
| `Tab` | Cycle between the read-clone and write-clone of a split DataSource |
| Drag | Pan the graph |
| Scroll wheel | Zoom |

## Selection

| Action | Result |
|--------|--------|
| Click a node | Select it; highlight its direct signal connections |
| Shift+click | Add node to multi-selection |
| Click background | Clear selection |
| ✕ Clear button | Clear selection |

Selected nodes are highlighted; unselected nodes and edges are dimmed.

## Focus layouts

Focus mode draws a compact, optimised Graphviz layout showing only the relevant subset of the graph.

### Node focus

1. Select one or more nodes (click / shift+click, or use the sidebar).
2. Click **⊞ Focus** — renders an optimised layout for the selected nodes plus their immediate DataSource neighbours.

### Watchlist

Pin nodes of interest by clicking the **⊕** pin icon next to them in the sidebar.

- **◉ N** button (where *N* is the pin count) — draws an optimised layout for all pinned nodes and their DataSources.
- Click ⊕ again to unpin.

### Returning to the full graph

Click **← Full** to exit focus/watchlist/filter mode and return to the complete graph.

## State and thread filtering

Use the **State** and **Thread** dropdown menus in the header to restrict the graph to GAMs active in a particular MARTe real-time state or thread.

- The graph switches to an optimised focused layout showing only the matching GAMs and all DataSources that supply or consume their signals.
- Click **← Full** (or reset both dropdowns to *All*) to return to the full graph.

## Sidebar

The sidebar on the left shows all GAMs and DataSources in a tree.

| Action | Result |
|--------|--------|
| Click a node | Expand its signal list; select and zoom to it in the graph |
| Ctrl+click | Deselect the node |
| ⊕ pin icon | Pin / unpin to watchlist |
| Filter box | Filter tree by node name or signal name |
| ☰ button | Collapse / expand the sidebar |

Signal rows show type, direction, and any diagnostics (errors / warnings).

## Tooltips

Hovering over a node in the graph shows a tooltip with the node class, doc-string (if present), and a count of its input/output signals.

## Live reload

In standalone mode, `mdt graph` watches the scanned directory for file changes. When a `.marte` file is saved the graph rebuilds automatically; the current pan/zoom position and selection are restored.

In LSP-integrated mode (`mdt lsp --graph`) the graph rebuilds after every successful validation triggered by the editor.

## Keyboard shortcuts summary

| Key | Action |
|-----|--------|
| `/` | Open search |
| `h` | Reset view (fit graph) |
| `?` | Toggle help overlay |
| `Tab` | Cycle DataSource clones |
| `Home` | Reset view |
| `+` / `−` | Zoom in / out |
