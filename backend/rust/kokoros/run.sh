#!/bin/bash
set -ex

CURDIR=$(dirname "$(realpath $0)")

export LD_LIBRARY_PATH=$CURDIR/lib:${LD_LIBRARY_PATH:-}

# SSL certificates for model auto-download
if [ -d "$CURDIR/etc/ssl/certs" ]; then
    export SSL_CERT_DIR=$CURDIR/etc/ssl/certs
fi

# espeak-ng data directory
if [ -d "$CURDIR/espeak-ng-data" ]; then
    export ESPEAK_NG_DATA=$CURDIR/espeak-ng-data
fi

# Use bundled ld.so if present (portability)
if [ -f $CURDIR/lib/ld.so ]; then
    exec $CURDIR/lib/ld.so $CURDIR/kokoros-grpc "$@"
fi

exec $CURDIR/kokoros-grpc "$@"
