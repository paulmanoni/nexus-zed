package com.github.paulmanoni.nexus

import com.intellij.openapi.project.Project
import com.redhat.devtools.lsp4ij.LanguageServerFactory
import com.redhat.devtools.lsp4ij.server.StreamConnectionProvider

/**
 * LSP4IJ entry point: tells the framework how to spawn a connection to nexus-lsp.
 *
 * The server, file mappings, and this factory are wired together in plugin.xml
 * via the `com.redhat.devtools.lsp4ij.server` /
 * `com.redhat.devtools.lsp4ij.fileNamePatternMapping` extension points.
 */
class NexusLanguageServerFactory : LanguageServerFactory {
    override fun createConnectionProvider(project: Project): StreamConnectionProvider =
        NexusStreamConnectionProvider(project)
}
