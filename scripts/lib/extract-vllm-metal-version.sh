#!/bin/bash
set -euo pipefail

grep -m1 -oE '^[[:space:]]*(local[[:space:]]+)?(vllm_v|VLLM_VERSION)[[:space:]]*=[[:space:]]*"[0-9]+\.[0-9]+\.[0-9]+"[[:space:]]*(#.*)?$' \
    | grep -oE '[0-9]+\.[0-9]+\.[0-9]+'
