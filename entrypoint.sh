#!/bin/bash
set -e

cd /build

if [ "$REBUILD" != "false" ]; then
	rm -rf ./local-ai
	ESPEAK_DATA=/build/lib/Linux-$(uname -m)/piper_phonemize/lib/espeak-ng-data make build
fi

./local-ai "$@"