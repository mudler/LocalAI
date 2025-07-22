#!/bin/bash
set -ex

CURDIR=$(dirname "$(realpath $0)")

exec $CURDIR/local-store "$@"