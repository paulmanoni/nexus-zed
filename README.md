# Nexus for Zed

A [Zed](https://zed.dev) extension that makes the [nexus](https://github.com/paulmanoni/nexus)
framework's `//@` **decorator handlers** first-class in the editor.

Annotate a handler with a `//@` doc comment and nexus wires it up for you
(`nexus generate handlers` / `nexus dev`). This extension detects those
annotations live and gives you:

- **Coloring** — decorators stop looking like flat comment grey: the `@keyword`
  is painted like a keyword, the HTTP method like a constant, and the
  path/name/type arguments like strings (via LSP semantic tokens, so your theme
  picks the colors). The HTTP method also carries a per-verb modifier, so you can
  give `GET`/`POST`/`PUT`/… distinct, Postman-style colors — see
  [Enable coloring](#enable-coloring). **Requires** `"semantic_tokens":
  "combined"` in your Zed settings (it defaults to `"off"`).
- **Diagnostics** — with severity matching nexus's own contract. **Errors** for
  anything `nexus generate handlers` rejects (the handler silently won't
  register): unknown decorators (`//@reset` → *"did you mean `//@rest`?"*), wrong
  argument counts (`//@rest GET` is missing its path), a bad `//@auth` value, a
  modifier with no primary, and more than one primary. **Warnings** for things
  nexus accepts but are likely mistakes (a non-standard HTTP method, a path
  missing its leading `/`, a decorator not attached to a `func`). `//@auth` /
  `//@use` on a custom `//@pkg.Func` is **allowed** (nexus appends it as a
  trailing option, e.g. `inertia.Page(..., auth.Required())`), so it isn't
  flagged.
- **Outline** — every decorated handler in the file (`documentSymbol`), each
  labelled with its decorator, e.g. `NewGetUser  //@rest GET /users/:id`.
- **Hover** — documentation for any `//@` keyword under the cursor.
- **Completion** — the decorator catalog after you type `//@`.

It runs as a small companion language server (`nexus-lsp`) **alongside gopls**,
so all your normal Go tooling keeps working.

## `nexus.toml` intellisense

The same server also understands your **`nexus.toml`** config file (and only a
file named exactly `nexus.toml` — other TOML files are left to their own tooling).
Its schema mirrors nexus's own loaders, so you get:

- **Diagnostics** — unknown keys in a closed section (`introspecton` →
  *"did you mean `introspection`?"*), a `[runtime]` scalar mistakenly written at
  the top level (*"…is ignored by nexus — move it under `[runtime]`"*), wrong
  value types, an invalid listener `scope`, an unparseable `max_age` duration or
  CIDR, and negative rate limits. Open namespaces (`[env]`, custom
  `[extensions.*]`) and arbitrary top-level sections are left alone.
- **Completion** — section headers after `[`, a section's remaining keys, and the
  allowed values for enum keys (e.g. `scope = "public" | "admin" | "internal"`).
- **Hover** — docs for any section header or key under the cursor.
- **Outline** — every `[table]` in the file (`documentSymbol`).

## Enable coloring

Zed's LSP semantic-token highlighting defaults to **off**, so decorator coloring
(and per-verb HTTP method colors) won't appear until you turn it on. Add this to
your Zed `settings.json`:

```jsonc
{
  // Overlay LSP semantic tokens on top of tree-sitter (keeps normal Go/TOML
  // highlighting; "full" would replace it — don't use that).
  "languages": {
    "Go": { "semantic_tokens": "combined" }
  },

  // Optional: Postman-style per-verb colors for //@rest. nexus-lsp tags the HTTP
  // method token (semantic type "constant") with a per-method modifier; map each
  // to a color. Plain constants (no modifier) are untouched.
  "global_lsp_settings": {
    "semantic_token_rules": [
      { "token_type": "constant", "token_modifiers": ["get"],     "foreground_color": "#98c379", "font_weight": "bold" },
      { "token_type": "constant", "token_modifiers": ["post"],    "foreground_color": "#d19a66", "font_weight": "bold" },
      { "token_type": "constant", "token_modifiers": ["put"],     "foreground_color": "#61afef", "font_weight": "bold" },
      { "token_type": "constant", "token_modifiers": ["patch"],   "foreground_color": "#56b6c2", "font_weight": "bold" },
      { "token_type": "constant", "token_modifiers": ["delete"],  "foreground_color": "#e06c75", "font_weight": "bold" },
      { "token_type": "constant", "token_modifiers": ["head"],    "foreground_color": "#c678dd", "font_weight": "bold" },
      { "token_type": "constant", "token_modifiers": ["options"], "foreground_color": "#d19a66", "font_weight": "bold" }
    ]
  }
}
```

## Decorators it understands

| Primary | Args | Registers |
|---|---|---|
| `//@provide` | — | a DI constructor |
| `//@rest` | `METHOD PATH` | a REST route |
| `//@query` / `//@mutation` / `//@subscription` | — | a GraphQL field |
| `//@ws` | `PATH TYPE` | a WebSocket handler |
| `//@worker` | `NAME` | a background worker |

| Modifier | Args | Effect |
|---|---|---|
| `//@auth` | `Required` \| `Requires("ROLE")` | auth gate on the primary |
| `//@use` | `<expr>` | per-op middleware |

Qualified custom decorators from extensions (e.g. `//@inertia.Page "GET" "/users" "Users/Index"`)
are recognized as primaries and left to their owning package.

## Install

Just add the extension — **no separate setup**:

- **From the registry:** `zed: extensions` → search **Nexus** → Install.
- **As a dev extension (this repo):** `zed: install dev extension` → pick this
  folder. Zed compiles the WASM for you (no Rust toolchain setup needed).

The extension is a thin WASM shell; the detection lives in `nexus-lsp`, a plain
Go binary. On first use it resolves the binary in this order:

1. a `nexus-lsp` already on your PATH — **the dev workflow**: if you
   `go install github.com/paulmanoni/nexus-zed/lsp/nexus-lsp@latest` (ensure
   `"$(go env GOPATH)/bin"` is on PATH), that build wins so you can iterate on
   the server;
2. otherwise it **auto-downloads** the prebuilt binary for your platform from
   this repo's [GitHub releases](https://github.com/paulmanoni/nexus-zed/releases)
   and caches it — zero setup.

Then open a Go file that uses `//@` decorators, or a `nexus.toml` — diagnostics
and the outline appear immediately. (LSP-based coloring also needs
`"semantic_tokens": "combined"` — see [Enable coloring](#enable-coloring).)

> The prebuilt binaries are cross-compiled and attached to each release by
> `.github/workflows/release.yml` on a `v*` tag (darwin/linux arm64+amd64,
> windows amd64). Asset names are `nexus-lsp-<os>-<arch>[.exe]`, which is exactly
> what `src/lib.rs` looks for.

## Develop

```sh
make test       # Go unit + LSP integration tests
make install    # go install nexus-lsp onto your PATH
make lsp        # build ./bin/nexus-lsp locally (with version stamp)
```

Layout:

```
nexus-zed/
  extension.toml          # Zed manifest — registers nexus-lsp for Go + TOML
  Cargo.toml, src/lib.rs  # WASM extension: locates & launches nexus-lsp
  lsp/                    # the language server (Go module)
    decorators/           # the //@ decorator detection engine (pure, unit-tested)
    nexustoml/            # the nexus.toml schema + intellisense (pure, unit-tested)
    nexus-lsp/            # LSP/JSON-RPC over stdio; dispatches .go vs nexus.toml
```

The engines in `lsp/decorators` and `lsp/nexustoml` are deliberately tolerant of
in-progress code — they never run `go/parser` or a real TOML decoder, scanning
line-by-line so you get results while typing.

## License

MIT.
