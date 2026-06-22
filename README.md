# Nexus for Zed

A [Zed](https://zed.dev) extension that makes the [nexus](https://github.com/paulmanoni/nexus)
framework's `//@` **decorator handlers** first-class in the editor.

Annotate a handler with a `//@` doc comment and nexus wires it up for you
(`nexus generate handlers` / `nexus dev`). This extension detects those
annotations live and gives you:

- **Diagnostics** — unknown decorators (`//@reset` → *"did you mean `//@rest`?"*),
  wrong argument counts (`//@rest GET` is missing its path), orphaned modifiers
  (`//@auth` with no primary), multiple primaries on one function, and decorators
  not attached to a `func`.
- **Outline** — every decorated handler in the file (`documentSymbol`), each
  labelled with its decorator, e.g. `NewGetUser  //@rest GET /users/:id`.
- **Hover** — documentation for any `//@` keyword under the cursor.
- **Completion** — the decorator catalog after you type `//@`.

It runs as a small companion language server (`nexus-lsp`) **alongside gopls**,
so all your normal Go tooling keeps working.

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

The extension is a thin WASM shell; the actual detection lives in `nexus-lsp`, a
plain Go binary. Install it onto your PATH:

```sh
go install github.com/paulmanoni/nexus-zed/lsp/nexus-lsp@latest
# ensure "$(go env GOPATH)/bin" is on your PATH
```

Then add the extension in Zed:

- **From the registry:** `zed: extensions` → search **Nexus** → Install.
- **As a dev extension (this repo):** `zed: install dev extension` → pick this
  folder. Zed compiles the WASM for you (no Rust toolchain setup needed).

Open any Go file that uses `//@` decorators — diagnostics and the outline appear
immediately.

## Develop

```sh
make test       # Go unit + LSP integration tests
make install    # go install nexus-lsp onto your PATH
make lsp        # build ./bin/nexus-lsp locally (with version stamp)
```

Layout:

```
nexus-zed/
  extension.toml          # Zed manifest — registers nexus-lsp for Go
  Cargo.toml, src/lib.rs  # WASM extension: locates & launches nexus-lsp
  lsp/                    # the language server (Go module)
    decorators/           # the detection engine (pure, unit-tested)
    nexus-lsp/            # LSP/JSON-RPC over stdio
```

The detection engine in `lsp/decorators` is deliberately tolerant of in-progress
code — it never runs `go/parser`, scanning line-by-line so you get results while
typing.

## License

MIT.
