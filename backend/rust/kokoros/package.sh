#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath $0)")
mkdir -p $CURDIR/package/lib

# Copy the binary and run script
cp -avf $CURDIR/target/release/kokoros-grpc $CURDIR/package/
cp -rfv $CURDIR/run.sh $CURDIR/package/
chmod +x $CURDIR/package/run.sh

# Copy espeak-ng data
if [ -d "/usr/share/espeak-ng-data" ]; then
    cp -rf /usr/share/espeak-ng-data $CURDIR/package/
elif [ -d "/usr/lib/x86_64-linux-gnu/espeak-ng-data" ]; then
    cp -rf /usr/lib/x86_64-linux-gnu/espeak-ng-data $CURDIR/package/
fi

# Bundle all dynamic library dependencies
echo "Bundling dynamic library dependencies..."
ldd $CURDIR/target/release/kokoros-grpc | grep "=>" | awk '{print $3}' | while read lib; do
    if [ -n "$lib" ] && [ -f "$lib" ]; then
        cp -avfL "$lib" $CURDIR/package/lib/
    fi
done

# Copy CA certificates for HTTPS (needed for model auto-download)
if [ -d "/etc/ssl/certs" ]; then
    mkdir -p $CURDIR/package/etc/ssl
    cp -rf /etc/ssl/certs $CURDIR/package/etc/ssl/
fi

# Copy the dynamic linker
source "$CURDIR/../../../scripts/build/package-system-libs.sh" "$CURDIR/package/lib" ""

echo "Packaging completed successfully"
ls -liah $CURDIR/package/
ls -liah $CURDIR/package/lib/
