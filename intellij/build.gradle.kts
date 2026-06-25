import org.jetbrains.intellij.platform.gradle.IntelliJPlatformType

plugins {
    id("java")
    id("org.jetbrains.kotlin.jvm") version "1.9.25"
    id("org.jetbrains.intellij.platform") version "2.5.0"
}

group = providers.gradleProperty("pluginGroup").get()
version = providers.gradleProperty("pluginVersion").get()

// Path to the bundled demo project (demo/users.go + demo/nexus.toml) that the
// runIde / runGoLand sandbox tasks open on launch.
val demoProjectPath = layout.projectDirectory.dir("demo").asFile.absolutePath

repositories {
    mavenCentral()
    intellijPlatform {
        defaultRepositories()
        // LSP4IJ is distributed via the JetBrains Marketplace.
        marketplace()
    }
}

dependencies {
    intellijPlatform {
        create(
            IntelliJPlatformType.fromCode(providers.gradleProperty("platformType").get()),
            providers.gradleProperty("platformVersion").get(),
        )

        // The LSP client framework the plugin builds on.
        plugin("com.redhat.devtools.lsp4ij:${providers.gradleProperty("lsp4ijVersion").get()}")

        pluginVerifier()
        zipSigner()
    }
}

intellijPlatform {
    pluginConfiguration {
        ideaVersion {
            sinceBuild = providers.gradleProperty("pluginSinceBuild")
            // Open-ended: don't pin an untilBuild, so the plugin keeps loading
            // on newer IDEs without a republish.
            untilBuild = provider { null }
        }
    }
}

// Extra sandbox launchers for other JetBrains IDEs. The plugin itself is built
// against IntelliJ Community (the common denominator); these just run the same
// plugin in a different IDE so you can verify it there.
//   ./gradlew runGoLand   — GoLand: native .go support, best for the //@ decorator features.
intellijPlatformTesting {
    runIde {
        register("runGoLand") {
            type = IntelliJPlatformType.GoLand
            version = providers.gradleProperty("golandVersion")
            // Open the demo project on launch so the LSP attaches to a real
            // nexus .go file + nexus.toml immediately (otherwise the IDE opens to
            // the welcome screen with nothing for the server to act on).
            task {
                args(demoProjectPath)
            }
        }
    }
}

kotlin {
    jvmToolchain(17)
}

tasks {
    // Same auto-open for the default `runIde` (IntelliJ Community) sandbox.
    runIde {
        args(demoProjectPath)
    }

    wrapper {
        gradleVersion = "8.10"
    }
}
