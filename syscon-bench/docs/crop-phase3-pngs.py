#!/usr/bin/env python3
"""Crop a PNG to its non-transparent (or non-page-bg) content bbox."""
from __future__ import annotations

import struct
import sys
import zlib
from pathlib import Path


def decode_png(data: bytes):
    assert data[:8] == b"\x89PNG\r\n\x1a\n"
    pos = 8
    idat = b""
    w = h = color = None
    while pos < len(data):
        length = struct.unpack(">I", data[pos : pos + 4])[0]
        ctype = data[pos + 4 : pos + 8]
        cdata = data[pos + 8 : pos + 8 + length]
        pos += 12 + length
        if ctype == b"IHDR":
            w, h, _bit, color, *_rest = struct.unpack(">IIBBBBB", cdata)
        elif ctype == b"IDAT":
            idat += cdata
        elif ctype == b"IEND":
            break
    raw = zlib.decompress(idat)
    bpp = {2: 3, 6: 4}[color]
    stride = w * bpp
    rows = []
    i = 0
    prev = bytearray(stride)
    for _y in range(h):
        f = raw[i]
        i += 1
        row = bytearray(raw[i : i + stride])
        i += stride
        if f == 1:
            for x in range(bpp, stride):
                row[x] = (row[x] + row[x - bpp]) & 255
        elif f == 2:
            for x in range(stride):
                row[x] = (row[x] + prev[x]) & 255
        elif f == 3:
            for x in range(stride):
                a = row[x - bpp] if x >= bpp else 0
                row[x] = (row[x] + ((a + prev[x]) // 2)) & 255
        elif f == 4:
            for x in range(stride):
                a = row[x - bpp] if x >= bpp else 0
                b = prev[x]
                c = prev[x - bpp] if x >= bpp else 0
                p = a + b - c
                pa, pb, pc = abs(p - a), abs(p - b), abs(p - c)
                pr = a if pa <= pb and pa <= pc else (b if pb <= pc else c)
                row[x] = (row[x] + pr) & 255
        elif f != 0:
            raise SystemExit(f"unsupported filter {f}")
        rows.append(row)
        prev = row
    return w, h, bpp, rows


def encode_png(w: int, h: int, bpp: int, rows: list[bytearray]) -> bytes:
    color = 6 if bpp == 4 else 2
    raw = bytearray()
    for row in rows:
        raw.append(0)
        raw.extend(row)
    compressed = zlib.compress(bytes(raw), 9)

    def chunk(ctype: bytes, cdata: bytes) -> bytes:
        return (
            struct.pack(">I", len(cdata))
            + ctype
            + cdata
            + struct.pack(">I", zlib.crc32(ctype + cdata) & 0xFFFFFFFF)
        )

    ihdr = struct.pack(">IIBBBBB", w, h, 8, color, 0, 0, 0)
    return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", ihdr) + chunk(b"IDAT", compressed) + chunk(b"IEND", b"")


def crop(path: Path, pad: int = 6, alpha_thresh: int = 10) -> None:
    data = path.read_bytes()
    w, h, bpp, rows = decode_png(data)
    minx, miny, maxx, maxy = w, h, -1, -1
    for y, row in enumerate(rows):
        for x in range(w):
            o = x * bpp
            if bpp == 4:
                keep = row[o + 3] > alpha_thresh
            else:
                r, g, b = row[o], row[o + 1], row[o + 2]
                keep = (r, g, b) != (247, 248, 250)
            if keep:
                minx = min(minx, x)
                miny = min(miny, y)
                maxx = max(maxx, x)
                maxy = max(maxy, y)
    if maxx < 0:
        print(f"{path.name}: no content", file=sys.stderr)
        return
    minx = max(0, minx - pad)
    miny = max(0, miny - pad)
    maxx = min(w - 1, maxx + pad)
    maxy = min(h - 1, maxy + pad)
    nw, nh = maxx - minx + 1, maxy - miny + 1
    out_rows = [rows[y][minx * bpp : (maxx + 1) * bpp] for y in range(miny, maxy + 1)]
    path.write_bytes(encode_png(nw, nh, bpp, out_rows))
    print(f"cropped {path.name}: {w}x{h} -> {nw}x{nh}")


def main() -> None:
    if len(sys.argv) < 2:
        raise SystemExit("usage: crop-phase3-pngs.py <png> [pad] [alphaThresh]")
    path = Path(sys.argv[1])
    pad = int(sys.argv[2]) if len(sys.argv) > 2 else 6
    alpha = int(sys.argv[3]) if len(sys.argv) > 3 else 10
    crop(path, pad=pad, alpha_thresh=alpha)


if __name__ == "__main__":
    main()
