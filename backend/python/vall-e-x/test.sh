#!/bin/bash
set -e
TEST_FILE="./source/test.py"

source $(dirname $0)/../common/libbackend.sh

runUnittests
