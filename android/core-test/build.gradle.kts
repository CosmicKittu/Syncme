// Compiles the Android app's pure-Kotlin protocol core (app/src/main/java/.../core)
// on the JVM and runs an end-to-end test against the real Go daemon.
// Used by scripts/e2e-test.sh and CI; not part of the APK.
plugins {
    kotlin("jvm") version "2.0.21"
    application
}

repositories { mavenCentral() }

dependencies {
    implementation("org.json:json:20240303")
}


sourceSets {
    main {
        kotlin.srcDir("../app/src/main/java/com/cosmickittu/syncme/core")
    }
}

application { mainClass.set("E2EKt") }
