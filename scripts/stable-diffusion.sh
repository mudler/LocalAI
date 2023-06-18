#!/usr/bin/env bash
#-------------------------------------------------------------------------------------------------------------
# Syntax: ./stable-diffusion.sh

set -e

if ! dpkg -s libopencv-dev > /dev/null 2>&1; then
    if [ ! -d "/var/lib/apt/lists" ] || [ "$(ls /var/lib/apt/lists/ | wc -l)" = "0" ]; then
        apt-get update
    fi
    apt-get -y install --no-install-recommends libopencv-dev
    ln -s /usr/include/opencv4/opencv2 /usr/include/opencv2
fi

echo "Done!"