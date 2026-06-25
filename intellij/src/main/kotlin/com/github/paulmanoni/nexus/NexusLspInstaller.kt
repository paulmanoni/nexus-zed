package com.github.paulmanoni.nexus

import com.google.gson.JsonParser
import com.intellij.openapi.application.PathManager
import com.intellij.openapi.diagnostic.logger
import com.intellij.openapi.util.SystemInfo
import com.intellij.util.io.HttpRequests
import com.intellij.util.system.CpuArch
import java.io.File
import java.nio.file.Files
import java.nio.file.Path

/**
 * Resolves the `nexus-lsp` executable, mirroring the Zed extension's strategy:
 *
 *  1. A `nexus-lsp` already on the user's PATH wins (the dev workflow: a local
 *     `go install` build, so you can iterate on the server).
 *  2. Otherwise a prebuilt binary is downloaded from this repo's latest GitHub
 *     release and cached under the IDE's system dir, keyed by release tag — so a
 *     fresh install needs zero setup.
 *
 * Network/installation work happens lazily when the language server actually
 * starts (see [NexusStreamConnectionProvider.start]), never on the EDT.
 */
object NexusLspInstaller {
    private const val REPO = "paulmanoni/nexus-zed"
    private const val LATEST_RELEASE = "https://api.github.com/repos/$REPO/releases/latest"

    private val LOG = logger<NexusLspInstaller>()

    /** Cached resolution for this IDE session, keyed by the resolved path's existence. */
    @Volatile
    private var cached: Path? = null

    /** Base name of the executable, `.exe` on Windows. */
    private val exeName: String get() = if (SystemInfo.isWindows) "nexus-lsp.exe" else "nexus-lsp"

    @Synchronized
    fun resolveBinary(): Path {
        cached?.let { if (Files.isRegularFile(it)) return it }

        // 1. PATH wins.
        findOnPath()?.let {
            LOG.info("nexus-lsp resolved from PATH: $it")
            cached = it
            return it
        }

        // 2. Download + cache the matching release asset.
        val downloaded = downloadLatest()
        cached = downloaded
        return downloaded
    }

    private fun findOnPath(): Path? {
        val path = System.getenv("PATH") ?: return null
        val sep = File.pathSeparator
        for (dir in path.split(sep)) {
            if (dir.isBlank()) continue
            val candidate = File(dir, exeName)
            if (candidate.isFile && candidate.canExecute()) return candidate.toPath()
        }
        return null
    }

    /**
     * Asset names produced by the repo's release workflow, e.g.
     * `nexus-lsp-darwin-arm64`, `nexus-lsp-windows-amd64.exe`.
     */
    private fun assetName(): String {
        val os = when {
            SystemInfo.isMac -> "darwin"
            SystemInfo.isWindows -> "windows"
            SystemInfo.isLinux -> "linux"
            else -> error("nexus-lsp has no build for this OS (${SystemInfo.OS_NAME})")
        }
        val arch = when {
            CpuArch.isArm64() -> "arm64"
            CpuArch.isIntel64() -> "amd64"
            else -> error("nexus-lsp has no build for this CPU architecture (${CpuArch.CURRENT})")
        }
        val ext = if (SystemInfo.isWindows) ".exe" else ""
        return "nexus-lsp-$os-$arch$ext"
    }

    private fun cacheRoot(): Path = Path.of(PathManager.getSystemPath(), "nexus-lsp")

    private fun downloadLatest(): Path {
        val wanted = assetName()
        val (tag, downloadUrl) = fetchLatestAsset(wanted)

        // Cache per release tag: an upgrade re-downloads, but repeat starts within
        // a version reuse the cached file.
        val versionDir = cacheRoot().resolve(tag)
        val target = versionDir.resolve(exeName)
        if (Files.isRegularFile(target)) {
            LOG.info("nexus-lsp cache hit: $target")
            makeExecutable(target)
            return target
        }

        Files.createDirectories(versionDir)
        LOG.info("Downloading $wanted ($tag) -> $target")
        HttpRequests.request(downloadUrl)
            .productNameAsUserAgent()
            .saveToFile(target.toFile(), null)
        makeExecutable(target)

        pruneOldVersions(versionDir)
        return target
    }

    /** Returns (tag, browser_download_url) for the asset named [wanted]. */
    private fun fetchLatestAsset(wanted: String): Pair<String, String> {
        val json = HttpRequests.request(LATEST_RELEASE)
            .accept("application/vnd.github+json")
            .productNameAsUserAgent()
            .readString()

        val root = JsonParser.parseString(json).asJsonObject
        val tag = root.get("tag_name")?.asString
            ?: error("GitHub release for $REPO has no tag_name")

        val assets = root.getAsJsonArray("assets") ?: error("release $tag has no assets")
        for (el in assets) {
            val obj = el.asJsonObject
            if (obj.get("name")?.asString == wanted) {
                val url = obj.get("browser_download_url")?.asString
                    ?: error("asset $wanted in $tag has no download URL")
                return tag to url
            }
        }
        error("no asset named '$wanted' in release $tag of $REPO")
    }

    private fun makeExecutable(path: Path) {
        if (!SystemInfo.isWindows) {
            val f = path.toFile()
            if (!f.canExecute()) f.setExecutable(true, false)
        }
    }

    /** Delete cached binaries from other release tags to keep the dir tidy. */
    private fun pruneOldVersions(keep: Path) {
        val root = cacheRoot()
        val children = root.toFile().listFiles() ?: return
        for (child in children) {
            if (child.isDirectory && child.toPath() != keep) {
                child.deleteRecursively()
            }
        }
    }
}
