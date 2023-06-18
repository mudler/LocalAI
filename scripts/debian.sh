#!/usr/bin/env bash
#-------------------------------------------------------------------------------------------------------------
# Syntax: ./debian.sh [upgrade packages flag]

set -e

UPGRADE_PACKAGES=${1:-"true"}

# Ensure apt is in non-interactive to avoid prompts
export DEBIAN_FRONTEND=noninteractive

# Function to call apt-get if needed
apt_get_update_if_needed()
{
    if [ ! -d "/var/lib/apt/lists" ] || [ "$(ls /var/lib/apt/lists/ | wc -l)" = "0" ]; then
        echo "Running apt-get update..."
        apt-get update
    else
        echo "Skipping apt-get update."
    fi
}

# Get to latest versions of all packages
if [ "${UPGRADE_PACKAGES}" = "true" ]; then
    apt_get_update_if_needed
    apt-get -y upgrade --no-install-recommends
    apt-get autoremove -y
fi

# Run install apt-utils to avoid debconf warning then verify presence of other common developer tools and dependencies
if [ "${PACKAGES_ALREADY_INSTALLED}" != "true" ]; then
    apt_get_update_if_needed

    package_list="apt-utils \
        ca-certificates \
        cmake \
        patch \
        curl"

    echo "Packages to verify are installed: ${package_list}"
    apt-get -y install --no-install-recommends ${package_list} 2> >( grep -v 'debconf: delaying package configuration, since apt-utils is not installed' >&2 )

    PACKAGES_ALREADY_INSTALLED="true"
fi

echo "Done!"