#!/bin/bash

# Builds locally from sources the packages needed by the llama cpp backend.

# Makes sure a few base packages exist.
# sudo apt-get --no-upgrade -y install g++ gcc binutils cmake git build-essential autoconf libtool pkg-config 

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
echo "Script directory: $SCRIPT_DIR"

CPP_INSTALLED_PACKAGES_DIR=$1
if [ -z ${CPP_INSTALLED_PACKAGES_DIR} ]; then 
    echo "CPP_INSTALLED_PACKAGES_DIR env variable not set. Don't know where to install: failed."; 
    echo
    exit -1
fi

if [ -d "${CPP_INSTALLED_PACKAGES_DIR}" ]; then
  echo "gRPC installation directory already exists. Nothing to do."
  exit 0
fi

# The depth when cloning a git repo. 1 speeds up the clone when the repo history is not needed.
GIT_CLONE_DEPTH=1

NUM_BUILD_THREADS=$(nproc --ignore=1)

# Google gRPC --------------------------------------------------------------------------------------
TAG_LIB_GRPC="v1.59.0"
GIT_REPO_LIB_GRPC="https://github.com/grpc/grpc.git"
GRPC_REPO_DIR="${SCRIPT_DIR}/../grpc_repo"
GRPC_BUILD_DIR="${SCRIPT_DIR}/../grpc_build"
SRC_DIR_LIB_GRPC="${GRPC_REPO_DIR}/grpc"

echo "SRC_DIR_LIB_GRPC: ${SRC_DIR_LIB_GRPC}"
echo "GRPC_REPO_DIR: ${GRPC_REPO_DIR}"
echo "GRPC_BUILD_DIR: ${GRPC_BUILD_DIR}"

mkdir -pv ${GRPC_REPO_DIR}

rm   -rf ${GRPC_BUILD_DIR}
mkdir -pv ${GRPC_BUILD_DIR}

mkdir -pv ${CPP_INSTALLED_PACKAGES_DIR}
	
if [ -d "${SRC_DIR_LIB_GRPC}" ]; then
  echo "gRPC source already exists locally. Not cloned again."
else  
  ( cd ${GRPC_REPO_DIR} && \
    git clone --depth ${GIT_CLONE_DEPTH} -b ${TAG_LIB_GRPC} ${GIT_REPO_LIB_GRPC} && \
    cd ${SRC_DIR_LIB_GRPC} && \
    git submodule update --init --recursive --depth ${GIT_CLONE_DEPTH} 
  )    
fi

( cd ${GRPC_BUILD_DIR} && \
  cmake -G "Unix Makefiles" \
     -DCMAKE_BUILD_TYPE=Release \
     -DgRPC_INSTALL=ON \
     -DEXECUTABLE_OUTPUT_PATH=${CPP_INSTALLED_PACKAGES_DIR}/grpc/bin \
     -DLIBRARY_OUTPUT_PATH=${CPP_INSTALLED_PACKAGES_DIR}/grpc/lib \
     -DgRPC_BUILD_TESTS=OFF \
     -DgRPC_BUILD_CSHARP_EXT=OFF \
     -DgRPC_BUILD_GRPC_CPP_PLUGIN=ON \
     -DgRPC_BUILD_GRPC_CSHARP_PLUGIN=OFF \
     -DgRPC_BUILD_GRPC_NODE_PLUGIN=OFF \
     -DgRPC_BUILD_GRPC_OBJECTIVE_C_PLUGIN=OFF \
     -DgRPC_BUILD_GRPC_PHP_PLUGIN=OFF \
     -DgRPC_BUILD_GRPC_PYTHON_PLUGIN=ON \
     -DgRPC_BUILD_GRPC_RUBY_PLUGIN=OFF \
     -Dprotobuf_WITH_ZLIB=ON \
     -DRE2_BUILD_TESTING=OFF \
     -DCMAKE_INSTALL_PREFIX=${CPP_INSTALLED_PACKAGES_DIR}/ \
     ${SRC_DIR_LIB_GRPC}  && \
  cmake --build .  -- -j ${NUM_BUILD_THREADS} && \
  cmake --build .  --target install -- -j ${NUM_BUILD_THREADS} 
)

rm -rf ${GRPC_BUILD_DIR}
rm -rf ${GRPC_REPO_DIR}

