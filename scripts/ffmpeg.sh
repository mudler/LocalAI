#!/usr/bin/env bash
#-------------------------------------------------------------------------------------------------------------
# Syntax: ./ffmpeg.sh [upgrade packages flag]

set -e

FFMPEG=${1:-"false"}

# Ensure apt is in non-interactive to avoid prompts
export DEBIAN_FRONTEND=noninteractive

# Install ffmpeg
if [ "${FFMPEG}" = "true" ] && ! dpkg -s ffmpeg > /dev/null 2>&1; then
    if [ ! -d "/var/lib/apt/lists" ] || [ "$(ls /var/lib/apt/lists/ | wc -l)" = "0" ]; then
        apt-get update
    fi
    apt-get -y install --no-install-recommends ffmpeg
fi

echo "Done!"