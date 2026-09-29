#!/usr/bin/env python3
"""Write a log file to stdout, with the limits replay.sh needs.

macOS has neither pv nor GNU timeout, and per-line sleeping in bash cannot hold 50k
lines/s. Chunks are paced against a schedule so the rate stays honest without a sleep
per line.
"""

from __future__ import annotations

import argparse
import sys
import time


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("path")
    parser.add_argument("--rate", type=float, default=0, help="lines/sec; 0 means unlimited")
    parser.add_argument("--max-events", type=int, default=0)
    parser.add_argument("--max-seconds", type=float, default=0)
    args = parser.parse_args()

    chunk = 2000 if args.rate else 65536
    sent = 0
    buf: list[bytes] = []
    start = time.monotonic()
    out = sys.stdout.buffer

    with open(args.path, "rb") as handle:
        for line in handle:
            if args.max_events and sent >= args.max_events:
                break
            if args.max_seconds and (time.monotonic() - start) >= args.max_seconds:
                break
            buf.append(line)
            sent += 1
            if len(buf) < chunk:
                continue
            out.write(b"".join(buf))
            out.flush()
            buf.clear()
            if args.rate:
                delay = (sent / args.rate) - (time.monotonic() - start)
                if delay > 0:
                    time.sleep(delay)
    if buf:
        out.write(b"".join(buf))
        out.flush()
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except BrokenPipeError:
        raise SystemExit(0)
