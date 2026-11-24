#!/bin/bash
# Checks how entrypoint.sh drops root. It runs the script with fake id, stat,
# chown and setpriv commands that record what they were asked, so no root and
# no container is needed.
#
#   1. As root it hands a folder that another user owns over to the LocalAI
#      user, and leaves a folder that the user already owns alone.
#   2. It then re-runs itself through setpriv as that user, with the groups of
#      the device files and never group 0.
#   3. LOCALAI_RUN_AS_ROOT=true keeps root: setpriv is not called.
#   4. A process that is not root is left alone: setpriv is not called.
set -euo pipefail

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
REPO_ROOT=$(dirname "$(dirname "$(dirname "$(realpath "$0")")")")

mkdir -p "$WORK/bin" "$WORK/owned" "$WORK/foreign" "$WORK/dev"
touch "$WORK/dev/kvm" "$WORK/dev/render" "$WORK/dev/rootdev"

cat >"$WORK/bin/id" <<'SH'
#!/bin/sh
echo "${FAKE_UID:-0}"
SH
# stat -c %u <dir> and stat -c %g <dev>, from a table in the file name.
cat >"$WORK/bin/stat" <<'SH'
#!/bin/sh
case "$3" in
*/owned) echo 10001 ;;
*/foreign) echo 0 ;;
*/dev/kvm) echo 108 ;;
*/dev/render) echo 108 ;;
*/dev/rootdev) echo 0 ;;
*) echo 0 ;;
esac
SH
cat >"$WORK/bin/chown" <<'SH'
#!/bin/sh
echo "chown $*" >>"$FAKE_LOG"
SH
cat >"$WORK/bin/setpriv" <<'SH'
#!/bin/sh
echo "setpriv $*" >>"$FAKE_LOG"
exit 0
SH
chmod +x "$WORK/bin/"*

fail() { echo "FAIL: $*" >&2; cat "$WORK/log" >&2 2>/dev/null || true; exit 1; }

run() { # run <uid> [env...]
	local uid=$1
	shift
	: >"$WORK/log"
	# setpriv is faked and exits, so the script never reaches ./local-ai.
	env "$@" FAKE_UID="$uid" FAKE_LOG="$WORK/log" PATH="$WORK/bin:$PATH" \
		LOCALAI_DATA_DIRS="$WORK/owned $WORK/foreign" \
		LOCALAI_DEVICE_PATHS="$WORK/dev/kvm $WORK/dev/render $WORK/dev/rootdev $WORK/dev/missing" \
		bash "$REPO_ROOT/entrypoint.sh" run >"$WORK/out" 2>&1 || true
}

run 0
grep -q "chown -R 10001:10001 $WORK/foreign" "$WORK/log" || fail "a root-owned folder was not handed over"
grep -q "chown .*$WORK/owned" "$WORK/log" && fail "a folder the user owns was changed"
grep -q -- "setpriv --reuid=10001 --regid=10001 --groups=108 -- " "$WORK/log" || fail "setpriv did not get the device group once, without group 0"

run 0 LOCALAI_UID=2000 LOCALAI_GID=3000
grep -q -- "setpriv --reuid=2000 --regid=3000" "$WORK/log" || fail "LOCALAI_UID and LOCALAI_GID were ignored"

run 0 LOCALAI_RUN_AS_ROOT=true
grep -q setpriv "$WORK/log" && fail "LOCALAI_RUN_AS_ROOT=true still dropped root"

run 10001
grep -q setpriv "$WORK/log" && fail "a process that is not root was re-run"

echo "entrypoint privilege tests passed"
