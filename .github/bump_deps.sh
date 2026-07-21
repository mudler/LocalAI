#!/bin/bash
set -xe
REPO=$1
BRANCH=$2
VAR=$3
FILE=$4

if [ -z "$FILE" ]; then
    FILE="Makefile"
fi

# -L so a renamed/transferred upstream repo (GitHub answers 301) still
# resolves instead of handing us the redirect body, and -f so an HTTP error
# aborts the run rather than letting an error page reach sed below.
LAST_COMMIT=$(curl -sfL -H "Accept: application/vnd.github.VERSION.sha" "https://api.github.com/repos/$REPO/commits/$BRANCH")

# Guard the sed input: anything that is not a bare 40-hex SHA (an API error
# body, an empty response) would otherwise be spliced into the Makefile pin —
# either corrupting it silently or blowing up sed with an unterminated
# expression, which is how this job failed for a renamed repo.
if ! [[ "$LAST_COMMIT" =~ ^[0-9a-f]{40}$ ]]; then
    echo "Refusing to bump $VAR: expected a 40-char commit SHA for $REPO@$BRANCH, got: $LAST_COMMIT" >&2
    exit 1
fi

# Read $VAR from Makefile (only first match)
set +e
CURRENT_COMMIT="$(grep -m1 "^$VAR?=" $FILE | cut -d'=' -f2)"
set -e

sed -i $FILE -e "s/$VAR?=.*/$VAR?=$LAST_COMMIT/"

if [ -z "$CURRENT_COMMIT" ]; then
    echo "Could not find $VAR in Makefile."
    exit 0
fi

echo "Changes: https://github.com/$REPO/compare/${CURRENT_COMMIT}..${LAST_COMMIT}" >> "${VAR}_message.txt"
echo "${LAST_COMMIT}" >> "${VAR}_commit.txt"