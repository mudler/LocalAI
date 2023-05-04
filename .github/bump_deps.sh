#!/bin/bash
set -xe
REPO=$1
BRANCH=$2
VAR=$3

LAST_COMMIT=$(curl -s -H "Accept: application/vnd.github.VERSION.sha" "https://api.github.com/repos/$REPO/commits/$BRANCH")

sed -i Makefile -e "s/$VAR?=.*/$VAR?=$LAST_COMMIT/"
