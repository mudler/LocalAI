#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath $0)")
mkdir -p $CURDIR/package/lib

# Copy the binary
cp -avf $CURDIR/target/release/kokoros-grpc $CURDIR/package/

# Copy the run script
cp -rfv $CURDIR/run.sh $CURDIR/package/
chmod +x $CURDIR/package/run.sh

# Copy ONNX Runtime shared libraries from ort build artifacts
ORT_LIB_DIR=$(find $CURDIR/target/release/build -name "libonnxruntime*.so*" -path "*/ort-sys-*/out/*" -exec dirname {} \; 2>/dev/null | head -1)
if [ -n "$ORT_LIB_DIR" ]; then
    cp -avfL $ORT_LIB_DIR/libonnxruntime*.so* $CURDIR/package/lib/ 2>/dev/null || true
fi

# Copy espeak-ng data
if [ -d "/usr/share/espeak-ng-data" ]; then
    cp -rf /usr/share/espeak-ng-data $CURDIR/package/
elif [ -d "/usr/lib/x86_64-linux-gnu/espeak-ng-data" ]; then
    cp -rf /usr/lib/x86_64-linux-gnu/espeak-ng-data $CURDIR/package/
fi

# Copy ALL dynamic library dependencies of the binary
echo "Bundling dynamic library dependencies..."
ldd $CURDIR/target/release/kokoros-grpc | grep "=>" | awk '{print $3}' | while read lib; do
    if [ -n "$lib" ] && [ -f "$lib" ]; then
        cp -avfL "$lib" $CURDIR/package/lib/ 2>/dev/null || true
    fi
done

# Copy CA certificates for HTTPS (needed for model auto-download)
if [ -d "/etc/ssl/certs" ]; then
    mkdir -p $CURDIR/package/etc/ssl
    cp -rf /etc/ssl/certs $CURDIR/package/etc/ssl/
fi

# Copy the dynamic linker
if [ -f "/lib64/ld-linux-x86-64.so.2" ]; then
    cp -arfLv /lib64/ld-linux-x86-64.so.2 $CURDIR/package/lib/ld.so
elif [ -f "/lib/ld-linux-aarch64.so.1" ]; then
    cp -arfLv /lib/ld-linux-aarch64.so.1 $CURDIR/package/lib/ld.so
fi

echo "Packaging completed successfully"
ls -liah $CURDIR/package/
ls -liah $CURDIR/package/lib/
