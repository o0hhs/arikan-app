@file:Suppress("UNUSED_VARIABLE")

import com.android.build.gradle.AppExtension
import com.android.build.gradle.BaseExtension
import java.net.URL
import java.util.*
import org.jetbrains.kotlin.gradle.dsl.JvmTarget
import org.jetbrains.kotlin.gradle.tasks.KotlinCompile

buildscript {
    repositories {
        mavenCentral()
        google()
        // Vendored copy of the few kr328 artifacts (golang gradle-plugin, kaidl)
        // that only exist in MetaCubeX/maven-backup. Do not add the mutable
        // raw.githubusercontent.com branch mirror here: buildscript artifacts
        // execute during Gradle configuration, so they must come from this
        // repository-controlled copy instead of an unpinned branch.
        maven(rootProject.projectDir.resolve("maven").toURI())
    }
    dependencies {
        classpath(libs.build.android)
        classpath(libs.build.kotlin.common)
        classpath(libs.build.kotlin.serialization)
        classpath(libs.build.ksp)
        classpath(libs.build.golang)
    }
}

subprojects {
    repositories {
        mavenCentral()
        google()
        // Same trust boundary as the buildscript block above: kr328 artifacts
        // are resolved from the repository-controlled vendored Maven copy, not
        // from the mutable raw.githubusercontent.com branch mirror.
        maven(rootProject.projectDir.resolve("maven").toURI())
    }

    val isApp = name == "app"

    apply(plugin = if (isApp) "com.android.application" else "com.android.library")

    fun queryConfigProperty(key: String): Any? {
        val localProperties = Properties()
        val localPropertiesFile = rootProject.file("local.properties")
        if (localPropertiesFile.exists()) {
            localProperties.load(localPropertiesFile.inputStream())
        } else {
            return null
        }
        return localProperties.getProperty(key)
    }

    extensions.configure<BaseExtension> {
        buildFeatures.buildConfig = true
        defaultConfig {
            if (isApp) {
                // Arikan's own id: it has to differ from the official Mikan's for the two to
                // be installable side by side, and it has to stay the same across our
                // releases — the in-app updater and the panel's device fingerprint both key
                // off it. A build can override it with custom.application.id in
                // local.properties (see queryConfigProperty).
                val customApplicationId = queryConfigProperty("custom.application.id") as? String?
                applicationId = customApplicationId.takeIf { it?.isNotBlank() == true } ?: "ru.arikan.app"
            }

            project.name.let { name ->
                namespace = if (name == "app") "com.github.kr328.clash"
                else "com.github.kr328.clash.$name"
            }

            minSdk = 21
            targetSdk = 35

            // Arikan starts after the Mikan line it forks (upstream is at 0.1.3): its own
            // first release is 0.2.0, so the release notes file for it is a new one rather
            // than a rewrite of Mikan's 0.1.0 history.
            versionName = "0.2.0"
            // Keep Android's update counter increasing when resetting the fork's display version.
            versionCode = 10201013

            resValue("string", "release_name", "v$versionName")
            resValue("integer", "release_code", "$versionCode")

            // The first releases ship what phones actually use: every 64-bit Android device
            // is arm64-v8a, older 32-bit ones are armeabi-v7a. Emulator and Chromebook ABIs
            // (x86, x86_64) need a Go toolchain triplet of their own and are a later build.
            ndk {
                abiFilters += listOf("arm64-v8a", "armeabi-v7a")
            }

            externalNativeBuild {
                cmake {
                    abiFilters("arm64-v8a", "armeabi-v7a")
                }
            }

            if (!isApp) {
                consumerProguardFiles("consumer-rules.pro")
            } else {
                setProperty("archivesBaseName", "arikan-v$versionName")
            }
        }

        ndkVersion = "29.0.14206865"

        compileSdkVersion(defaultConfig.targetSdk!!)

        if (isApp) {
            packagingOptions {
                resources {
                    excludes.add("DebugProbesKt.bin")
                }
            }
        }

        productFlavors {
            flavorDimensions("feature")

            // Upstream ships mikan-alpha and mikan-meta side by side, hence the flavor's
            // .alpha/.meta suffix. Arikan is one app whose id must be exactly ru.arikan.app,
            // so the suffix is off unless local.properties asks for it (remove.suffix=false);
            // debug builds get a .debug suffix from buildTypes instead, which keeps a test
            // build installable next to a release one.
            val removeSuffix = (queryConfigProperty("remove.suffix") as? String)?.toBoolean() ?: true

            create("alpha") {
                isDefault = true
                dimension = flavorDimensionList[0]

                if (isApp) {
                    resValue("string", "launch_name", "@string/launch_name_alpha")
                    resValue("string", "application_name", "@string/application_name_alpha")
                }

                if (isApp && !removeSuffix) {
                    applicationIdSuffix = ".alpha"
                }
            }

            create("meta") {

                dimension = flavorDimensionList[0]
                if (isApp) {
                    resValue("string", "launch_name", "@string/launch_name_meta")
                    resValue("string", "application_name", "@string/application_name_meta")
                }

                if (isApp && !removeSuffix) {
                    applicationIdSuffix = ".meta"
                }
            }
        }

        sourceSets {
            getByName("meta") {
                java.srcDirs("src/foss/java")
            }
            getByName("alpha") {
                java.srcDirs("src/foss/java")
            }
        }

        signingConfigs {
            val keystore = rootProject.file("signing.properties")
            if (keystore.exists()) {
                create("release") {
                    val prop = Properties().apply {
                        keystore.inputStream().use(this::load)
                    }

                    storeFile = rootProject.file("release.keystore")
                    storePassword = prop.getProperty("keystore.password")!!
                    keyAlias = prop.getProperty("key.alias")!!
                    keyPassword = prop.getProperty("key.password")!!
                }
            }
        }

        buildTypes {
            named("release") {
                isMinifyEnabled = isApp
                isShrinkResources = isApp
                signingConfig = signingConfigs.findByName("release")
                proguardFiles(
                    getDefaultProguardFile("proguard-android-optimize.txt"),
                    "proguard-rules.pro"
                )
            }
            named("debug") {
                versionNameSuffix = ".debug"

                if (isApp) {
                    // The application's own id, so a debug build and a release build can both
                    // be installed: they are signed with different keys, and Android refuses
                    // an update across keys. See the flavor suffix note above. Libraries must
                    // not carry a suffix — they have no id of their own to change.
                    applicationIdSuffix = ".debug"
                }
            }
        }

        buildFeatures.apply {
            dataBinding {
                isEnabled = name != "hideapi"
            }
        }

        if (isApp) {
            this as AppExtension

            splits {
                abi {
                    isEnable = true
                    isUniversalApk = true
                    reset()
                    include("arm64-v8a", "armeabi-v7a")
                }
            }
        }

        compileOptions {
            sourceCompatibility = JavaVersion.VERSION_21
            targetCompatibility = JavaVersion.VERSION_21
        }
    }

    // Align Kotlin bytecode with Java (fixes: javac 21 vs Kotlin 21 mismatch when defaults differ).
    afterEvaluate {
        if (isApp) {
            tasks.matching { it.name.startsWith("package") && it.name.endsWith("Release") }.configureEach {
                doFirst {
                    check(rootProject.file("signing.properties").isFile && rootProject.file("release.keystore").isFile) {
                        "Release signing is required: provide signing.properties and release.keystore."
                    }
                }
            }
        }
        tasks.withType<KotlinCompile>().configureEach {
            compilerOptions.jvmTarget.set(JvmTarget.JVM_21)
        }
    }
}

task("clean", type = Delete::class) {
    delete(rootProject.buildDir)
}

tasks.wrapper {
    distributionType = Wrapper.DistributionType.ALL

    doLast {
        val sha256 = URL("$distributionUrl.sha256").openStream()
            .use { it.reader().readText().trim() }

        file("gradle/wrapper/gradle-wrapper.properties")
            .appendText("distributionSha256Sum=$sha256")
    }
}
