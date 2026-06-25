# Nexus — JetBrains plugin

JetBrains/IntelliJ-platform plugin that brings [nexus](https://github.com/paulmanoni/nexus-zed)
framework support to IDEs like IntelliJ IDEA, GoLand, and friends. It's the
JetBrains counterpart to the Zed extension in the repo root — both drive the same
Go language server, [`nexus-lsp`](../lsp).

## What it does

The plugin attaches `nexus-lsp` to:

- **Go files** — the `//@` decorator handlers (`//@rest`, `//@provide`, `//@query`, …)
- **`nexus.toml`** — the config schema

…and surfaces, via LSP:

| Feature | LSP method |
| --- | --- |
| Live diagnostics | `textDocument/publishDiagnostics` |
| Outline | `textDocument/documentSymbol` |
| Hover docs (markdown) | `textDocument/hover` |
| Completion (`@ [ . =` triggers) | `textDocument/completion` |
| Semantic highlighting | `textDocument/semanticTokens/full` |

The server only acts on `.go` files and files named `nexus.toml`; other TOML
files (`Cargo.toml`, etc.) get no nexus features.

## How it's built

- **[LSP4IJ](https://github.com/redhat-developer/lsp4ij)** provides the LSP client,
  so the plugin works in **all** JetBrains IDEs, including the free Community
  editions — not just the ones with the paid built-in LSP API.
- The `nexus-lsp` binary **auto-downloads** from this repo's GitHub releases on
  first use (cached under the IDE's system dir, keyed by release tag), mirroring
  the Zed extension. A `nexus-lsp` already on your `PATH` wins — the dev workflow.

Key classes (`src/main/kotlin/com/github/paulmanoni/nexus/`):

- `NexusLanguageServerFactory` — LSP4IJ entry point (referenced from `plugin.xml`).
- `NexusStreamConnectionProvider` — launches the `nexus-lsp` child process over stdio.
- `NexusLspInstaller` — resolve-from-PATH-or-download logic.

Wiring lives in `src/main/resources/META-INF/plugin.xml` (the `lsp4ij.server` and
`lsp4ij.fileNamePatternMapping` extension points).

## Build & run

Requires JDK 17 (the pinned platform, 2024.1, compiles on 17; newer platforms
need JDK 21).

```bash
cd intellij

# Build the distributable zip -> build/distributions/nexus-intellij-<version>.zip
./gradlew buildPlugin

# Launch a sandbox IDE with the plugin loaded (LSP4IJ is pulled in automatically).
./gradlew runIde

# Verify plugin.xml / API compatibility across the supported IDE range.
./gradlew verifyPlugin
```

Install the built zip via **Settings → Plugins → ⚙ → Install Plugin from Disk…**
(LSP4IJ must be installed too — it's a dependency; the Marketplace prompts for it).

### Debugging the LSP

LSP4IJ ships an **LSP Console** (View → Tool Windows → *LSP Consoles*) that shows
the raw JSON-RPC traffic and the server's stderr log — the fastest way to see what
`nexus-lsp` is doing.

## Configuration knobs

Versions are centralized in `gradle.properties`:

- `platformVersion` — which IDE to build/run against.
- `lsp4ijVersion` — the LSP4IJ release to depend on. If Gradle can't resolve it,
  bump to a build listed on the
  [LSP4IJ versions page](https://plugins.jetbrains.com/plugin/23257-lsp4ij/versions)
  that supports your `pluginSinceBuild`.
