#!/usr/bin/env bash
# Builds the release archives of a version and does with them what a user
# does: check the checksums, unpack an archive, run the binary. It fails
# when an archive holds something else than the binary, the license and the
# README, when a linux binary is not statically linked, or when the binary
# does not name the version it was built as.
#
# The release workflow runs it for the tag; CI runs it for every change, so
# that a release does not meet a broken build for the first time on the day
# of the release.
#
# usage: check-dist.sh <version>
set -euo pipefail

version=${1:?usage: check-dist.sh <version>}
make dist VERSION="$version"
cd dist

if command -v sha256sum > /dev/null; then
  sha256sum -c SHA256SUMS
else
  shasum -a 256 -c SHA256SUMS
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

count=0
for platform in linux_amd64 linux_arm64 darwin_arm64; do
  name="my_${version}_${platform}"
  [ -f "$name.tar.gz" ] || { echo "FAILED: $name.tar.gz is missing"; exit 1; }
  grep -q "  $name.tar.gz\$" SHA256SUMS || { echo "FAILED: SHA256SUMS does not list $name.tar.gz"; exit 1; }
  want=$(printf '%s\n' "$name/" "$name/LICENSE" "$name/README.md" "$name/my")
  have=$(tar -tzf "$name.tar.gz" | LC_ALL=C sort)
  if [ "$have" != "$want" ]; then
    echo "FAILED: $name.tar.gz holds:"
    echo "$have"
    exit 1
  fi
  tar -xzf "$name.tar.gz" -C "$tmp"
  [ -x "$tmp/$name/my" ] || { echo "FAILED: $name/my is not executable"; exit 1; }
  case "$platform" in
    linux_*)
      file "$tmp/$name/my"
      file "$tmp/$name/my" | grep -q 'statically linked' || { echo "FAILED: $name/my is not statically linked"; exit 1; }
      ;;
  esac
  count=$((count + 1))
done
archives=(my_"${version}"_*.tar.gz)
[ "${#archives[@]}" -eq "$count" ] || { echo "FAILED: ${#archives[@]} archives of $version, a release has $count"; exit 1; }

# The binary of this machine runs and says what it is.
case "$(uname -s)/$(uname -m)" in
  Linux/x86_64) native=linux_amd64 ;;
  Linux/aarch64) native=linux_arm64 ;;
  Darwin/arm64) native=darwin_arm64 ;;
  *) native= ;;
esac
if [ -n "$native" ]; then
  line=$("$tmp/my_${version}_${native}/my" version)
  echo "$line"
  case "$line" in
    "my $version ("*) ;;
    *) echo "FAILED: the binary does not name version $version"; exit 1 ;;
  esac
  "$tmp/my_${version}_${native}/my" version --json
fi
echo "release archives ok"
