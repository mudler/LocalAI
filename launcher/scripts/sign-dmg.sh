#!/usr/bin/env bash
# Signs, notarizes and staples a macOS DMG, then refreshes the DMG entry of the
# update feed. Used by launcher-electron-build.yaml after electron-builder.
#
# electron-builder already signs, notarizes and staples the app inside the DMG.
# It does not notarize the DMG file itself, and its own DMG signing (dmg.sign)
# adds no secure timestamp, which Apple needs. So the workflow does it here,
# with the same steps that the earlier launcher used: a Developer ID signature
# with a secure timestamp, notarytool with an API key, and stapler.
#
# Usage: sign-dmg.sh <path/to/LocalAI.dmg> [path/to/latest-mac.yml]
#
# Environment (all required, values are never printed):
#   MACOS_CERTIFICATE, MACOS_CERTIFICATE_PWD   base64 .p12 and its password
#   MACOS_NOTARY_KEY                           base64 App Store Connect API key (.p8)
#   MACOS_NOTARY_KEY_ID, MACOS_NOTARY_ISSUER_ID
#   RUNNER_TEMP                                optional, folder for temporary files
set -euo pipefail

dmg="${1:?usage: sign-dmg.sh <dmg> [latest-mac.yml]}"
feed="${2:-}"

[ -f "$dmg" ] || { echo "::error::DMG not found: $dmg"; exit 1; }
missing=""
for v in MACOS_CERTIFICATE MACOS_CERTIFICATE_PWD MACOS_NOTARY_KEY MACOS_NOTARY_KEY_ID MACOS_NOTARY_ISSUER_ID; do
  [ -n "${!v:-}" ] || missing="$missing $v"
done
if [ -n "$missing" ]; then
  # Only the names are printed.
  echo "::error::Cannot sign the DMG, these variables are empty:$missing"
  exit 1
fi

umask 077
work=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/dmg-sign.XXXXXX")
keychain="$work/dmg-sign.keychain-db"
keyfile="$work/notary-key.p8"
cleanup() {
  security delete-keychain "$keychain" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

# 1. Import the certificate into a keychain of its own.
kc_pwd=$(openssl rand -hex 24)
echo "$MACOS_CERTIFICATE" | base64 --decode > "$work/cert.p12"
security create-keychain -p "$kc_pwd" "$keychain"
security set-keychain-settings -lut 21600 "$keychain"
security unlock-keychain -p "$kc_pwd" "$keychain"
security import "$work/cert.p12" -k "$keychain" -P "$MACOS_CERTIFICATE_PWD" \
  -T /usr/bin/codesign -T /usr/bin/security
security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$kc_pwd" "$keychain" >/dev/null
rm -f "$work/cert.p12"
default_keychain=$(security default-keychain | tr -d ' "')
security list-keychains -d user -s "$keychain" "$default_keychain"

identity=$(security find-identity -v -p codesigning "$keychain" | awk '/Developer ID Application/ {print $2; exit}')
[ -n "$identity" ] || { echo "::error::No Developer ID Application identity in the certificate"; exit 1; }

# 2. Sign the container. A disk image has no entitlements and no hardened
# runtime; it needs the identity and a secure timestamp.
codesign --force --timestamp --sign "$identity" --keychain "$keychain" "$dmg"
codesign --verify --strict --verbose=2 "$dmg"

# 3. Notarize. --wait blocks until Apple has the result.
echo "$MACOS_NOTARY_KEY" | base64 --decode > "$keyfile"
notary_args=(--key "$keyfile" --key-id "$MACOS_NOTARY_KEY_ID" --issuer "$MACOS_NOTARY_ISSUER_ID")
rc=0
out=$(xcrun notarytool submit "$dmg" "${notary_args[@]}" --wait --timeout 45m --output-format json) || rc=$?
echo "$out"
id=$(echo "$out" | jq -r '.id // empty' 2>/dev/null || true)
status=$(echo "$out" | jq -r '.status // empty' 2>/dev/null || true)
if [ "$status" != "Accepted" ]; then
  echo "::error::Notarization of the DMG ended with status '${status:-unknown}' (notarytool exit code $rc)"
  if [ -n "$id" ]; then
    xcrun notarytool log "$id" "${notary_args[@]}" || true
  fi
  exit 1
fi

# 4. Staple the ticket. This changes the file, so every checksum, size and
# feed entry of the DMG must be made after this point.
xcrun stapler staple "$dmg"
xcrun stapler validate "$dmg"

# 5. The feed names the DMG with a hash and a size from before signing. The
# updater applies the zip, but the entry must not be wrong. Refresh it.
if [ -n "$feed" ]; then
  name=$(basename "$dmg")
  sha=$(openssl dgst -sha512 -binary "$dmg" | base64 | tr -d '\n')
  size=$(wc -c < "$dmg" | tr -d ' ')
  path=$(sed -n 's/^path: *//p' "$feed" | head -n 1)
  awk -v name="$name" -v sha="$sha" -v size="$size" -v top="$path" '
    /^ *- url:/ { cur = $0; sub(/^ *- url: */, "", cur) }
    /^ +sha512:/ && cur == name { sub(/sha512:.*/, "sha512: " sha) }
    /^ +size:/   && cur == name { sub(/size:.*/, "size: " size) }
    /^sha512:/ && top == name   { $0 = "sha512: " sha }
    { print }
  ' "$feed" > "$work/feed.yml"
  cat "$work/feed.yml" > "$feed"
  echo "feed refreshed for $name (size $size)"
fi
echo "DMG signed, notarized and stapled: $dmg"
