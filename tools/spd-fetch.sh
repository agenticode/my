#!/usr/bin/env bash
# Downloads public DDR5 SPD dumps that may not be copied into this
# repository (iron2love/DDR5_SPD_Data states no licence) into a directory
# outside it, and checks the SHA-256 of each file.
#
#   tools/spd-fetch.sh <directory>
#   MY_SPD_DIR=<directory> go test ./internal/decode/spd -run TestFetchedDumps
#
# It needs curl, tar and sha256sum or shasum. It writes only below
# <directory>.
set -eu -o pipefail

if [ $# -ne 1 ]; then
	echo "usage: tools/spd-fetch.sh <directory>" >&2
	exit 64
fi
out=$1
repo=iron2love/DDR5_SPD_Data
commit=9213707909b3b583db55c2bb9bbc1e02193286ae

root=$(cd "$(dirname "$0")/.." && pwd)
made=0
[ -d "$out" ] || made=1
mkdir -p "$out"
case "$(cd "$out" && pwd)/" in
"$root/"*)
	[ "$made" = 0 ] || rmdir "$out"
	echo "spd-fetch: $out lies inside the repository; choose a directory outside it" >&2
	exit 64
	;;
esac

if command -v sha256sum >/dev/null 2>&1; then
	sum() { sha256sum "$1" | cut -d' ' -f1; }
else
	sum() { shasum -a 256 "$1" | cut -d' ' -f1; }
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
curl -sSfL --max-time 120 -o "$work/src.tgz" "https://codeload.github.com/$repo/tar.gz/$commit"
tar -xzf "$work/src.tgz" -C "$work"
src="$work/DDR5_SPD_Data-$commit/SPD_Data"

bad=0
while IFS='|' read -r want name; do
	[ -n "$name" ] || continue
	if [ ! -f "$src/$name" ]; then
		echo "missing: $name" >&2
		bad=1
		continue
	fi
	got=$(sum "$src/$name")
	if [ "$got" != "$want" ]; then
		echo "SHA-256 differs: $name ($got)" >&2
		bad=1
		continue
	fi
	cp "$src/$name" "$out/$name"
	echo "ok $name"
done <<'LIST'
87f2965787b53d5b3fdc936ebc972077e22fc8dd4b9a307cbf0495b3594f9b91|CL38-48-48 D5-8000(GeIL_64000-CL38-48-48-100 1.45V).bin
a82beaba457318645620d5f1a469d98f88cebc8a884c50809c152ef64e84c3cf|CMK32GX5M2B5600Z40(VENG_5600-40-40-40-77 1.25V).bin
e6a1e5727be59f21d886fd268ccb6a5346bf9633fa77a11e2724e26944678375|CT16G56C46U5.M8G1(Crucial_5600-CL46 1.1V).bin
4c86e20c548f2aca52ee3939e2396fc2a70c2d3646ff09ecde511f000f32651c|F5-6000J3636F16G(Z5 GSKill_6000-CL36-36-36-96 1.35V).bin
1b6cec0e8d8b6fe9aab200329ea1527332eadd316ca024fc44afa3209a26908e|KD58GU880-56G4600(KLEVV_5600-CL46-46-46 1.1V).bin
2ba0367e11f36081cd023c94bcb949352dda68b675981360fa3cb17789fd0a38|M323R1GB4PB0-CWMOL.bin
a31fe8a5f6ec0aeac4aff50fb2f084eea8ae74a1af7de6077281f9fc8ec4b571|M323R2GA3DB0-CWMOD.bin
9f5035bf6c3e99a981172488693e0699ade8c77397d89fc17e80e4b3577c8999|UD5-6000(T.FORCE_6000-CL38-38-38-78 1.25V).bin
4e96b446879301748d7751a6ac06905708fda1ef305284a395bac684a1ff2064|UD5-6000(T.FORCE_8000-CL38-48-48-84 1.45V).bin
LIST
exit $bad
