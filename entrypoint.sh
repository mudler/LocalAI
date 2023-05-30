#!/bin/bash
set -e

cd /build

if [ "$REBUILD" != "false" ]; then
	rm -rf ./local-ai
	make build
fi

./local-ai "$@"