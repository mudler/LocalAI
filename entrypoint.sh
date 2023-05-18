#!/bin/bash

cd /build

if [ "$REBUILD" != "false" ]; then
	make rebuild
fi

./local-ai "$@"