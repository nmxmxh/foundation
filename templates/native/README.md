# Native Runtime Scaffold

This folder is the Foundation native shell scaffold. It uses Tauri v2 for the
device shell and `foundation/runtime-native` for binary Foundation dispatch.

The scaffold assumes the standard Foundation layout:

```text
project/
├── frontend/
└── native/
```

`src-tauri/tauri.dev.conf.json` starts the frontend dev server and
`src-tauri/tauri.prod.conf.json` builds and bundles `../../frontend/dist`. The
two kinds of path resolve from different directories: Tauri runs the
`beforeDevCommand`/`beforeBuildCommand` hooks from `native/` (where
`package.json` lives), so they `cd ../frontend`, while `frontendDist` resolves
from `src-tauri/`, so it is `../../frontend/dist`. If a project changes this
layout, update both Tauri config overlays and the native package scripts
together.

Common commands from the project root:

```bash
make native-dev
make native-build
make native-mobile-init
make native-bench
make native-doctor
```

Native IPC is a measured control boundary. Hot compute remains in
`foundation/runtime-sdk` lanes: WASM/SAB, Rust FFI, shared memory, framed stdio,
WebSocket, or HTTP fallback.

## Device Access

The scaffold enables no device plugins by default. That is intentional: every
device surface must be added with a matching Tauri plugin dependency,
capability, iOS privacy string, Android permission/feature, and Foundation
runtime test.

Foundation uses two device lanes:

1. WebView compatibility lane: browser APIs such as `getUserMedia`. Use this for
   preview, simple capture, uploads, and WebRTC-shaped workflows.
2. Native byte lane: Swift/Kotlin platform capture -> Rust plugin host ->
   `foundation/runtime-native` -> `foundation/runtime-sdk`. Use this for raw
   camera frames, microphone PCM, sensors, `wgpu`, WASM parity, and any
   performance-critical stream.

Camera, microphone, and sensor streams should not be routed through JSON or
owned by the WebView compositor when the feature depends on raw buffers or low
latency. Model them as binary frames, native buffer handles, packet-ring
descriptors, or `runtime-sdk` typed slots with epoch gating.

Official Tauri plugins such as geolocation, biometric auth, NFC, haptics,
barcode scanning, notifications, filesystem, and shell access are configuration
work once the product needs them. Keep their capability snippets inactive until
the dependency and product permission copy exist.

The default shell does not register storage commands. The `runtime-native`
in-memory store is bounded but ephemeral; it is not a credential vault. Install
and narrowly scope Tauri Stronghold or an audited OS keychain/keystore adapter
before persisting passwords, refresh tokens, signing keys, or tenant secrets.

The active capability list is explicit in `src-tauri/tauri.conf.json` and both
environment overlays. Keep example capabilities inactive until the app actually
uses the matching Tauri plugin.

Deep OS integrations such as iOS widgets, App Intents, Siri shortcuts, or
embedded SwiftUI/Compose views are outside the Foundation scaffold baseline.
Plan those as app-specific native extensions.

## Mobile Builds

```bash
npm run ios:init        # generate src-tauri/gen/apple (commit it)
npm run ios:dev
npm run ios:build

npm run android:init    # generate src-tauri/gen/android (commit it)
npm run android:dev
npm run android:build
```

Keep the `"tauri": "tauri"` script in `package.json`: the generated Xcode
"Build Rust Code" phase and Android Gradle task call back into
`npm run -- tauri <platform> *-script`.

### Prerequisites

iOS: Xcode, CocoaPods, the iOS Rust targets, and a simulator runtime
(`xcodebuild -downloadPlatform iOS`; without it Tauri reports "iOS platform not
installed").

```bash
rustup target add aarch64-apple-ios aarch64-apple-ios-sim x86_64-apple-ios
```

Android: JDK 17 or 21 (Gradle 8.14 does not run on newer JDKs), the SDK
platform and build-tools the generated Gradle project targets (compileSdk 36,
AGP 8.11 → build-tools 35), and NDK r28+ (16 KB page-aligned native libraries by
default, required for new Google Play submissions). Set `JAVA_HOME`,
`ANDROID_HOME`, and `NDK_HOME`.

```bash
brew install openjdk@21
brew install --cask android-commandlinetools
sdkmanager --licenses
sdkmanager "platform-tools" "platforms;android-36" "build-tools;35.0.0" "ndk;28.2.13676358"
rustup target add aarch64-linux-android armv7-linux-androideabi \
  i686-linux-android x86_64-linux-android
```

### Signing

- iOS: sign in to the Apple Developer account in Xcode and set
  `APPLE_DEVELOPMENT_TEAM` (or `bundle > iOS > developmentTeam`). `tauri ios
  build` cannot archive without it.
- Android: create an upload keystore outside the repository and wire a release
  `signingConfig` that reads a gitignored `gen/android/keystore.properties`,
  falling back to an unsigned build when the file is absent. Enable Play App
  Signing so the upload key is resettable.

### Release hardening checklist

The generated `gen/` projects are Tauri templates, not Foundation-owned, so
apply these once after `mobile:init`:

1. Android: `android:allowBackup="false"` plus `dataExtractionRules` excluding
   all domains, while any session material lives in WebView storage; remove the
   Android TV `leanback` feature and `LEANBACK_LAUNCHER` category unless the
   app targets TV.
2. iOS: put release-only Info.plist keys (for example
   `ITSAppUsesNonExemptEncryption`) in `src-tauri/Info.ios.plist`, which the
   Tauri CLI merges into the generated plist and survives re-init.
3. CSP: keep `object-src 'none'; base-uri 'self'`. A CSS-in-JS library that
   injects `<style>` at runtime needs `style-src 'unsafe-inline'` in the release
   overlay; record it in this README with the line
   `CSP exception: style-src 'unsafe-inline'` and set
   `app.security.dangerousDisableAssetCspModification` to `["style-src"]`, or a
   Tauri-injected style nonce silently disables `'unsafe-inline'`.
4. Every origin the app calls must be in `connect-src`/`img-src`/`font-src` of
   the matching overlay; remote font or script CDNs are blocked by default, so
   self-host them.
5. The backend's `ALLOWED_ORIGINS` must include the shell origins,
   `http://tauri.localhost` (Android) and `tauri://localhost` (iOS), or the API
   rejects the CORS preflight and every call from the app fails.
