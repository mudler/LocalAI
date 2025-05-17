#!/bin/bash

## Patches
## Apply patches from the `patches` directory
for patch in $(ls patches); do
    echo "Applying patch $patch"
    patch -d llama.cpp/ -p1 < patches/$patch
done 

set -e

cp -r CMakeLists.txt llama.cpp/tools/grpc-server/
cp -r grpc-server.cpp llama.cpp/tools/grpc-server/
cp -rfv llama.cpp/common/json.hpp llama.cpp/tools/grpc-server/
cp -rfv llama.cpp/tools/server/utils.hpp llama.cpp/tools/grpc-server/
cp -rfv llama.cpp/tools/server/httplib.h llama.cpp/tools/grpc-server/

set +e
if grep -q "grpc-server" llama.cpp/tools/CMakeLists.txt; then
    echo "grpc-server already added"
else
    echo "add_subdirectory(grpc-server)" >> llama.cpp/tools/CMakeLists.txt
fi
set -e

# Now to keep maximum compatibility with the original server.cpp, we need to remove the index.html.gz.hpp and loading.html.hpp includes
# and remove the main function
# TODO: upstream this to the original server.cpp by extracting the upstream main function to a separate file
awk '
/int[ \t]+main[ \t]*\(/ {          # If the line starts the main function
    in_main=1;                     # Set a flag
    open_braces=0;                 # Track number of open braces
}
in_main {
    open_braces += gsub(/\{/, "{"); # Count opening braces
    open_braces -= gsub(/\}/, "}"); # Count closing braces
    if (open_braces == 0) {         # If all braces are closed
        in_main=0;                  # End skipping
    }
    next;                           # Skip lines inside main
}
!in_main                           # Print lines not inside main
' "llama.cpp/tools/server/server.cpp" > llama.cpp/tools/grpc-server/server.cpp

# remove index.html.gz.hpp and loading.html.hpp includes
if [[ "$OSTYPE" == "darwin"* ]]; then
    # macOS
    sed -i '' '/#include "index\.html\.gz\.hpp"/d; /#include "loading\.html\.hpp"/d' llama.cpp/tools/grpc-server/server.cpp
else
    # Linux and others
    sed -i '/#include "index\.html\.gz\.hpp"/d; /#include "loading\.html\.hpp"/d' llama.cpp/tools/grpc-server/server.cpp
fi