#!/bin/bash
set -xe
REPO=$1
BRANCH=$2
VAR=$3

LAST_COMMIT=$(curl -s -H "Accept: application/vnd.github.VERSION.sha" "https://api.github.com/repos/$REPO/commits/$BRANCH")

# Read $VAR from Makefile (only first match)
set +e
CURRENT_COMMIT="$(grep -m1 "^$VAR?=" Makefile | cut -d'=' -f2)"
set -e

sed -i Makefile -e "s/$VAR?=.*/$VAR?=$LAST_COMMIT/"

if [ -z "$CURRENT_COMMIT" ]; then
    echo "Could not find $VAR in Makefile."
    exit 0
fi

echo "Changes: https://github.com/$REPO/compare/${CURRENT_COMMIT}..${LAST_COMMIT}" >> "${VAR}_message.txt"
echo "${LAST_COMMIT}" >> "${VAR}_commit.txt"