#!/bin/bash
set -e

source $(dirname $0)/../common/libbackend.sh

runUnittests
