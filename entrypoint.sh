#!/bin/bash
set -e

cd /

# Ensure persistent directories exist (Docker VOLUME mounts shadow the image,
# so on first run these may be empty; we need the directories at minimum).
for dir in /models /backends /data /configuration; do
	mkdir -p "$dir"
done

# Show config persistence status so operators can verify their volume mounts.
CONFIG_DIR="${LOCALAI_CONFIG_DIR:-/configuration}"
SETTINGS_FILE="$CONFIG_DIR/runtime_settings.json"

echo "-----------------------------------------------------------------"
echo "LocalAI configuration directory: $CONFIG_DIR"
if [ -f "$SETTINGS_FILE" ]; then
	echo "[OK]  runtime_settings.json found — config is persisted on a volume"
else
	echo "[WARN] runtime_settings.json not found — config will be created now"
	echo "       mount a persistent volume at $CONFIG_DIR to keep settings"
	echo "       across Docker image updates and container restarts."
fi
echo "-----------------------------------------------------------------"

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
