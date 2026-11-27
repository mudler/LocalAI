# LocalAI Android client

A client-only Android app. It shows the WebUI of LocalAI servers that run
somewhere else. Users add a server by address or by a P2P invite link.
Nothing runs on the phone. User documentation:
`docs/content/start/install/android.md`.

The app is a Capacitor app. Its WebView runs the launcher shell
(`launcher/src/renderer`) with an Android bridge over a client core. The
client core uses the shared modules of `launcher/src/core`. Native work is
done by Kotlin plugins.

## Layout

| Path | What it holds |
|---|---|
| `src/main.tsx` | The entry point. Renders the launcher `App` with the Android bridge. |
| `src/bridge/` | `androidBridge.ts` (the `CoreApi & ServersApi` bridge) and `backButton.ts`. |
| `src/core/` | `clientCore.ts` (config, servers, WebUI model, monitor), `configIO.ts` (the config file in the app data folder), `apkUpdate.ts` (update policy), `androidUpdater.ts` (update flow), `p2pTunnels.ts` (P2P forwarders), `events.ts`. |
| `src/plugins/` | `definitions.ts` (the TypeScript contract of every native plugin), `index.ts` (registration), `nativeTransport.ts` (the HTTP transport of the connection test). |
| `src/mobile.css` | All phone CSS. Every rule is under `html.lnr-mobile`, so the desktop gets none of it. |
| `test/` | vitest tests and fakes of the plugins. |
| `native/` | The Gradle project. Kotlin sources in `app/src/main/java/io/localai/client/`. |
| `p2pmobile/` | The P2P library: a Go module built into an AAR with `gomobile bind`. See `p2pmobile/README.md`. |
| `scripts/version-code.mjs` | The `versionCode` of a stable version or a dev build. |
| `scripts/write-manifest.mjs` | Writes the update manifest of one APK. CI runs it. |

Native plugins (Kotlin package `io.localai.client`):

| Plugin | Package | What it does |
|---|---|---|
| `NativeHttp` | `http` | The HTTP request of the connection test (OkHttp, no redirects, no cookies, TLS reasons). |
| `WebuiView` | `webui` | A second WebView that shows the server WebUI. It applies a Kotlin port of `launcher/src/core/webuiPolicy.ts`. Each server origin has its own WebView profile. |
| `P2P` | `p2p` | Pairing, the forwarders, and the foreground service that keeps them open. It keeps the device credential of a sign-in invite out of the web layer, encrypted with an Android Keystore key, and `WebuiView` posts it to the server to sign in. A sign-out in the WebUI (its `POST /api/auth/logout`) stops this for that server until the person opens it again from the server list or pairs again. |
| `Updater` | `update` | Download, checksum and signer check, install with `PackageInstaller`. |
| `Scanner` | `scan` | The QR scanner (zxing-android-embedded). |
| `AppLinks` | `links` | `localai://pair` links and shared text. |

## Shared code with the launcher

`android/` imports launcher code only through the aliases
`@launcher/renderer`, `@launcher/shared` and `@launcher/core` (plus
`@localai/ui-kit` and `@webui`). `aliases.mjs` defines them for Vite and
vitest. Keep the `paths` of `tsconfig.json` in step by hand. ESLint rejects
relative imports that leave `android/`, imports of `launcher/src/main` and
`launcher/src/preload`, and other `@launcher/*` roots.

A change in `launcher/` must keep the desktop launcher green:
`npm run typecheck && npm test` in `launcher/`. The desktop must not change at
desktop widths.

Strings: renderer strings go in `launcher/src/renderer/strings.ts`. Kotlin
strings go in `native/app/src/main/res/values/strings_<area>.xml`.

## Toolchain

| Tool | Version |
|---|---|
| Node.js | 22 |
| JDK for Gradle | 21 (Temurin) |
| Capacitor | 8.6.2 (core, android, cli) |
| Android Gradle Plugin, Gradle wrapper | 8.13.0, 8.14.3 |
| Kotlin Gradle Plugin | 2.4.21 |
| Android SDK | platform 36, build-tools 35.0.0, platform-tools |
| `compileSdk`, `targetSdk`, `minSdk` | 36, 36, 26 |
| For the P2P library | Go (the version of the root `go.mod`), JDK 17 or newer, NDK `30.0.16248370` |
| gomobile, gobind | `golang.org/x/mobile v0.0.0-20261117202452-47699113848a` (the `tool` directive of `p2pmobile/go.mod`) |

Set `JAVA_HOME` to JDK 21 and `ANDROID_HOME` to the SDK. For the P2P library,
the SDK must contain the NDK, or set `ANDROID_NDK_HOME`.

## Build

1. Build the web app and copy it into the native project. A fresh checkout
   needs this step before any Gradle task, because the Gradle projects of
   Capacitor and its plugins live in `node_modules`:

   ```sh
   cd android
   npm ci
   npm run build
   npx cap sync android
   ```

2. Optional: build the P2P library. Without it, the app builds, and the P2P
   plugin reports that P2P is not available (`BuildConfig.P2P_LIBRARY` is
   `false`).

   ```sh
   cd android/p2pmobile
   bash build.sh               # arm64 only
   bash build.sh arm64,amd64   # also x86_64, for an emulator
   ```

   The script writes `p2pmobile/build/p2pmobile.aar`. Gradle uses the file
   when it exists. Build the AAR before Gradle configures the project.

3. Build the APK:

   ```sh
   cd android/native
   ./gradlew assembleDevDebug
   ```

   The APK is in `app/build/outputs/apk/dev/debug/`.

### Flavors and build types

| Flavor | Application ID | Update feed |
|---|---|---|
| `stable` | `io.localai.client` | `releases/latest/download/android-update.json` |
| `dev` | `io.localai.client.dev` | `releases/download/dev-master/android-update-dev.json` |

The two flavors are separate apps. They install next to each other, and
neither one updates the other. Each flavor has a `debug` and a `release`
build type, for example `assembleStableRelease`.

### Gradle properties

Pass them with `-P<name>=<value>`. All are optional.

| Property | Default | What it does |
|---|---|---|
| `localaiVersionName` | `0.0.0-local` | The `versionName`. |
| `localaiVersionCode` | `1` | The `versionCode`, from 1 to 2100000000. |
| `localaiAbis` | `arm64-v8a` | ABIs, comma-separated. Allowed: `arm64-v8a`, `x86_64`. CI test builds add `x86_64` for the emulator. Published APKs are `arm64-v8a` only. |
| `localaiUpdateBase` | `https://github.com/mudler/LocalAI/releases` | The base URL of the update feed. Must be `https://`. An `http://` feed is accepted only together with `localaiSignWithDebug=true` (see [Test the self-update](#test-the-self-update)). |
| `localaiRequireSigning` | `false` | `true`: a missing release signing value fails the build. |
| `localaiSignWithDebug` | `false` | `true`: release builds use the debug key. For test APKs only. Cannot be used with `localaiRequireSigning`. |

A release build reads its key from these environment variables:
`ANDROID_KEYSTORE_FILE`, `ANDROID_KEYSTORE_PASSWORD`, `ANDROID_KEY_ALIAS` and
`ANDROID_KEY_PASSWORD`.

### Versions

`versionName` comes from `launcher/scripts/version.mjs`: tag `vX.Y.Z` gives
`X.Y.Z`, and a dev build gives `X.Y.(Z+1)-dev.<run>`. `versionCode` comes from
`scripts/version-code.mjs`:

- Stable: `((MAJOR * 1000 + MINOR) * 1000 + PATCH) * 100 + 99`. For example,
  `5.0.0` gives `500000099`.
- Dev: the whole minutes since `2026-01-01T00:00:00Z` at build time.

There are no release-candidate builds of the app.

## Tests

| Area | Command | What it covers |
|---|---|---|
| Web (`android/`) | `npm run lint && npm run typecheck && npm test && npm run build` | The bridge, the client core, P2P forwarders, the update policy and flow, links, the phone CSS rules, the version code, the manifest writer, the import rules. |
| Native (`android/native/`) | `./gradlew lintDevDebug testDevDebugUnitTest assembleDevDebug` | JVM unit tests: the WebUI policy parity test, WebView profiles, the device sign-in and credential store, the HTTP probe, links, the P2P supervisor, backoff, service and network policies, the APK download, signer check and install status. |
| P2P library (`android/p2pmobile/`) | `go vet ./... && go test ./...` | The Go facade on the host. |
| Emulator | `./gradlew connectedDevDebugAndroidTest -PlocalaiAbis=arm64-v8a,x86_64` | `SmokeTest`: the app starts, adds a URL server, loads the WebUI and loads the P2P library. Needs an emulator or a device, and `node test/fixtures/fake-localai.mjs 18080` with `adb reverse tcp:18080 tcp:18080`. |

The parity test reads `launcher/test/fixtures/webui-policy.json`, the same
fixture that the desktop policy tests write and use. A change to
`webuiPolicy.ts` must keep the Kotlin port in `webui/WebuiPolicy.kt` in step.

## CI

`.github/workflows/android-client.yaml` runs on pull requests, on pushes to
`master` and by hand, for changes under `android/`, the shared launcher code,
the UI kit, the P2P packages and `go.mod`/`go.sum`. Jobs:

| Job | What it does |
|---|---|
| `version` | The dev version and `versionCode`. |
| `web` | Lint, type check, vitest and the web bundle. |
| `p2p-aar` | Vet, test and build the P2P library for arm64 and x86_64. Checks the 16 KB alignment of the native libraries. A cache skips the build when the Go inputs did not change. |
| `apk` | Gradle lint, unit tests and the APK, with the AAR. Fails when the APK has no `lib/arm64-v8a/libgojni.so`, debug or signed. |
| `emulator-smoke` | The smoke test on an API 34 x86_64 emulator against the fake server. |
| `publish` | Attaches the dev APK to the `dev-master` pre-release. Runs only when publishing is on, and only after `apk` and `emulator-smoke` pass. Signed or debug-key, see [Publishing](#publishing). |

Pull requests, manual runs and `master` pushes while publishing is off build
a debug APK of the `dev` flavor (`arm64-v8a` and `x86_64`). The APK is a
workflow artifact: `localai-android-pr` (7 days) or `localai-android-dev`
(14 days). Each CI run signs with a new debug key, so a debug APK does not
update over another one.

`.github/workflows/android-client-release.yaml` is a reusable workflow.
`release.yaml` calls it for a release tag. It builds the signed `stable` APK
and attaches `LocalAI-Android.apk`, `LocalAI-Android.apk.sha256` and
`android-update.json` to the release. It does nothing for a tag that is not
`vX.Y.Z`.

### Publishing

Two repository variables control publishing:

| `ANDROID_PUBLISH` | `ANDROID_SIGNING` | A push to `master` in `mudler/LocalAI` |
|---|---|---|
| not `true` | any | Builds a debug APK as a workflow artifact. Nothing goes to a release. |
| `true` | not `true` | Builds the `dev` release APK (`arm64-v8a`) signed with a debug key made on the runner, and attaches `LocalAI-Android-dev-debug-key.apk` and its `.sha256` to `dev-master`. No update manifest. |
| `true` | `true` | Builds the `dev` release APK signed with the development key and attaches `LocalAI-Android-dev.apk`, its `.sha256` and `android-update-dev.json` to `dev-master`. A `vX.Y.Z` tag also gets the signed `stable` APK. |

The debug key changes on every CI run, and Android installs an update only
when its signer matches the installed app. So a debug-key APK never updates
over another one: to move to a newer one, uninstall the app first (this
removes its servers and sign-ins). CI publishes no manifest for it, because
the in-app updater would download the APK and then refuse it. The app treats
the missing manifest as "no update". Each mode removes the other mode's files
from `dev-master`.

To switch on the debug-key dev APK, set `ANDROID_PUBLISH` to `true`.

To switch on signed publishing, a maintainer does these steps in order:

1. Make two keystores: a release key for the `stable` app and a separate key
   for the `dev` app. Keep an offline backup of the release key in two
   places. If the release key is lost, users must uninstall the app and lose
   their servers and sign-ins.
2. Register the developer and both application IDs, with their signing keys,
   for Android developer verification.
3. Add the repository secrets (base64 of each keystore, and its passwords):

   | Secret | Use |
   |---|---|
   | `ANDROID_RELEASE_KEYSTORE`, `ANDROID_RELEASE_KEYSTORE_PASSWORD`, `ANDROID_RELEASE_KEY_ALIAS`, `ANDROID_RELEASE_KEY_PASSWORD` | The `stable` app, in `android-client-release.yaml`. |
   | `ANDROID_DEV_KEYSTORE`, `ANDROID_DEV_KEYSTORE_PASSWORD`, `ANDROID_DEV_KEY_ALIAS`, `ANDROID_DEV_KEY_PASSWORD` | The `dev` app, in `android-client.yaml`. |

4. Add the repository variables `ANDROID_RELEASE_CERT_SHA256` and
   `ANDROID_DEV_CERT_SHA256`: the SHA-256 digest of each signing certificate,
   in hex. Colons are ignored. A build signed by another certificate fails
   before upload.
5. Set the repository variables `ANDROID_PUBLISH` and `ANDROID_SIGNING` to
   `true`.

An app installed from a debug-key APK cannot update to a signed one. Its
users uninstall it and install the signed APK once.

With both variables set to `true`:

- A push to `master` in `mudler/LocalAI` builds `assembleDevRelease` with
  `-PlocalaiRequireSigning=true`, checks the certificate, and writes the
  checksum and the manifest. The `publish` job then waits for the `dev-master`
  release (it never creates it) and uploads `LocalAI-Android-dev.apk`,
  `LocalAI-Android-dev.apk.sha256` and `android-update-dev.json`, the
  manifest last.
- A `vX.Y.Z` tag runs the `android-client` job of `release.yaml`, which calls
  `android-client-release.yaml`. Its `check-feed` job downloads the attached
  files and compares them with the build.

Pull requests never get the secrets and never publish.

### The update manifest

```json
{
  "schema": 1,
  "applicationId": "io.localai.client",
  "versionName": "5.0.0",
  "versionCode": 500000099,
  "minSdk": 26,
  "apk": "LocalAI-Android.apk",
  "size": 41234567,
  "sha256": "<64 lowercase hex>",
  "commit": "<40 hex>",
  "notesUrl": "https://github.com/mudler/LocalAI/releases/tag/v5.0.0"
}
```

The app refuses a manifest with another `applicationId`, a `minSdk` above
the device, a `versionCode` that is not higher than its own, or an `apk` name
other than the channel's fixed asset name. It builds the APK URL from the
feed base and the fixed name, never from the manifest. The `.sha256` file
must agree with the manifest. The downloaded APK must match the size, the
SHA-256 and the signing certificate of the installed app.

### Test the self-update

Two test APKs signed with the debug key read a local feed over HTTP. Gradle
refuses an `http://` feed for any other build. `192.0.2.10` below stands for
the LAN address of the test machine; the phone must reach it.

1. Build the installed version and the update, with a higher version code:

   ```sh
   cd android/native
   ./gradlew assembleStableRelease -PlocalaiSignWithDebug=true \
     -PlocalaiUpdateBase=http://192.0.2.10:8090 \
     -PlocalaiVersionName=5.0.0 -PlocalaiVersionCode=500000099
   cp app/build/outputs/apk/stable/release/app-stable-release.apk /path/to/old.apk
   ./gradlew assembleStableRelease -PlocalaiSignWithDebug=true \
     -PlocalaiUpdateBase=http://192.0.2.10:8090 \
     -PlocalaiVersionName=5.0.1 -PlocalaiVersionCode=500000199
   ```

2. Make the feed. The stable channel reads `latest/download/`, the dev
   channel `download/dev-master/` (with `android-update-dev.json` and
   `LocalAI-Android-dev.apk`):

   ```sh
   mkdir -p feed/latest/download
   cp app/build/outputs/apk/stable/release/app-stable-release.apk feed/latest/download/LocalAI-Android.apk
   (cd feed/latest/download && sha256sum LocalAI-Android.apk > LocalAI-Android.apk.sha256)
   node ../scripts/write-manifest.mjs --app-id io.localai.client \
     --version-name 5.0.1 --version-code 500000199 \
     --apk feed/latest/download/LocalAI-Android.apk --name LocalAI-Android.apk \
     --commit 0000000000000000000000000000000000000000 \
     --notes-url https://github.com/mudler/LocalAI/releases \
     --out feed/latest/download/android-update.json
   ```

3. Serve it on the LAN address, install `old.apk` on the phone, and select
   **Check now** in **Settings > Updates**:

   ```sh
   node ../../launcher/scripts/fake-release-server.mjs 8090 "$PWD/feed" feed.log 192.0.2.10
   ```

`test/fakeFeed.test.ts` checks that the server answers at the paths the app
reads.

## Security rules

Keep these rules when you change the app:

- The Capacitor WebView loads only the bundled app (`allowNavigation` is
  empty).
- The WebUI WebView has no JavaScript interface, no Capacitor bridge and no
  `WebMessageListener`. File and content access are off, mixed content is
  `NEVER_ALLOW`, and Safe Browsing is on. `onReceivedSslError` and
  `onReceivedClientCertRequest` cancel.
- The network security config allows cleartext HTTP and trusts the `system`
  and `user` CA stores. Nothing bypasses a TLS error.
- The app stores no passwords and no API keys.
- P2P forwarders listen on loopback only. Each P2P server has its own
  loopback host (`127.0.0.2`, `127.0.0.3`, and so on), so cookies stay
  separate.
- The P2P identity is in `noBackupFilesDir/p2p/identity.key`. WebView data
  (`app_webview/`) and `webui_profiles.xml` are excluded from backup and
  device transfer (`res/xml/backup_rules.xml` and
  `res/xml/data_extraction_rules.xml`; keep the two files in step).
- OAuth and OIDC sign-in (GitHub, OIDC) completes inside the WebUI WebView:
  `OAuthSignInFlow.kt` (a port of the launcher's `OAuthSignInFlow`) lets the
  identity provider origins of one sign-in load in the main frame, from the
  server's login route to the return to the server, for at most 10 minutes
  and 5 origins. Subframes, windows, permissions and other schemes keep the
  rules above. `WebuiPolicyParityTest` runs the sign-in cases of the shared
  fixture; change the two ports together.
