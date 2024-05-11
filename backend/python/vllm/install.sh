#!/bin/bash
set -e

EXTRA_PIP_INSTALL_FLAGS="--no-build-isolation"

source $(dirname $0)/../common/libbackend.sh

installRequirements
