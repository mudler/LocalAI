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

main() {
  local sub="${1:-}"; shift || true
  case "$sub" in
    import-cert) cmd_import_cert ;;
    sign)        cmd_sign "$@" ;;
    notarize)    cmd_notarize "$@" ;;
    *) echo "usage: $0 {import-cert|sign <path>|notarize <dmg>}" >&2; exit 2 ;;
  esac
}

main "$@"
