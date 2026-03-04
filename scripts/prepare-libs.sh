#!/bin/bash

mkdir -p backend-assets/lib

OS="$(uname)"

if [ "$OS" == "Darwin" ]; then
    LIBS="$(otool -L $1 | awk 'NR > 1 { system("echo " $1) } ' | xargs echo)"
elif [ "$OS" == "Linux" ]; then
    LIBS="$(ldd $1 | awk 'NF == 4 { system("echo " $3) } ' | xargs echo)"
else
    echo "Unsupported OS"
    exit 1
fi

for lib in $LIBS; do
  cp -f $lib backend-assets/lib
done

echo "==============================="
echo "Copied libraries to backend-assets/lib"
echo "$LIBS"
echo "==============================="