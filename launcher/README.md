# LocalAI Launcher (Electron)

A small desktop launcher for LocalAI: install or update the server, start and
stop it, show its log, and show the server's WebUI in the same window (or in
the system browser). It replaced the earlier Go/Fyne launcher, which was
removed from the repository. It uses the same data folder (`~/.localai`) and the same
`launcher.json`. See "Packaging and CI" below.

## Layout

- `src/main`: Electron main process (TypeScript, CommonJS output in `dist/node`).
  Pure modules with no Electron import, unit-tested under plain Node:
  `supervisor.ts` (process group, readiness by HTTP, bounded stop),
  `platform.ts` (the server build for this OS and the names that depend on
  it),
  `childEnv.ts`, `trayPolicy.ts`, `updateProvider.ts` (the `UpdateProvider`
  interface, the GitHub Releases implementation and the `releases/latest`
  redirect lookup), `checksum.ts` (fails closed), `atomicInstall.ts`,
  `updaterStatus.ts`, `models.ts`, `config.ts` and `configStore.ts`
  (launcher.json, one writer), `logs.ts`, `serverInstall.ts`,
  `serverVersion.ts`, `serverLaunch.ts`, `settings.ts`, the `sandbox*.ts`
  modules, `ipcGuard.ts` (sender check), `ipcValidate.ts` (input checks),
  `ipcHandlers.ts` (one handler per channel), `menuModel.ts`, `core.ts`
  (start order and actions), `webuiPolicy.ts` (the WebUI view's security
  decisions), `webuiState.ts` (the WebUI view's lifecycle) and
  `windowLayout.ts` (view bounds and stacking), `hostTools.ts` (free port,
  what listens on a port, hardware summary), `loginItem.ts` (Launch at
  login), `secretStore.ts` (secrets.json over an injected safeStorage),
  `apiKey.ts`, `network.ts` (bind address and LAN addresses),
  `webuiCredential.ts` (the WebUI sign-in cookie), `notifications.ts` (the
  notification streams, event parser, reconnect, link check, suppression rule).
  Electron-facing: `index.ts`, `menu.ts`, `tray.ts`, `selfUpdate.ts`, `webuiView.ts`, `dimView.ts`, `notifyGlue.ts` (desktop notifications, badge).
- `src/shared/ipc.ts`: the IPC contract. Every channel, its direction
  (invoke, event, send) and its payload types. `test/preload.test.ts` fails
  when the preload or the main handler table disagrees with it.
- `src/preload`: the context bridge (sandboxed, context isolation on, no Node in the renderer).
- `src/renderer`: React + Vite. The status bar and the shell screens. It
  uses the `@localai/ui-kit` package and the WebUI's theme CSS (aliases in
  `vite.config.mts`). `bridge.ts` lists the bridge methods the shell can do
  without; a control whose method is missing is hidden. `dev/` holds a fake
  bridge and named fixtures for the component tests.
- `test`: vitest. `test/fixtures/fake-server.cjs` stands in for the server and
  starts a grandchild so group signals are exercised.
- `scripts/smoke-packaged.sh`: runs a packaged AppImage under `xvfb-run`, a
  macOS `.app`, or the installed Windows `local-ai-launcher.exe` (Git Bash).
- `scripts/webui-security.sh`: runs the built app under `xvfb-run` with a
  hostile server page and reports how the WebUI view handled each attempt.

## Commands

Node 22 or newer.

```sh
npm install
npm test                 # unit tests
npm run typecheck
npm run build            # main (tsc) and renderer (vite)
npm start                # run from source (needs a display)
npm run dist:appimage    # release/LocalAI-Launcher-x86_64.AppImage and latest-linux.yml
SMOKE_DIR=/short/scratch/dir scripts/smoke-packaged.sh release/LocalAI-Launcher-x86_64.AppImage [--appimage-extract-and-run]
```

Environment variables read by the app (all optional, meant for tests):
`LOCALAI_LAUNCHER_SERVER_BIN`, `LOCALAI_LAUNCHER_ENDPOINT`,
`LOCALAI_LAUNCHER_FEED_URL` (generic feed for the launcher's own updates,
packaged builds only), `LOCALAI_LAUNCHER_SIGNAL_FILE` (milestone lines for the smoke test)
and `LOCALAI_LAUNCHER_SANDBOX_ATTACH` (the AppArmor attach pattern for the
profile setup test; without it, setup is refused because the attach rule is
not decided yet).

Command-line switch: `--hidden` starts without showing the window when a
tray icon exists (for a login item). After a hidden start the launcher never
opens the WebUI in the browser by itself, also with "open in browser" on;
only a user action does.

"Launch at login" (Settings, key `start_on_boot`): on Linux the launcher
writes `localai-launcher.desktop` to the XDG autostart folder
(`$XDG_CONFIG_HOME/autostart`, default `~/.config/autostart`), through a
temporary file and a rename. It runs the AppImage file (`APPIMAGE`) with
`--hidden`, and the launcher writes it again at each start, so a moved
AppImage keeps working. Turning the setting off removes the file. A run from
source has no AppImage file, so the setting cannot be turned on there and
Settings says why. On macOS it is Electron's login item; macOS passes no
arguments, so the launcher treats a start at login as a hidden start. The
setting is saved only when the change took effect. On Windows it is
Electron's login item (a `Run` entry of the current user) with `--hidden`.

## Config

The launcher reads and writes `~/.localai/launcher.json` (on Windows
`%LOCALAPPDATA%\LocalAI\launcher\launcher.json`), with the keys of the
Go launcher. It adds keys of its own, which an older launcher keeps and
ignores: `show_status_bar` (missing means true; View menu, tray, Settings),
`sandbox_notice_dismissed_version` (the launcher version for which the user
chose "Not now" on the sandbox notice), `open_webui_in_browser` (missing means
false), `desktop_notifications` (missing means true, see Desktop
notifications below), `remotes` (the saved servers, see Servers below), `default_target`,
`keep_local_running`, `update_channel`, `remote_server_url` (the old "last
server chosen in Connect": read once for the migration, never written again),
`webui_permissions` (remembered microphone and camera choices
per origin), `auto_check_updates` (missing means true: packaged builds check
for a LocalAI update once a day and never install without asking),
`last_update_check` (when the last server update check finished, in
milliseconds since the epoch), `include_prereleases` (read only: true reads as
the `rc` channel; never written again) and `secrets_plaintext_file` (missing means false; see Secrets).

`environment_vars` is read once: at the first start, its values (plain text,
as the Go launcher wrote them) move into the secret store and the key is
emptied. An older launcher then starts the server without them.

## Servers, the default target and the update channel

`launcher.json` keeps these keys (`src/main/serversConfig.ts`; Go keys and
unknown keys are kept; writes go through the one config queue):

| Key | Meaning |
|---|---|
| `remotes` | A list of `{id, name, url}`: the saved servers. The url is an origin (no path, no credentials, no secret). At most 20, names of 1 to 60 characters, unique addresses. The ids are made by the main process. |
| `default_target` | `local` or the id of a saved server. Missing, or an id that is not in the list, means `local`. Written only when it changes. |
| `keep_local_running` | Opt-in, missing means false. See below. |
| `update_channel` | `stable`, `rc` or `dev`. Missing means `stable`; an old `include_prereleases: true` reads as `rc`. |
| `dev_channel_confirmed` | The user confirmed Development once. |
| `remote_server_url` | The old "last server chosen in Connect". Moved into `remotes` once (name from the host); it never becomes the default. Read, never written. |

When a remote is the default (core.ts, `localManaged`): the window opens it at
once, and the launcher does not install, start, update, health-check or
auto-start the local server. A running local server that the launcher
started stops after the new page has loaded. With `keep_local_running` on,
the local server keeps its own rules in the background. Picking a server in
the bar switcher makes it the default. Switching to This computer starts it
(the Install screens come first when it is missing). A remote is saved and
opened only after a passing test (`src/main/testConnection.ts`: no
credentials, certificate errors never skipped, no redirect off the origin,
8 s timeout; a server that asks for a sign-in counts as reachable);
"Save without testing" exists only in Settings. While the remote default
does not answer, the shell shows the recovery screen and the launcher retries
every 30 s (`remoteMonitor.ts`); it never starts the local server by itself.
Plain http to any host is accepted, with a warning in the form and an amber
"Remote, not encrypted" chip in the bar.

The IPC channels are `launcher:servers:{get,event,test,add,update,remove,
set-default,cancel-switch,retry,set-keep-local,open-switcher}`. They take a
saved server id or a typed address, never an origin. The switcher shortcut is
Ctrl+Shift+S (Cmd+Shift+S on macOS), a View menu accelerator, so it works
while the WebUI has the focus (`menuModel.ts` lists what was checked).

### Update channel

Settings, Updates. `stable` is the default and uses the latest release.
`rc` also offers release candidates (this is the old pre-release switch, and
the only channel the launcher's own updater follows). `dev` follows the
rolling development build:

- Where it comes from: the `dev-master` pre-release of the LocalAI
  repository, with `local-ai-master-<os>-<arch>` and a `.sha256` file for each
  platform. All names are in `src/main/devFeed.ts`; the fake release server in
  `scripts/fake-release-server.mjs` serves the same names for tests.
- The same fail-closed checksum check and atomic swap as a release. "Newer"
  means a different build id (the short commit sha from the release text),
  not a higher version. The previous binary stays as `local-ai.previous`,
  in both directions, so Back to Stable keeps the development build as the
  rollback copy.
- The server starts with `LOCALAI_PREFER_DEV_BACKENDS=true` (`serverLaunch.ts`);
  backends that are already installed are not touched.
- The first change to Development needs a confirmation (the checkbox dialog).
  The bar and About show a Development chip while the window shows This
  computer. With a remote default the channel applies to This computer only.

## Packaging and CI

electron-builder packs the app (`electron-builder.yml`). Electron and
electron-builder are exact pins in `package.json`. `test/builderConfig.test.ts`
runs the builder's own schema validation on the config and checks the
targets, names, entitlements and publish rules. `test/builderFiles.test.ts`
fails when a main-process module is not in the `files:` list.

### Local builds

```sh
ELECTRON_SKIP_BINARY_DOWNLOAD=1 npm ci   # the builder downloads Electron itself
npm run dist:appimage                    # x64
npm run dist:appimage:arm64              # arm64 (packed on any host; run it on arm64)
npm run dist:mac                         # on macOS: DMG and zip, unsigned without CSC_* variables
npm run dist:win                         # on Windows: NSIS installer and zip, unsigned without WIN_CSC_* variables
# With a version, as CI does:
npm run build && npx electron-builder --linux AppImage --x64 --publish never \
  -c.extraMetadata.version=$(node scripts/version.mjs tag v4.2.1)
SMOKE_DIR=/short/dir scripts/smoke-packaged.sh release/LocalAI-Launcher-x86_64.AppImage --appimage-extract-and-run
```

Output in `release/`:

| Platform | Files |
|---|---|
| Linux x64 | `LocalAI-Launcher-x86_64.AppImage`, `latest-linux.yml` |
| Linux arm64 | `LocalAI-Launcher-arm64.AppImage`, `latest-linux-arm64.yml` |
| macOS arm64 | `LocalAI.dmg`, `LocalAI-Launcher-<version>-arm64-mac.zip`, blockmaps, `latest-mac.yml` |
| Windows x64 | `LocalAI-Launcher-Setup-x64.exe` (NSIS, per-user), `LocalAI-Launcher-win-x64.zip` (portable), the installer blockmap, `latest.yml` |

The Windows build runs on Windows. On Linux, electron-builder makes the
zip without Wine, but the NSIS installer needs Wine, and signing needs
Windows or osslsigncode.

A packaged x64 AppImage is about 120 MB (126 MB for 4.12.1; 276 MB unpacked).

`scripts/version.mjs` gives the version: `tag v4.2.1` gives `4.2.1`,
`tag v4.2.1-rc.1` (or `-rc1`) gives `4.2.1-rc.1`, `-beta.N` and `-alpha.N` are
accepted, and any other tag shape fails. `dev <run>` reads tags on stdin and
gives `<newest vX.Y.Z, patch + 1>-dev.<run>`; other tag shapes are skipped. A
`-dev.N` build never updates itself (`selfUpdateAllowed` in
`src/main/updaterStatus.ts`).

### Workflows

| Workflow | Trigger | Permissions | What it does |
|---|---|---|---|
| `launcher-electron-build.yaml` | `workflow_call` | `contents: read` | npm ci, type check, unit tests, build with `--publish never`, `.sha256` files, packaged smoke (AppImage under Xvfb with `--appimage-extract-and-run`; on macOS the DMG is signed, notarized and stapled, then checked with `codesign`, `spctl` and `stapler`, and the app from the mounted DMG is checked the same way before the smoke), then workflow artifacts. amd64 on `ubuntu-22.04`, arm64 on `ubuntu-24.04-arm`, macOS on `macos-latest`, Windows on `windows-latest` (type check, `test/windows.test.ts`, `test/windowsHost.test.ts` and the builder config test; the rest of the unit suite is POSIX-specific and runs in the other jobs; then a silent per-user install of the NSIS installer and the smoke on the installed app). |
| `launcher-electron-release.yaml` | `workflow_call` from `release.yaml`, every `v*` tag, after GoReleaser | build: read; upload: `contents: write`; check: read | Version from the tag, build with required macOS signing (Windows signing when its secrets exist), feed files, upload to the existing release (no checkout, files first, feed files last, `--clobber`), then a check of the attached feed. |
| `launcher-electron-dev.yaml` | push to `master` and pull requests (path filter), manual run | build: read; publish: `contents: write` | Development builds as artifacts `localai-launcher-dev-linux-amd64`, `-linux-arm64`, `-macos-arm64`, `-windows-amd64` (14 days; pull requests: amd64 AppImage only, no secrets, 7 days). Master builds are not cancelled by a newer push (a waiting run is replaced). After they pass, the `publish` job (master pushes only, never pull requests or manual runs) attaches the AppImages, the DMG, the Windows installer and zip, and their `.sha256` files to the `dev-master` pre-release with `--clobber`, next to the server binaries. It waits for that release (made by `master-dev-binary.yml`) and never creates it. Manual run: build and smoke only. |

Signing uses the repository secrets `MACOS_CERTIFICATE` and
`MACOS_CERTIFICATE_PWD` (as `CSC_LINK` and `CSC_KEY_PASSWORD`) and
`MACOS_NOTARY_KEY`, `MACOS_NOTARY_KEY_ID`, `MACOS_NOTARY_ISSUER_ID` (written to
a key file for `APPLE_API_KEY`, `APPLE_API_KEY_ID`, `APPLE_API_ISSUER`).
electron-builder makes its own temporary keychain. It signs, notarizes and
staples the app. It does not notarize the DMG file, so the workflow does that in
`scripts/sign-dmg.sh` after the build: a Developer ID signature with a secure
timestamp, `notarytool submit --wait` (the job fails unless the status is
`Accepted`, and prints the notarization log), then `stapler staple`. The
script uses its own temporary keychain and key file, and deletes both when it
ends. The step also writes the new hash and size of the DMG into `latest-mac.yml`,
and the checksum file is made after stapling, so it matches the file that
users download. A release build
(`require-signing: true`) fails when one of the five secrets is missing, so it
never makes an unsigned release DMG. A development build without the secrets is
unsigned and says so. A development build also has `dev` in its file names
(`LocalAI-Launcher-dev-x86_64.AppImage`, `LocalAI-Launcher-dev.dmg`), so it is
never mistaken for a release file.
Windows signing uses the optional repository secrets `WINDOWS_CERTIFICATE`
(a base64 `.pfx` file) and `WINDOWS_CERTIFICATE_PWD`, passed to
electron-builder as `WIN_CSC_LINK` and `WIN_CSC_KEY_PASSWORD`. With both,
the builder signs the app, the installer and the uninstaller with signtool,
SHA-256 and an RFC 3161 timestamp, and the job checks the signatures with
`Get-AuthenticodeSignature`. Without them, the job warns, sets
`CSC_IDENTITY_AUTO_DISCOVERY=false` and makes an unsigned build, also for a
release: Windows signing is never required. A signed build writes the
publisher name into `app-update.yml`; electron-updater then installs only
updates signed by that publisher. So once a signed release is out, a later
unsigned release cannot update those installs. Keep signing on once it is
on.
Repository variables: `LAUNCHER_DEV_ARM64=false` (no arm64 on master, for the
Actions storage quota), `LAUNCHER_DEV_SIGN_MACOS=false` (unsigned dev DMG).

### How a release gets the feed

The updater reads `latest-linux.yml`, `latest-linux-arm64.yml`,
`latest-mac.yml` or `latest.yml` (Windows) from the newest GitHub release. So every server release,
release candidates too, must carry them. `detectUpdateChannel: false` keeps the
name `latest-*` for a release candidate. The upload job attaches the packages
and checksums first and the feed files last. The check job reads each feed
file from the release and fails when its version is not the tag's version, or
when a file it names is missing or has another size.

The workflow never publishes a release. Today a release is published by hand
and GoReleaser attaches its files after that, so a published release has no
launcher feed until the upload job ends, and installed launchers report an
update error in that time. To keep a release a draft until the feed is
checked, push the tag with the release still a draft and publish it after the
check job is green. A publish by the workflow token would not start the
workflows that run on `release: published`.

The release files are `LocalAI-Launcher-x86_64.AppImage`,
`LocalAI-Launcher-arm64.AppImage`, `LocalAI.dmg` (each with a `.sha256` file),
the macOS zip `LocalAI-Launcher-<version>-arm64-mac.zip` with its `.sha256`
file, `LocalAI-Launcher-Setup-x64.exe` and `LocalAI-Launcher-win-x64.zip`
(each with a `.sha256` file), the blockmaps, and the four feed files. The names of the AppImages and
the DMG are the names that the download links of earlier releases use.

### A second update channel later

1. Build the channel's releases with `-c.publish.channel=<name>` (for example
   `beta`); the builder then writes `<name>-linux.yml` and `<name>-mac.yml`.
   Add a `channel` input to the build and release workflows and add those
   files to the upload and the feed check.
2. In the app, set `autoUpdater.channel` and `allowPrerelease` from a setting
   (the Settings mock has a channel selector), behind the `UpdateProvider`
   interface.
3. Note that electron-updater on GitHub picks the newest release of the same
   pre-release name: a `4.2.1-rc.1` launcher only offers later `rc` releases,
   not the next stable one. A channel needs its own tag suffix rule.

### AppImage and DMG changes (account)

State of the packaging work, compared with the earlier Go launcher's packages:

| AppImage | State |
|---|---|
| 1. Build tool | Done. electron-builder replaces `build.sh` for this launcher; exact pins of Electron 45.3.2 and electron-builder 26.18.0. The files the builder downloads (Electron zip, AppImage toolset) are not checked a second time by this repository. |
| 2. Size and dependencies | Done. About 120 MB; the install page states the new requirements. |
| 3. FUSE | Done. Documented; `--appimage-extract-and-run` tested on a host without FUSE 2 and used by the CI smoke. |
| 4. Sandbox | Partly. The fallback stays and is shown. The desktop entry no longer forces `--no-sandbox`. A start with the sandbox on was not seen in a test (the test host restricts user namespaces). The profile attach rule is open. |
| 5. Environment leakage | Done (lifecycle code); the smoke shows the stripped variables. |
| 6. Desktop integration | Done. `local-ai-launcher`, categories Development and Utility, icon, `desktopName`, `StartupWMClass=local-ai-launcher`. |
| 7. Atomic self-update | Not done. "Restart to update" still uses electron-updater's own install. |
| 8. Builder file list | Done. The test also covers modules that nothing imports yet. |
| 9. Smoke | Done for x64 (release and development versions). arm64 runs only in CI. |
| 10. CI | Done as workflows; not yet run on hosted runners. |

| DMG | State |
|---|---|
| 1. Build tool | Done in the workflow, not run yet. electron-builder signs, notarizes and staples the app. `scripts/sign-dmg.sh` signs, notarizes and staples the DMG file. |
| 2. Entitlements | Done. JIT, unsigned executable memory, audio input, camera, and usage strings for the WebUI voice features. Not set any more: the network client and server keys (they apply only in the App Sandbox). Library validation stays on: the server is a separate process. Not tested on a Mac. |
| 3. Order of staple | Partly. The app is stapled before the DMG is made, and CI checks `codesign`, `spctl` and `stapler validate` on the app from the DMG, but not with the network off. The DMG itself is signed, notarized and stapled by `scripts/sign-dmg.sh`, and CI checks it with `codesign`, `spctl` and `stapler validate`. |
| 4. Updater zip | Done (zip and `latest-mac.yml`). |
| 5. Architecture | Done. Apple Silicon only. |
| 6. Dev builds on master | Done. Signed and notarized when the secrets are present; `LAUNCHER_DEV_SIGN_MACOS=false` is the documented fallback. |
| 7. Shared signing runner | Done. GitHub-hosted `macos-latest` so no lock. |
| 8. Version | Done (`scripts/version.mjs`). |
| 9. Quarantine | Not done. Needs a clean Mac with two macOS versions. |

## Limits of the prototype

No worker mode.

## Windows

- Server build: `platform.ts` maps `win32`/`x64` to `windows`/`amd64`.
  There is no `windows/arm64` server, so Windows on Arm runs the x64
  launcher, which reports `x64`. The release asset is
  `local-ai-v<version>-windows-amd64.exe` (GoReleaser adds `.exe`); the
  development asset is `local-ai-master-windows-amd64.exe` with
  `local-ai-master-windows-amd64.exe.sha256`. The installed files are
  `bin\local-ai.exe` and `bin\local-ai.previous.exe`.
- Download check: the same checksum rule as on the other platforms, and the
  file must be a PE executable (`MZ` and the `PE` signature). macOS expects
  Mach-O and Linux ELF. The rename over the old binary is tried again for
  about 2 s when Windows reports `EPERM` or `EBUSY` (an antivirus scan).
- Start and stop: the server starts with no console window. Windows has no
  SIGTERM, and a console program with no window cannot be asked to close
  from another process; the server has no shutdown endpoint. So the stop is
  forced at once: `%SystemRoot%\System32\taskkill.exe /PID <pid> /T /F`,
  and when taskkill cannot run, the server alone is ended. The server keeps
  its backends in a Job Object that ends them with it. Requests that run at
  that time are cut off.
- Environment: Windows variable names have no case. The rules of
  `childEnv.ts` and Settings match `Path`, `api_key` and `electron_*` like
  their upper-case names, and a later layer replaces a name of an earlier one
  whatever its case.
- Helper programs (`netstat`, `tasklist`, `taskkill`) run from
  `%SystemRoot%\System32`, never from `PATH`. "What listens on this port"
  reads `netstat -ano` and finds listening sockets by the foreign address
  `*:0`, because the state column is translated.
- Data: `%LOCALAPPDATA%\LocalAI` (models, backends, server data, logs), shared
  with `local-ai.exe` from a terminal; the launcher's own files (launcher.json,
  secrets.json, bin, metadata, downloads) in `%LOCALAPPDATA%\LocalAI\launcher`,
  apart from the `bin` folder of `install.ps1`. A `%USERPROFILE%\.localai\launcher.json`
  of an earlier build keeps the old layout (`src/main/paths.ts`). An absolute
  `LOCALAI_BASE_PATH` moves the default data folder on every platform. Electron
  keeps its own cache in `%APPDATA%\LocalAI Launcher`. Folder modes (`0o700`) do
  nothing on Windows; the files inherit the ACL of the user profile.
- Window and tray: no menu bar in the window (the accelerators work). The
  tray icon is 16 px; a click shows the window. Closing the window hides it
  to the tray. The app user model id is `io.localai.launcher`, the `appId`
  of the installer.
- Self-update: the installed app updates with the next NSIS installer
  (electron-updater). The portable zip has no uninstaller next to it
  (`isPortableWindows`), so it does not update itself.
- Not tested on a Windows host by this repository's own tests until the
  Windows CI job runs: `test/windowsHost.test.ts` (the real taskkill stop
  and the real netstat lookup) runs only there.

## Launcher updates

electron-updater checks and downloads; it runs only in a packaged release
build. A run from source and a development build (`x.y.z-dev.N`) do not
update themselves (a development build checks only a test feed set with
`LOCALAI_LAUNCHER_FEED_URL`). Downgrades are never offered. Pre-releases
are offered only on the Release candidates channel (Settings, Updates;
config `update_channel`; the Development channel does not change the
launcher's own updates); a launcher on a release-candidate version takes
stable updates only, because `updaterSettings` always sets electron-updater's
`allowPrerelease`, which it would otherwise turn on for such a version. The
same setting makes the server check use the newest release including
pre-releases. `selfUpdateGate` and `updaterSettings` (`updaterStatus.ts`) are
the only gate; `test/selfUpdateGate.test.ts` covers every combination.

"Restart to update" on Linux does not call electron-updater's install
(`quitAndInstall`), which deletes the AppImage before it moves the new one
in. `appImageUpdate.ts` takes the file of electron-updater's
`update-downloaded` event (`downloadedFile`, which electron-updater already
checked against the feed's SHA-512), checks its size and the ELF and
AppImage type 2 magic, stages a copy in the AppImage's own folder with the
file's mode, fsyncs it, checks the copy's SHA-512 against the feed, and
renames it over the AppImage (`atomicInstall.ts`, shared with the server
install). The file name stays. Then the launcher stops the server and starts
the AppImage file again. On any failure the old file is untouched, the
launcher keeps running, and the updater status gives the reason
(`not-appimage`, `no-download`, `size-mismatch`, `not-an-appimage`,
`checksum-mismatch`, `read-only`, `no-space`, `failed`). macOS and Windows
keep electron-updater's install. On Windows that runs the next NSIS
installer (per-user, no administrator prompt), which closes the launcher,
installs and starts it again.

Limits: the copy, the fsync and the checksum run on the main thread (about
1.1 s for a 150 MB file on a local SSD; the window does not respond
meanwhile). A file name with a version in it is kept as it is;
electron-updater would rename it. The updated file is not added to a
desktop menu by the AppImage runtime (`APPIMAGE_SILENT_INSTALL` is not
used).

## Secrets, network exposure and environment variables

The API key, the worker token and the values of the server's environment
variables live in `~/.localai/secrets.json`, never in launcher.json and never
in a log line (`secretStore.ts`):

| Where | When | Who can read a value |
|---|---|---|
| OS secure storage (`safeStorage`): base64 of each encrypted blob in a 0600 file, written atomically | the OS has secure storage (macOS Keychain; on Linux a keyring, not Chromium's `basic_text` fallback) | the main process, as this user, while the keyring is unlocked |
| memory only ("Your system has no keyring") | no secure storage: the default | the main process, until it quits |
| a plain 0600 file | no secure storage and the user turned on "Keep secrets in a file" (Settings, Advanced) | anything running as this user |

How the store finds secure storage: once at start, it encrypts and decrypts
a fixed probe string (never a secret). On Linux the backends `basic_text`
and `unknown` do not count, and the probe must also work. On macOS and
Windows the probe alone decides. The availability flags are not used: on a
Linux host with no keyring, Electron 45.3.2 reports the backend
`gnome_libsecret`, `isAsyncEncryptionAvailable()` answers true and
`encryptString()` throws. If encryption fails later anyway (it throws or
gives an empty result), the value is kept in memory, the whole store is
session-only until the launcher quits, and the log names the secret, never
its value.

The store uses the synchronous `encryptString` and `decryptString`, which
Electron 45 marks deprecated and logs a warning for. The async calls are not
used: on the same host `encryptStringAsync()` works without a keyring,
because it encrypts with Chromium's fixed built-in key (blobs start with
`v10`), so a round trip with them cannot tell a keyring from no keyring.

A plain file moves into secure storage when it becomes available. Values that
cannot be decrypted now (a locked keyring) are kept in the file unchanged and
are never overwritten. Old plain-text values in launcher.json stay there, and
are used for the session, until they can be stored.

How a secret travels:

- To the server: only through its environment (`LOCALAI_API_KEY` for the
  key, the user's variables by name), never on its command line, which any
  user can read with `ps`. The child environment is built explicitly: the
  launcher's own variables are dropped (Electron, AppImage, the loader
  path), then the user's variables, then the launcher's (the key).
- To the renderer: never by default. `getNetwork` sends a hint (`lai-` and
  the last 4 characters) and `getEnvVars` sends names only. Show calls
  `revealApiKey`, which answers once per request, at most 5 times a minute
  and not twice within a second. Copy calls `copyApiKey(format)`: the main
  process writes the key, or a ready "Set up an app" example with it, to the
  clipboard and clears the clipboard after 60 seconds if the text is still
  there.
- To the WebUI view: the server asks for the key once it has one. The main
  process sets the server's own sign-in cookie (`token`, HttpOnly,
  SameSite=Lax) for the local origin in that origin's session, so the page
  signs in by itself. It is a session cookie (no expiry). Without secure
  storage (no keyring) the key lives only for this session, so the launcher
  also clears the cookies of that WebUI partition at quit
  (`webuiCredential.ts`); with secure storage nothing more is done.
- Logs: lines from the server, the log tail and the failure detail are
  scrubbed of every secret value of 12 characters or more that was given to
  a server in this session, on top of the log hub's key=value rules.

**Allow other devices.** `getNetworkPlan` returns what will change: the bind
address (`0.0.0.0:<port>`), the addresses other devices use (the non-internal
IPv4 addresses of this computer, without container and VM bridges) and
whether a key is created. Connect shows
it and applies it only on Allow, with `setNetworkExposed(true, planId)`; the
plan id is single use and expires after 5 minutes. The key is created before
the address changes, so the server never listens on the network without one.
Turning it off goes back to `127.0.0.1:<port>` and keeps the key. A typed
address that reaches other devices (Settings, Network) also creates the key,
and so does a start with such an address and no readable key (no keyring, so
the last key lived only for that session, or an old config that exposed the
server without one). In the last case, when secrets are kept across a
restart (a keyring, or the plain file), Connect shows once "A new API key was
created because LocalAI was open to your network without one." until the user
selects OK (`launcher:api-key:dismiss-notice`), and the log says so without
the key. Without a keyring a lost key is expected, so there is no notice.
The WebUI view always uses the loopback URL. A running server restarts to
apply the change (the plan says so). The Go launcher had no CORS or API key
settings, so there are none here beyond the key.

**Environment variables** (Settings, Advanced): names match
`^[A-Za-z_][A-Za-z0-9_]*$`, at most 128 characters, values up to 32 KB, at
most 200 variables. Refused names: the variables the launcher strips
(`ELECTRON_*`, `APPDIR`, `APPIMAGE*`, `ARGV0`, `OWD`, `LD_LIBRARY_PATH`,
`LD_PRELOAD`, `NODE_OPTIONS`, `GSETTINGS_SCHEMA_DIR`, `CHROME_*`) and the ones
it sets itself (`LOCALAI_API_KEY`, `API_KEY`, `LOCALAI_ADDRESS`, `ADDRESS`,
`PATH`, `LOCALAI_BASE_PATH`). `LOCALAI_BASE_PATH` is the data folder (the
parent of the models folder), so the server never picks a base folder of its
own from the current folder. PATH: the server gets the system PATH unchanged, because a user
value would replace the search path of the server and of every backend it
starts; change the system PATH instead. An inherited `API_KEY` or
`LOCALAI_API_KEY` of the desktop session is dropped too. A name of this kind
in an old config is listed as "Not passed".

Channels: `launcher:network:get`, `:plan`, `:set-exposed`,
`launcher:api-key:regenerate`, `:reveal`, `:copy`, `:dismiss-notice`, `launcher:env:get`, `:set`,
`:delete` and `launcher:secrets:state`, each behind the sender check and the
input checks. `test/coreSecrets.test.ts` proves with the fake server that a
value reaches the server by its environment and not by its arguments, its
`/proc` command line, the logs, the config file, the secrets file as plain
text, or anything the renderer gets without asking.

## Sandbox on Linux

The AppImage start script that electron-builder generates adds `--no-sandbox`
when `unshare -Ur true` fails. On Ubuntu 24.04 that is the default, because
unprivileged user namespaces are restricted by AppArmor. The status bar
shows a "Sandbox off" chip in that case. The sandbox guide opens by itself
once, when the server is first ready and the notice is not answered. "Not
now" saves the launcher version; for that version the guide does not open by
itself again, and the chip stays.

"Set up sandbox profile" is disabled while `SandboxView.setupAvailable` is
false, which is the case without `LOCALAI_LAUNCHER_SANDBOX_ATTACH` (the
AppImage today): the guide says "Setup is not available yet. The window works
as before." and shows no command. The main process refuses setup in that case
too. A profile on the AppImage's mount path would be user-writable, so the
planned fix for Ubuntu is a `.deb` that installs the profile for a root-owned
path. "Not now" and "open the WebUI in my browser instead" stay.

## WebUI view

When the local server is ready, the window shows the server's own WebUI
(`/app`) in a second view, above a 30 px status bar that the shell draws.

### Window layout

The window is a `BaseWindow` with up to four `WebContentsView`s, from bottom
to top: the shell (the launcher's React page, the only view with the preload
bridge), the WebUI view, the dim layer (only while the server is stopped),
and the corner dot. `windowLayout.ts` computes their
bounds and order from the state of `webuiState.ts` on every state change and
resize. The window has a minimum size of 720 by 480.

| Layout | When | Views |
|---|---|---|
| `shell-only` | no page yet (server not ready), local server failed, install or update, a shell screen (Logs, Settings, Connect), worker mode, the view crashed or did not load, WebUI in the browser | the shell fills the window; the WebUI view is hidden, kept alive and not reloaded |
| `webui`, bar shown | server ready, or a remote server; also a stopped or starting local server while the page is kept | the WebUI fills the window except the bottom 30 px and the shell's strips (insets); the shell's bar and strips show around it |
| `webui`, bar hidden | as above, with "Show status bar" off | the WebUI fills the window; a 28 px dot view sits 6 px from the bottom-right corner |

A hidden bar comes back by itself while a state needs the user: server
failed, a launcher or LocalAI update on offer, sandbox notice not answered,
remote server, or a problem with the view (`barForced` in the state).

**Why a BaseWindow and a separate dot view.** Electron 45 has no
click-through for part of a view: a view takes every click inside its
bounds. So the shell cannot sit above the WebUI with a hole in it. Also, a
`BrowserWindow`'s own page cannot be raised above a child view. With
child views of a `BaseWindow`, the main process can reorder them
(`addChildView` on a view that is already a child moves it to the top):

- The shell is raised above the WebUI (`shellAbove` in the state) only while
  a popover of the bar is open (`setOverlay(true)`). Then the shell view
  covers the whole window with a transparent page and gets every click.
- Banners do not raise the shell. The update banner above the view and the
  "stopped" or "starting" banner above the bar take their own strips: the
  shell sends their heights with `setInsets({ top, bottom })`, and the WebUI
  view gives way to them.
- While the "stopped" banner shows, a dim layer covers the WebUI view
  (`dimView.ts`): its own small view with the WebUI's bounds, right above
  it, with a static data page (JavaScript off) that washes the page with
  the launcher's background colour. The banner and the bar are outside
  those bounds and stay clickable. The page underneath stays loaded and is
  not reloaded; it cannot be clicked while the server is stopped. The
  keyboard goes to the shell. Not while starting.
- The dot is a small view of its own, always on top. Its page is a static
  data URL with JavaScript off and a `default-src 'none'` policy. A click
  follows a link to a `.invalid` host; the main process cancels that
  navigation in `will-navigate` and shows the status bar.

Z-order limits: while the shell is raised, the WebUI cannot be clicked. This
is intended, because the popover is modal. Native dialogs (the
permission prompt, the save dialog) are separate windows and are not
affected.

### Contract for the shell renderer

- Read the state with `getWebui()` and `onWebui()` (`WebuiViewState` in
  `shared/types.ts`). `home` is what the window shows when no shell screen is
  open; `layout` also counts the open screen. In `webui` layout, draw the bar
  at the bottom (`STATUS_BAR_HEIGHT` in `shared/layout.ts`) and the strips,
  and keep the page background transparent everywhere else.
- Call `setShellScreen('logs' | 'settings' | 'connect' | null)` when a shell
  screen opens or closes over the WebUI. Call `setOverlay(true | false)` when
  a popover of the bar opens or closes. Send `setInsets({ top, bottom })`
  when a strip appears, disappears or changes height (`bottom` excludes the
  bar).
- `banner` is `stopped` or `starting` while the local server is not ready and
  the page is kept. The Start button is in the "stopped" banner. A failed
  local server makes `home` `shell-only`: the shell shows the start-failed
  screen, and the page comes back, not reloaded, when the server is ready.
- `target.kind === 'remote'`: show "Remote: host". The way back is the server
  switcher (`setDefaultServer('local')`). `connectRemote(id?)` opens a saved
  server; the renderer never sends an origin to open, only a saved id or, for
  a test, a typed address. `remote` is set while the remote default does not
  answer: the shell shows the recovery screen.
- `problem` (crashed, unresponsive, load-failed): offer `reloadWebui()`.
  `placement` is `browser` (the setting) or `browser-fallback` (two crashes
  in this session; `reloadWebui()` tries the window again).
- `openWebuiInBrowser()` opens the page that the view shows in the system
  browser. `resetWebuiPermissions()` forgets the remembered microphone and
  camera choices. The main process draws the dot; the shell does not.

### Main-process channels for the shell screens

Each one is behind the sender check and the input checks, like every other
channel (`src/shared/ipc.ts` has the full list):

| Channel | What it does |
|---|---|
| `launcher:window:get`, `:set-open-in-browser` | "Open in browser" setting; the remembered microphone and camera choice of the local server |
| `launcher:updates:get`, `:set-auto-check` | last server update check, auto-check setting, version of the rollback copy |
| `launcher:updates:release-notes` | opens the GitHub release page of a version, only when that version is on offer |
| `launcher:install:details` | platform, size of the installed binary, install folder as a display string |
| `launcher:updater:install` | installs a downloaded launcher update, or downloads an available one |
| `launcher:relaunch` | restarts the launcher (an AppImage from its file) |
| `launcher:port:find-free` | first port from the given one upward that binds on loopback, at most 100 tries |
| `launcher:port:process` | best effort: names and pids of the processes that listen on a port (`ss` on Linux, `lsof` on Linux and macOS, as fixed argv arrays), or `hidden`, `none`, `unavailable` |
| `launcher:settings:pick-folder` | native folder picker for the models or backends folder; the main process checks the folder (absolute, normalized, exists or can be created, not `/`, not the home folder), saves it and answers `saved` (with `restartRequired`), `refused` (with the reason) or `cancelled`. The renderer never passes a path, and `launcher:settings:save` refuses `modelsPath` and `backendsPath` |
| `launcher:hardware` | CPU model and count, memory, a GPU hint from one file check; no commands, no network |
| `launcher:webui:set-insets` (send) | heights of the shell's strips around the WebUI view |

### Security policy

The view renders server pages, model output and MCP app frames, so it gets
the strictest settings (`webuiView.ts`; the decisions are in
`webuiPolicy.ts`):

- No preload. `nodeIntegration` off (also in subframes and workers);
  `contextIsolation`, `sandbox` and `webSecurity` on; no insecure content; no
  `<webview>`. The process-wide `--no-sandbox` fallback of the AppImage start
  script is outside the view's control; nothing turns the sandbox off for the
  view.
- A persistent session partition per origin (`persist:webui-<hash>`). The
  local server, each remote server and the shell never share cookies or
  storage.
- Navigation of every frame stays on the chosen origin: scheme, host and port
  equal after normalisation. A URL with user info is refused, so
  `http://host@other` never matches. Other `http`, `https` and `mailto` links
  of the main frame open in the system browser. All other schemes are
  refused. Subframes can load the server origin, `about:srcdoc`, `about:blank`
  and the server's own `blob:` URLs, nothing else, and they never open the
  browser. The session also cancels every document request to another origin,
  also after a redirect.
- `window.open` and `target=_blank` never create a window. `/app` pages of
  the server open in the view; other server pages and other origins open in
  the browser.
- Permissions are refused, with two exceptions for the server origin:
  microphone and camera (a prompt; the answer is remembered per origin if
  the user wants) and clipboard write. Both only within 5 seconds of a click
  or a key press in the view, and only when the top page is the server too.
  Permission checks pass only for a remembered allow and for clipboard write.
  Device permissions (USB, HID, serial) are refused.
- Downloads always show the native save dialog, in the Downloads folder,
  with a cleaned name: no folders, no control or direction-override
  characters, no leading dots, no reserved names. A download from a page
  that is not the server is cancelled.
- Certificate errors are never bypassed. No client certificate is sent.
- The view is never a trusted IPC sender: `ipcGuard.isTrustedSender` accepts
  only the shell view's main frame (see `test/webuiSecurity.test.ts`).

A remote server may use `http` (owner decision 2026-11-15, works out of the
box): the form warns, and the bar keeps an amber "Remote, not encrypted" chip
for as long as that server is shown. Credentials in an address and other
schemes are refused. Each origin keeps its own session partition.

Known limits: a sign-in that redirects to another origin (an identity
provider) opens in the system browser, so it cannot complete inside the view;
use Open in browser for it. This is by decision: the view never shows a page
of an origin the user did not choose. A `javascript:` link runs inside the page itself
(same origin, no new rights), as in any browser.

### Desktop notifications

The launcher shows a native notification for each new item of a server's
notification inbox, such as a finished trigger run. Settings, General,
"Desktop notifications" turns it off (key `desktop_notifications`, missing
means on).

- One stream per server: `GET /api/chats/notifications/events` (server-sent
  events) of the local server while it runs, and of every saved remote
  server. A stream ends when its server is removed, stops answering or the
  setting is off; it reconnects after 1 s, doubling to 60 s (with jitter),
  and every 5 minutes after a 401, 403 or 404. The stream is live only: items
  that came while the launcher was closed are not shown.
- Sign-in: the local server gets `Authorization: Bearer <API key>`. A remote
  server gets the cookies of its WebUI session, so it works after the user
  has signed in to that server in the window.
- The title carries the server name when more than one server is configured.
  A notification is not shown when the window has the focus and already shows
  the page of the item's `link`.
- Clicking one focuses the window and opens the item's `link`. The link must
  be an in-app path (`/...`); a full URL, `//host`, a backslash or `..` is
  refused. A page of the server on show opens in the window; a page of any
  other server opens in the system browser.
- The unread count (`GET /api/chats/notifications/count`, read again on each
  `notifications_changed` event and on each reconnect) is the app badge
  (`app.setBadgeCount`, where the system supports it) and the tray tooltip.

`test/notifications.test.ts` covers the parser, reconnect, link check,
suppression and the setting, against `test/fixtures/fake-server.cjs`, which
serves the inbox endpoints and two test hooks (`POST /__emit`, `GET /__stats`).

### Tests

`test/webuiPolicy.test.ts`, `test/webuiState.test.ts`,
`test/windowLayout.test.ts` and `test/webuiSecurity.test.ts` (the Electron
wiring against a fake Electron, and the real trust check against WebUI
frames) run with `npm test`. `scripts/webui-security.sh` runs the built app:

```sh
npm run build
SEC_DIR=/short/scratch/dir scripts/webui-security.sh --no-sandbox
```

Results of a run on Linux (Electron 45.3.2, Xvfb, `--no-sandbox`):

| Attempt by the page | Result |
|---|---|
| `window.launcher`, `require`, `process`, `ipcRenderer`, `module` | all undefined |
| `window.open` to another origin | returns null; the URL goes to the browser |
| `window.open('javascript:...')` | returns null; refused |
| `target=_blank` link to another origin | the page stays; the URL goes to the browser |
| `location = 'file:///etc/passwd'`, a `data:` page | the page stays (Chromium refuses before the launcher sees it) |
| custom scheme navigation | the page stays; refused by the launcher |
| link to another origin | the page stays; the URL goes to the browser |
| redirect to another origin | the page stays; stopped in `will-redirect`, the URL goes to the browser |
| iframe to another origin, `data:` iframe | refused; the frame stays `about:blank`, its script never runs |
| `fetch('file:///etc/passwd')` | refused |
| microphone without a click | refused |
| microphone after a click | the prompt is shown |
| clipboard write after a click | allowed |
| clipboard read, notifications, geolocation, screen capture, MIDI, USB | refused |
| download named `../../.ssh/authorized_keys`, blob download named `../../.bashrc` | save dialog with a cleaned name; nothing saved |
| a request that reaches the other origin | none |
| switch to a shell screen and back | the page is kept, not reloaded |
| WebUI viewport | window content height minus 30 with the bar, full height without |
| corner dot | shown while the bar is hidden; a click shows the bar again |
| second origin (Connect) | its own view and session: no local storage of the first origin |
