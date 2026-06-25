package com.github.paulmanoni.nexus

import com.intellij.openapi.project.Project
import com.redhat.devtools.lsp4ij.server.ProcessStreamConnectionProvider

/**
 * Launches the `nexus-lsp` child process and connects it to LSP4IJ over its
 * stdio (the server speaks LSP via Content-Length framed JSON-RPC on stdin/out).
 *
 * The binary is resolved lazily in [start] — which LSP4IJ calls off the EDT on a
 * pooled thread — so the first start may download the release asset without
 * blocking the UI.
 */
class NexusStreamConnectionProvider(private val project: Project) : ProcessStreamConnectionProvider() {

    override fun start() {
        val exe = NexusLspInstaller.resolveBinary()
        super.setCommands(listOf(exe.toAbsolutePath().toString()))
        project.basePath?.let { super.setWorkingDirectory(it) }
        super.start()
    }
}
