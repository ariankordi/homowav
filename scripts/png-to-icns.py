#!/usr/bin/env python3
"""Wraps one square PNG in a macOS .icns file, using only the standard library.

macOS 10.7 and later read PNG data inside .icns entries, so no pixel
conversion is needed: the file is the 'icns' header plus one entry whose type
code says the PNG's size. Apple's iconutil does the same job but only exists
on macOS, and the release build runs on Linux too.

The format (big-endian throughout): the magic 'icns', the total file length
as u32, then entries of a 4-character type, the entry length including its
8-byte header as u32, and the data.

Usage: png-to-icns.py <input.png> <output.icns>
"""

import struct
import sys

# The 8-byte signature every PNG starts with.
PNG_SIGNATURE = b"\x89PNG\r\n\x1a\n"
# Offset of the width and height (two u32) inside the IHDR chunk, which
# always comes first: signature (8) + chunk length (4) + chunk type (4).
IHDR_SIZE_OFFSET = 16
# icns entry types that hold PNG data, by pixel side.
ENTRY_TYPE_BY_SIDE = {
    16: b"icp4",
    32: b"icp5",
    64: b"icp6",
    128: b"ic07",
    256: b"ic08",
    512: b"ic09",
    1024: b"ic10",
}
# Bytes of the file header, and of each entry's header.
HEADER_SIZE = 8


def png_side(data: bytes) -> int:
    """Returns the side of a square PNG, or raises ValueError."""
    if not data.startswith(PNG_SIGNATURE) or len(data) < IHDR_SIZE_OFFSET + 8:
        raise ValueError("not a PNG file")
    width, height = struct.unpack_from(">II", data, IHDR_SIZE_OFFSET)
    if width != height:
        raise ValueError(f"the icon must be square, not {width}x{height}")
    return width


def make_icns(png: bytes) -> bytes:
    """Builds a one-entry .icns file around png."""
    side = png_side(png)
    entry_type = ENTRY_TYPE_BY_SIDE.get(side)
    if entry_type is None:
        sizes = ", ".join(str(known) for known in ENTRY_TYPE_BY_SIDE)
        raise ValueError(f"no icns entry for {side} px; use one of {sizes}")
    entry = entry_type + struct.pack(">I", HEADER_SIZE + len(png)) + png
    return b"icns" + struct.pack(">I", HEADER_SIZE + len(entry)) + entry


def main() -> int:
    if len(sys.argv) != 3:
        print(__doc__.strip().splitlines()[-1], file=sys.stderr)
        return 1
    input_path, output_path = sys.argv[1], sys.argv[2]
    with open(input_path, "rb") as source:
        png = source.read()
    try:
        icns = make_icns(png)
    except ValueError as error:
        print(f"{input_path}: {error}", file=sys.stderr)
        return 1
    with open(output_path, "wb") as target:
        target.write(icns)
    return 0


if __name__ == "__main__":
    sys.exit(main())
