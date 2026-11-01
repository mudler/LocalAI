#!/bin/bash
set -e

cd /

# Start as root only to fix the ownership of the data folders, then run
# LocalAI as an unprivileged user. The Sandbox add-on refuses to run as root,
# and a process that does not need root should not have it. A volume that an
# older image created as root is handed over on the first start. Set
# LOCALAI_RUN_AS_ROOT=true to keep the old behaviour (the sandbox is then
# unavailable). LOCALAI_UID and LOCALAI_GID change the user. A container that
# is already started as another user (docker --user, Kubernetes runAsUser) is
# left alone.
if [ "$(id -u)" = "0" ] && [ "${LOCALAI_RUN_AS_ROOT:-false}" != "true" ]; then
	run_uid="${LOCALAI_UID:-10001}"
	run_gid="${LOCALAI_GID:-10001}"
	# LOCALAI_DATA_DIRS and LOCALAI_DEVICE_PATHS exist for the entrypoint test.
	# shellcheck disable=SC2086
	for dir in ${LOCALAI_DATA_DIRS:-/models /backends /configuration /data /tmp/generated}; do
		[ -d "$dir" ] || continue
		if [ "$(stat -c %u "$dir")" != "$run_uid" ]; then
			echo "Handing $dir over to uid $run_uid (first start with the unprivileged user)"
			chown -R "$run_uid:$run_gid" "$dir" || echo "warning: could not change the owner of $dir; LocalAI may not be able to write there"
		fi
	done
	# The groups that own the devices the user needs (/dev/kvm for the sandbox,
	# the render nodes of the GPUs). Group 0 is never added.
	groups=""
	# shellcheck disable=SC2086
	for dev in ${LOCALAI_DEVICE_PATHS:-/dev/kvm /dev/kfd /dev/dri/*}; do
		[ -e "$dev" ] || continue
		gid="$(stat -c %g "$dev")"
		case ",$groups," in *",$gid,"*) continue ;; esac
		[ "$gid" = "0" ] || groups="${groups:+$groups,}$gid"
	done
	export HOME=/home/localai
	if [ -n "$groups" ]; then
		exec setpriv --reuid="$run_uid" --regid="$run_gid" --groups="$groups" -- "$0" "$@"
	fi
	exec setpriv --reuid="$run_uid" --regid="$run_gid" --clear-groups -- "$0" "$@"
fi

# If we have set EXTRA_BACKENDS, then we need to prepare the backends
if [ -n "$EXTRA_BACKENDS" ]; then
	echo "EXTRA_BACKENDS: $EXTRA_BACKENDS"
	# Space separated list of backends
	for backend in $EXTRA_BACKENDS; do
		echo "Preparing backend: $backend"
		make -C $backend
	done
fi

echo "CPU info:"
grep -e "model\sname" /proc/cpuinfo | head -1
grep -e "flags" /proc/cpuinfo | head -1
if grep -q -e "\savx\s" /proc/cpuinfo ; then
	echo "CPU:    AVX    found OK"
else
	echo "CPU: no AVX    found"
fi
if grep -q -e "\savx2\s" /proc/cpuinfo ; then
	echo "CPU:    AVX2   found OK"
else
	echo "CPU: no AVX2   found"
fi
if grep -q -e "\savx512" /proc/cpuinfo ; then
	echo "CPU:    AVX512 found OK"
else
	echo "CPU: no AVX512 found"
fi

exec ./local-ai "$@"
