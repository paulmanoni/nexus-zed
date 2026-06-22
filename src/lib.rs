//! Zed extension for the nexus framework.
//!
//! It attaches a tiny companion language server — `nexus-lsp` — to Go buffers,
//! running alongside gopls. The server understands nexus's `//@` decorator
//! handlers (`//@rest`, `//@provide`, `//@query`, …) and provides live
//! diagnostics, an outline of decorated handlers, hover docs, and completion.
//!
//! The LSP binary is plain Go and ships in this repo under `lsp/`. The
//! extension does not bundle it; it locates `nexus-lsp` on the worktree PATH
//! (install once with `go install`). This keeps the WASM extension trivial and
//! lets the server be rebuilt/updated independently of the framework.

use zed_extension_api::{self as zed, Command, LanguageServerId, Result, Worktree};

struct NexusExtension;

impl zed::Extension for NexusExtension {
    fn new() -> Self {
        NexusExtension
    }

    fn language_server_command(
        &mut self,
        _language_server_id: &LanguageServerId,
        worktree: &Worktree,
    ) -> Result<Command> {
        let command = worktree.which("nexus-lsp").ok_or_else(|| {
            concat!(
                "nexus-lsp not found on PATH. Install it once with:\n",
                "    go install github.com/paulmanoni/nexus-zed/lsp/nexus-lsp@latest\n",
                "and make sure \"$(go env GOPATH)/bin\" is on your PATH."
            )
            .to_string()
        })?;

        Ok(Command {
            command,
            args: vec![],
            env: worktree.shell_env(),
        })
    }
}

zed::register_extension!(NexusExtension);
