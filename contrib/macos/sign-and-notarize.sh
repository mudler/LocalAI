#!/usr/bin/env bash
# Code-sign and notarize macOS artifacts for LocalAI.
# Every sub-command is a no-op (exit 0) when its required secret is unset,
# so unsigned builds (forks, local dev, PRs) keep working.
set -euo pipefail

ENTITLEMENTS="contrib/macos/Launcher.entitlements"
KEYCHAIN="localai-ci.keychain-db"

cmd_import_cert() {
  if [ -z "${MACOS_CERTIFICATE:-}" ]; then
    echo "[sign] MACOS_CERTIFICATE unset: skipping cert import (unsigned build)"
    return 0
  fi
  local certfile keychain_pwd default_keychain
  certfile="$(mktemp).p12"
  keychain_pwd="${MACOS_CI_KEYCHAIN_PWD:?MACOS_CI_KEYCHAIN_PWD required when signing}"
  echo "$MACOS_CERTIFICATE" | base64 --decode > "$certfile"
  security create-keychain -p "$keychain_pwd" "$KEYCHAIN"
  security set-keychain-settings -lut 21600 "$KEYCHAIN"
  security unlock-keychain -p "$keychain_pwd" "$KEYCHAIN"
  security import "$certfile" -k "$KEYCHAIN" -P "${MACOS_CERTIFICATE_PWD:?}" \
    -T /usr/bin/codesign -T /usr/bin/security
  security set-key-partition-list -S apple-tool:,apple:,codesign: \
    -s -k "$keychain_pwd" "$KEYCHAIN" >/dev/null
  default_keychain="$(security default-keychain | tr -d ' "')"
  security list-keychains -d user -s "$KEYCHAIN" "$default_keychain"
  rm -f "$certfile"
  echo "[sign] certificate imported into $KEYCHAIN"
}

cmd_sign() {
  local target="$1"
  if [ -z "${MACOS_SIGN_IDENTITY:-}" ]; then
    echo "[sign] MACOS_SIGN_IDENTITY unset: skipping codesign of $target"
    return 0
  fi
  case "$target" in
    *.app)
      # Hardened runtime + entitlements are required for notarizing the app bundle.
      codesign --deep --force --options runtime --timestamp \
        --entitlements "$ENTITLEMENTS" \
        --sign "$MACOS_SIGN_IDENTITY" "$target"
      ;;
    *)
      # A disk image carries no entitlements/runtime; just sign the container.
      codesign --force --timestamp --sign "$MACOS_SIGN_IDENTITY" "$target"
      ;;
  esac
  codesign --verify --strict --verbose=2 "$target"
  echo "[sign] signed $target"
}

cmd_notarize() {
  local dmg="$1"
  if [ -z "${MACOS_NOTARY_KEY:-}" ]; then
    echo "[notarize] MACOS_NOTARY_KEY unset: skipping notarization of $dmg"
    return 0
  fi
  local keyfile
  keyfile="$(mktemp).p8"
  echo "$MACOS_NOTARY_KEY" | base64 --decode > "$keyfile"
  xcrun notarytool submit "$dmg" \
    --key "$keyfile" \
    --key-id "${MACOS_NOTARY_KEY_ID:?}" \
    --issuer "${MACOS_NOTARY_ISSUER_ID:?}" \
    --wait
  rm -f "$keyfile"
  xcrun stapler staple "$dmg"
  xcrun stapler validate "$dmg"
  echo "[notarize] notarized and stapled $dmg"
}

# Notarize and staple the .app bundle itself. Stapling the dmg alone is not
# enough: an app with no embedded ticket has no local proof of notarization, so
# Gatekeeper falls back to an online check — and the app then fails to launch on
# a machine that is offline / behind a firewall, or once it has been copied out
# of the dmg. Stapling the bundle makes it verify offline. notarytool needs an
# archive for a bundle, so we zip it first.
cmd_notarize_app() {
  local app="$1"
  if [ -z "${MACOS_NOTARY_KEY:-}" ]; then
    echo "[notarize] MACOS_NOTARY_KEY unset: skipping notarization of $app"
    return 0
  fi
  local keyfile zip
  keyfile="$(mktemp).p8"
  zip="$(mktemp).zip"
  echo "$MACOS_NOTARY_KEY" | base64 --decode > "$keyfile"
  ditto -c -k --keepParent "$app" "$zip"
  xcrun notarytool submit "$zip" \
    --key "$keyfile" \
    --key-id "${MACOS_NOTARY_KEY_ID:?}" \
    --issuer "${MACOS_NOTARY_ISSUER_ID:?}" \
    --wait
  rm -f "$keyfile" "$zip"
  xcrun stapler staple "$app"
  xcrun stapler validate "$app"
  echo "[notarize] notarized and stapled $app"
}

main() {
  local sub="${1:-}"; shift || true
  case "$sub" in
    import-cert)  cmd_import_cert ;;
    sign)         cmd_sign "$@" ;;
    notarize)     cmd_notarize "$@" ;;
    notarize-app) cmd_notarize_app "$@" ;;
    *) echo "usage: $0 {import-cert|sign <path>|notarize <dmg>|notarize-app <app>}" >&2; exit 2 ;;
  esac
}

main "$@"
