#!/bin/bash
set -e

cd /

# Start as root only to fix the ownership of the data folders, then run
# LocalAI as an unprivileged user. The Sandbox add-on refuses to run as root,
# and a process that does not need root should not have it. A volume that an
# older image created as root is handed over on the first start. Set
# LOCALAI_RUN_AS_ROOT=true to keep the old behaviour (the sandbox is then
# unavailable). LOCALAI_UID and LOCALAI_GID change the user. Without them,
# when /data and /models are owned by the same user that is not root (bind
# mounts of folders of the host user), LocalAI runs as that user and their
# owner is left as it is. A container that is already started as another user
# (docker --user, Kubernetes runAsUser) is left alone.
if [ "$(id -u)" = "0" ] && [ "${LOCALAI_RUN_AS_ROOT:-false}" != "true" ]; then
	run_uid="${LOCALAI_UID:-10001}"
	run_gid="${LOCALAI_GID:-10001}"
	if [ -z "${LOCALAI_UID:-}" ] && [ -z "${LOCALAI_GID:-}" ]; then
		# LOCALAI_OWNER_DIRS exists for the entrypoint test.
		owner_uid=""
		owner_gid=""
		# shellcheck disable=SC2086
		for dir in ${LOCALAI_OWNER_DIRS:-/data /models}; do
			if [ ! -d "$dir" ]; then
				owner_uid=""
				break
			fi
			uid="$(stat -c %u "$dir")"
			if [ -z "$owner_uid" ]; then
				owner_uid="$uid"
				owner_gid="$(stat -c %g "$dir")"
			elif [ "$owner_uid" != "$uid" ]; then
				owner_uid=""
				break
			fi
		done
		if [ -n "$owner_uid" ] && [ "$owner_uid" != "0" ] && [ "$owner_uid" != "$run_uid" ]; then
			echo "The data folders are owned by uid $owner_uid; running LocalAI as uid $owner_uid and gid $owner_gid without changing their owner (set LOCALAI_UID and LOCALAI_GID to choose another user)"
			run_uid="$owner_uid"
			run_gid="$owner_gid"
		fi
	fi
	# Python and other tools look the user up by uid, and some fail without a
	# passwd entry. Add one for a user that the image does not know.
	# LOCALAI_PASSWD_FILE and LOCALAI_GROUP_FILE exist for the entrypoint test.
	passwd_file="${LOCALAI_PASSWD_FILE:-/etc/passwd}"
	group_file="${LOCALAI_GROUP_FILE:-/etc/group}"
	if ! cut -d: -f3 "$passwd_file" 2>/dev/null | grep -qx "$run_uid"; then
		echo "localai-$run_uid:x:$run_uid:$run_gid:LocalAI:${LOCALAI_HOME_DIR:-${HOME:-/data/home}}:/usr/sbin/nologin" >>"$passwd_file" ||
			echo "warning: could not add uid $run_uid to $passwd_file"
	fi
	if ! cut -d: -f3 "$group_file" 2>/dev/null | grep -qx "$run_gid"; then
		echo "localai-$run_gid:x:$run_gid:" >>"$group_file" ||
			echo "warning: could not add gid $run_gid to $group_file"
	fi
	# LOCALAI_DATA_DIRS and LOCALAI_DEVICE_PATHS exist for the entrypoint test.
	# shellcheck disable=SC2086
	for dir in ${LOCALAI_DATA_DIRS:-/models /backends /configuration /data}; do
		[ -d "$dir" ] || continue
		if [ "$(stat -c %u "$dir")" != "$run_uid" ]; then
			echo "Handing $dir over to uid $run_uid (first start with the unprivileged user)"
			chown -R "$run_uid:$run_gid" "$dir" || echo "warning: could not change the owner of $dir; LocalAI may not be able to write there"
		fi
	done
	# The groups that own the devices the user needs (/dev/kvm for the sandbox,
	# the render nodes of the GPUs). Group 0 is never added.
	# The Docker socket is there for the agent worker and MCP servers that
	# start containers. Its group is joined only when the operator mounts the
	# socket, and access to it is equal to root on the host: mount it only
	# into a container that needs it.
	groups=""
	# shellcheck disable=SC2086
	for dev in ${LOCALAI_DEVICE_PATHS:-/dev/kvm /dev/kfd /dev/dri/* /var/run/docker.sock}; do
		[ -e "$dev" ] || continue
		gid="$(stat -c %g "$dev")"
		case ",$groups," in *",$gid,"*) continue ;; esac
		[ "$gid" = "0" ] || groups="${groups:+$groups,}$gid"
	done
	# The home of the user keeps caches that LocalAI and the backends write
	# (Python, torch and compiler caches). The image sets HOME=/data/home so
	# they live in the data volume and survive a recreated container.
	# LOCALAI_HOME_DIR exists for the entrypoint test.
	export HOME="${LOCALAI_HOME_DIR:-${HOME:-/data/home}}"
	mkdir -p "$HOME" || echo "warning: could not create the home folder $HOME"
	if [ -d "$HOME" ] && [ "$(stat -c %u "$HOME")" != "$run_uid" ]; then
		chown -R "$run_uid:$run_gid" "$HOME" || echo "warning: could not change the owner of $HOME; LocalAI may not be able to keep its caches there"
	fi
	if [ -n "$groups" ]; then
		exec setpriv --reuid="$run_uid" --regid="$run_gid" --groups="$groups" -- "$0" "$@"
	fi
	exec setpriv --reuid="$run_uid" --regid="$run_gid" --clear-groups -- "$0" "$@"
fi

# A container started as another user (docker --user, Kubernetes runAsUser)
# may get a HOME that it cannot write, such as / for a user ID with no passwd
# entry. Python and the backends keep caches there, so use the data volume, or
# the temp folder when the data volume is not writable either.
# LOCALAI_HOME_FALLBACK exists for the entrypoint test.
home_usable() { [ -n "$1" ] && mkdir -p "$1" 2>/dev/null && [ -w "$1" ]; }
if ! home_usable "${HOME:-}"; then
	for candidate in "${LOCALAI_HOME_FALLBACK:-/data/home}" "${TMPDIR:-/tmp}/localai-home-$(id -u)"; do
		if home_usable "$candidate"; then
			echo "HOME (${HOME:-unset}) is not writable; using $candidate"
			export HOME="$candidate"
			break
		fi
	done
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
