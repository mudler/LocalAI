#!/bin/sh
# Makes the golden images in this folder. The tests read them without
# e2fsprogs, so the core paths run on any machine.
#
# Usage: testdata/gen.sh SCRATCH_DIR
#
# It needs mke2fs, debugfs, e2fsck and gzip from e2fsprogs. It writes
# golden-*.img.gz here. The images are not bit-for-bit reproducible (file
# owners and a few times depend on the machine), and the tests do not depend
# on those fields. Each compressed image must stay under 100 KB.
#
# The tests expect this tree (see golden_test.go):
#   /a.txt "hello\n"        /hard (hard link of a.txt)    /link -> a.txt
#   /dir/b.txt "bee\n"      /dir/sub/c.txt (100 bytes)    /dir/longlink -> 70 x
#   /big (40000 bytes)      /sparse (20000 bytes, holes)  /empty/ (a directory)
#   /<255 n's>              /w (whiteout, char 0:0)       /opaque/ (opaque dir)
#   a.txt has user.x and security.capability, opaque/ has trusted.overlay.opaque
# golden-htree has /many with 600 entries, indexed by e2fsck -D.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
work=${1:?usage: gen.sh SCRATCH_DIR}
mkdir -p "$work"
export PATH="$PATH:/usr/sbin:/sbin"
export E2FSPROGS_FAKE_TIME=1700000000

tree() { # tree DIR
	d=$1
	rm -rf "$d"
	mkdir -p "$d/dir/sub" "$d/empty" "$d/opaque"
	printf 'hello\n' >"$d/a.txt"
	printf 'bee\n' >"$d/dir/b.txt"
	i=0
	: >"$d/dir/sub/c.txt"
	while [ $i -lt 10 ]; do printf '0123456789' >>"$d/dir/sub/c.txt"; i=$((i + 1)); done
	yes abcdefghi | head -c 40000 >"$d/big"
	truncate -s 20000 "$d/sparse"
	printf 'A' | dd of="$d/sparse" bs=1 seek=0 conv=notrunc 2>/dev/null
	printf 'Z' | dd of="$d/sparse" bs=1 seek=15000 conv=notrunc 2>/dev/null
	ln -s a.txt "$d/link"
	ln -s "$(printf 'x%.0s' $(seq 70))" "$d/dir/longlink"
	ln "$d/a.txt" "$d/hard"
	printf 'long\n' >"$d/$(printf 'n%.0s' $(seq 255))"
	find "$d" -exec touch -h -d @1700000000 {} +
}

script() { # script FILE: the commands that mke2fs -d cannot do
	printf '\001\000\000\002\377\377\377\377\000\000\000\000\000\000\000\000\000\000\000\000' >"$work/cap"
	cat >"$1" <<EOS
mknod w c 0 0
ea_set /opaque trusted.overlay.opaque y
ea_set /a.txt user.x hello
ea_set -f $work/cap /a.txt security.capability
EOS
}

make() { # make NAME ARGS...
	name=$1
	shift
	img="$work/$name.img"
	rm -f "$img"
	truncate -s 1M "$img"
	tree "$work/src"
	mke2fs -q -F -U 11111111-2222-3333-4444-555555555555 -E hash_seed=11111111-2222-3333-4444-555555555555 "$@" -d "$work/src" "$img"
	script "$work/script"
	debugfs -w -f "$work/script" "$img" >/dev/null 2>&1
}

make golden-ext4 -t ext4 -O ^has_journal -b 4096 -N 64
make golden-ext2-1k -t ext2 -b 1024 -N 64
make golden-inline -t ext4 -O ^has_journal,inline_data -b 1024 -N 64

# A directory of 600 entries, as an htree.
img="$work/golden-htree.img"
rm -f "$img"
truncate -s 2M "$img"
rm -rf "$work/src"
mkdir -p "$work/src/many"
i=0
while [ $i -lt 600 ]; do
	: >"$work/src/many/entry-$(printf '%04d' $i)-padding-name"
	i=$((i + 1))
done
mke2fs -q -F -U 11111111-2222-3333-4444-555555555555 -E hash_seed=11111111-2222-3333-4444-555555555555 -t ext4 -O ^has_journal,dir_index,^metadata_csum -b 1024 -N 700 -d "$work/src" "$img"
e2fsck -fyD "$img" >/dev/null 2>&1 || true
debugfs -R "htree_dump /many" "$img" 2>/dev/null | grep -q 'Number of entries' || { echo "the directory is not indexed" >&2; exit 1; }

for f in golden-ext4 golden-ext2-1k golden-inline golden-htree; do
	gzip -9 -n -c "$work/$f.img" >"$here/$f.img.gz"
	size=$(wc -c <"$here/$f.img.gz")
	[ "$size" -lt 100000 ] || { echo "$f is $size bytes compressed" >&2; exit 1; }
	echo "$f.img.gz: $size bytes"
done
