#!/usr/bin/env bash
# Prints the notes of a release: the entry of CHANGELOG.md for the version,
# without its heading and its link definitions. Links to files of the
# repository become links to those files at the tag, so that they work on
# the release page. It fails when CHANGELOG.md has no entry for the version.
#
# usage: release-notes.sh <version> [repository URL]
#   .github/scripts/release-notes.sh v1.1.0 > notes.md
set -euo pipefail

version=${1:?usage: release-notes.sh <version> [repository URL]}
repo=${2:-https://github.com/agenticode/my}
number=${version#v}
changelog=$(git rev-parse --show-toplevel)/CHANGELOG.md

entry=$(awk -v head="## [$number]" '
  index($0, head) == 1 { found = 1; next }
  found && /^## / { exit }
  found && /^\[[^]]+\]: / { next }
  found { print }
  END { if (!found) exit 1 }
' "$changelog") || { echo "release-notes.sh: CHANGELOG.md has no entry \"## [$number]\"" >&2; exit 1; }

# A relative link: a directory (ending in /) goes to tree/, a file to blob/.
printf '%s\n' "$entry" |
  sed -E \
    -e "s|\]\(([^):/#][^):]*/)\)|](${repo}/tree/${version}/\1)|g" \
    -e "s|\]\(([^):/#][^):]*)\)|](${repo}/blob/${version}/\1)|g" |
  awk 'NF { started = 1 } started { lines[++n] = $0 } END { while (n > 0 && lines[n] == "") n--; for (i = 1; i <= n; i++) print lines[i] }'
