#!/bin/bash

cd /build

make build

./local-ai "$@"