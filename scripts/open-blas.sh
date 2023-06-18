#!/usr/bin/env bash
#-------------------------------------------------------------------------------------------------------------
# Syntax: ./open-blash.sh

set -e

# Ensure apt is in non-interactive to avoid prompts
export DEBIAN_FRONTEND=noninteractive

# Install software-properties-common
if ! dpkg -s libopenblas-dev > /dev/null 2>&1; then
    if [ ! -d "/var/lib/apt/lists" ] || [ "$(ls /var/lib/apt/lists/ | wc -l)" = "0" ]; then
        apt-get update
    fi
    apt-get -y install --no-install-recommends libopenblas-dev
fi

echo "Done!"