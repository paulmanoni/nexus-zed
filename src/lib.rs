//! Zed extension for the nexus framework.
//!
//! It attaches a tiny companion language server — `nexus-lsp` — to Go and TOML
//! buffers, running alongside gopls. The server understands nexus's `//@`
//! decorator handlers (`//@rest`, `//@provide`, `//@query`, …) and the
//! `nexus.toml` config schema, providing live diagnostics, an outline, hover
//! docs, and completion for both.
//!
//! The LSP binary is plain Go and lives in this repo under `lsp/`. The extension
//! resolves it in two ways, in order:
//!   1. a `nexus-lsp` already on the worktree PATH (the dev workflow — your
//!      `go install`ed build wins, so you can iterate on the server), else
//!   2. a prebuilt binary auto-downloaded from this repo's GitHub releases and
//!      cached in the extension's work dir — so a fresh install needs zero setup.

use zed_extension_api::{self as zed, Command, LanguageServerId, Result, Worktree};

struct NexusExtension {
    // Cached path to the downloaded binary, so we don't re-check the release on
    // every language_server_command call within a session.
    cached_binary: Option<String>,
}

const REPO: &str = "paulmanoni/nexus-zed";

impl zed::Extension for NexusExtension {
    fn new() -> Self {
        NexusExtension {
            cached_binary: None,
        }
    }

    fn language_server_command(
        &mut self,
        language_server_id: &LanguageServerId,
        worktree: &Worktree,
    ) -> Result<Command> {
        Ok(Command {
            command: self.nexus_lsp_path(language_server_id, worktree)?,
            args: vec![],
            env: worktree.shell_env(),
        })
    }
}

impl NexusExtension {
    /// Resolve the `nexus-lsp` binary: prefer one on PATH, otherwise download a
    /// prebuilt release asset and cache it.
    fn nexus_lsp_path(
        &mut self,
        language_server_id: &LanguageServerId,
        worktree: &Worktree,
    ) -> Result<String> {
        // 1. A nexus-lsp on PATH wins (dev workflow: `go install …/lsp/nexus-lsp`).
        if let Some(path) = worktree.which("nexus-lsp") {
            return Ok(path);
        }

        // 2. Reuse the binary downloaded earlier this session if it still exists.
        if let Some(path) = &self.cached_binary {
            if file_exists(path) {
                return Ok(path.clone());
            }
        }

        // 3. Download the prebuilt binary for this platform from the latest release.
        let path = self.download_nexus_lsp(language_server_id)?;
        self.cached_binary = Some(path.clone());
        Ok(path)
    }

    fn download_nexus_lsp(&self, language_server_id: &LanguageServerId) -> Result<String> {
        zed::set_language_server_installation_status(
            language_server_id,
            &zed::LanguageServerInstallationStatus::CheckingForUpdate,
        );

        let release = zed::latest_github_release(
            REPO,
            zed::GithubReleaseOptions {
                require_assets: true,
                pre_release: false,
            },
        )?;

        let (os, arch) = zed::current_platform();
        let os_name = match os {
            zed::Os::Mac => "darwin",
            zed::Os::Linux => "linux",
            zed::Os::Windows => "windows",
        };
        let arch_name = match arch {
            zed::Architecture::Aarch64 => "arm64",
            zed::Architecture::X8664 => "amd64",
            zed::Architecture::X86 => {
                return Err("nexus-lsp has no 32-bit x86 build".to_string());
            }
        };
        let ext = if matches!(os, zed::Os::Windows) { ".exe" } else { "" };

        // Asset names produced by .github/workflows/release.yml, e.g.
        // "nexus-lsp-darwin-arm64", "nexus-lsp-windows-amd64.exe".
        let asset_name = format!("nexus-lsp-{os_name}-{arch_name}{ext}");
        let asset = release
            .assets
            .iter()
            .find(|a| a.name == asset_name)
            .ok_or_else(|| {
                format!(
                    "no nexus-lsp asset named {asset_name} in release {}",
                    release.version
                )
            })?;

        // Cache per release version so an upgrade re-downloads but reuses within
        // a version. Path is relative to the extension's work dir.
        let version_dir = format!("nexus-lsp-{}", release.version);
        let binary_path = format!("{version_dir}/nexus-lsp{ext}");

        if !file_exists(&binary_path) {
            zed::set_language_server_installation_status(
                language_server_id,
                &zed::LanguageServerInstallationStatus::Downloading,
            );
            zed::download_file(
                &asset.download_url,
                &binary_path,
                zed::DownloadedFileType::Uncompressed,
            )
            .map_err(|e| format!("failed to download {asset_name}: {e}"))?;
            zed::make_file_executable(&binary_path)?;

            // Drop older cached versions to keep the work dir tidy.
            if let Ok(entries) = std::fs::read_dir(".") {
                for entry in entries.flatten() {
                    let name = entry.file_name().to_string_lossy().to_string();
                    if name.starts_with("nexus-lsp-") && name != version_dir {
                        let _ = std::fs::remove_dir_all(entry.path());
                    }
                }
            }
        }

        Ok(binary_path)
    }
}

fn file_exists(path: &str) -> bool {
    std::fs::metadata(path).map(|m| m.is_file()).unwrap_or(false)
}

zed::register_extension!(NexusExtension);
