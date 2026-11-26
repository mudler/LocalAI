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
#   5. The home folder goes to a LOCALAI_UID that is not 10001, so LocalAI can
#      keep its caches there (the Sigstore trust of signed add-ons).
#   6. As root a missing home folder is made and handed to the user.
#   7. Not as root, a HOME that cannot be written is replaced by the data
#      volume folder, and a writable HOME is kept.
#   8. With no LOCALAI_UID, data folders that one user who is not root owns
#      (bind mounts of the host user) make LocalAI run as that user, and they
#      keep their owner. Root-owned or mixed folders keep the default user.
#   9. A user that the image does not know gets a passwd and a group entry.
#  10. A mounted Docker socket adds its group, and only when it is mounted.
set -euo pipefail

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
REPO_ROOT=$(dirname "$(dirname "$(dirname "$(realpath "$0")")")")

mkdir -p "$WORK/bin" "$WORK/owned" "$WORK/foreign" "$WORK/dev" "$WORK/home" "$WORK/ro" "$WORK/datahome" \
	"$WORK/hostdata" "$WORK/hostmodels" "$WORK/rootdir"
touch "$WORK/dev/kvm" "$WORK/dev/render" "$WORK/dev/rootdev" "$WORK/dev/docker.sock"

cat >"$WORK/bin/id" <<'SH'
#!/bin/sh
echo "${FAKE_UID:-0}"
SH
# stat -c %u|%g <path>, from a table in the file name: the same id for both.
cat >"$WORK/bin/stat" <<'SH'
#!/bin/sh
case "$3" in
*/owned) echo 10001 ;;
*/foreign) echo 0 ;;
*/dev/kvm) echo 108 ;;
*/dev/render) echo 108 ;;
*/dev/rootdev) echo 0 ;;
*/dev/docker.sock) echo 999 ;;
*/home) echo 10001 ;;
*/newhome) echo 0 ;;
*/hostdata) echo 1000 ;;
*/hostmodels) echo 1000 ;;
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
	printf 'root:x:0:0::/root:/bin/bash\nlocalai:x:10001:10001::/data/home:/usr/sbin/nologin\n' >"$WORK/passwd"
	printf 'root:x:0:\nlocalai:x:10001:\n' >"$WORK/group"
	# setpriv is faked and exits, so the script never reaches ./local-ai.
	# Not as root, the script reaches ./local-ai, which is missing, and stops.
	env LOCALAI_HOME_DIR="$WORK/home" LOCALAI_DATA_DIRS="$WORK/owned $WORK/foreign" \
		LOCALAI_OWNER_DIRS="$WORK/owned $WORK/foreign" \
		LOCALAI_DEVICE_PATHS="$WORK/dev/kvm $WORK/dev/render $WORK/dev/rootdev $WORK/dev/missing" \
		LOCALAI_PASSWD_FILE="$WORK/passwd" LOCALAI_GROUP_FILE="$WORK/group" \
		"$@" FAKE_UID="$uid" FAKE_LOG="$WORK/log" PATH="$WORK/bin:$PATH" \
		LOCALAI_HOME_FALLBACK="$WORK/datahome" \
		bash "$REPO_ROOT/entrypoint.sh" run >"$WORK/out" 2>&1 || true
}

run 0
grep -q "chown -R 10001:10001 $WORK/foreign" "$WORK/log" || fail "a root-owned folder was not handed over"
grep -q "chown .*$WORK/owned" "$WORK/log" && fail "a folder the user owns was changed"
grep -q -- "setpriv --reuid=10001 --regid=10001 --groups=108 -- " "$WORK/log" || fail "setpriv did not get the device group once, without group 0"
grep -q "chown .*$WORK/home" "$WORK/log" && fail "the home of the default user was changed"
grep -q "localai-" "$WORK/passwd" "$WORK/group" && fail "an entry was added for the default user"

run 0 LOCALAI_UID=2000 LOCALAI_GID=3000
grep -q -- "setpriv --reuid=2000 --regid=3000" "$WORK/log" || fail "LOCALAI_UID and LOCALAI_GID were ignored"
grep -q "chown -R 2000:3000 $WORK/home" "$WORK/log" || fail "the home folder was not handed to LOCALAI_UID"
grep -q "^localai-2000:x:2000:3000:" "$WORK/passwd" || fail "LOCALAI_UID got no passwd entry"
grep -q "^localai-3000:x:3000:" "$WORK/group" || fail "LOCALAI_GID got no group entry"

host="$WORK/hostdata $WORK/hostmodels"
run 0 LOCALAI_OWNER_DIRS="$host" LOCALAI_DATA_DIRS="$host $WORK/foreign"
grep -q -- "setpriv --reuid=1000 --regid=1000" "$WORK/log" || fail "folders of one host user did not make LocalAI run as that user"
grep -q "chown .*$WORK/host" "$WORK/log" && fail "folders of the host user were changed"
grep -q "chown -R 1000:1000 $WORK/foreign" "$WORK/log" || fail "a root-owned image volume was not handed to the host user"
grep -q "running LocalAI as uid 1000" "$WORK/out" || fail "the host user match was not logged"
grep -q "^localai-1000:x:1000:1000:" "$WORK/passwd" || fail "the host user got no passwd entry"

run 0 LOCALAI_OWNER_DIRS="$WORK/hostdata $WORK/foreign" LOCALAI_DATA_DIRS="$host"
grep -q -- "setpriv --reuid=10001 --regid=10001" "$WORK/log" || fail "mixed owners did not keep the default user"
grep -q "chown -R 10001:10001 $WORK/hostdata" "$WORK/log" || fail "mixed owners did not hand the folders to the default user"

run 0 LOCALAI_OWNER_DIRS="$WORK/foreign $WORK/rootdir"
grep -q -- "setpriv --reuid=10001 --regid=10001" "$WORK/log" || fail "root-owned folders did not keep the default user"

run 0 LOCALAI_OWNER_DIRS="$host" LOCALAI_UID=2000 LOCALAI_GID=2000
grep -q -- "setpriv --reuid=2000 --regid=2000" "$WORK/log" || fail "LOCALAI_UID did not win over the host user"

run 0 LOCALAI_DEVICE_PATHS="$WORK/dev/kvm $WORK/dev/docker.sock"
grep -q -- "setpriv --reuid=10001 --regid=10001 --groups=108,999 -- " "$WORK/log" || fail "a mounted Docker socket did not add its group"

run 0 LOCALAI_DEVICE_PATHS="$WORK/dev/kvm $WORK/dev/no-docker.sock"
grep -q -- "--groups=108 -- " "$WORK/log" || fail "a missing Docker socket changed the groups"

run 0 LOCALAI_RUN_AS_ROOT=true
grep -q setpriv "$WORK/log" && fail "LOCALAI_RUN_AS_ROOT=true still dropped root"

run 10001
grep -q setpriv "$WORK/log" && fail "a process that is not root was re-run"

run 0 LOCALAI_HOME_DIR="$WORK/newhome"
[ -d "$WORK/newhome" ] || fail "a missing home folder was not made"
grep -q "chown -R 10001:10001 $WORK/newhome" "$WORK/log" || fail "a new home folder was not handed to the user"

if [ "$(command id -u)" != "0" ]; then
	chmod 500 "$WORK/ro"
	run 1234 HOME="$WORK/ro/home"
	grep -q "using $WORK/datahome" "$WORK/out" || fail "an unwritable HOME was not replaced by the data folder"
	chmod 700 "$WORK/ro"

	run 1234 HOME=
	grep -q "using $WORK/datahome" "$WORK/out" || fail "an empty HOME was not replaced by the data folder"

	run 1234 HOME="$WORK/home"
	grep -q "is not writable" "$WORK/out" && fail "a writable HOME was replaced"
fi

echo "entrypoint privilege tests passed"
