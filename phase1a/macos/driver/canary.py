#!/usr/bin/env python3
"""Emit one canary line per second into Vector's canary socket.

Python rather than `date +%s%3N`, which macOS does not support, and rather than bash
/dev/tcp, which Apple's bash 3.2 does not have. The line format matches the Linux canary
exactly, so collect.sql does not care which host produced it.
"""

from __future__ import annotations

import socket
import sys
import time

LINE = "081111 111628 0 INFO sysbench.Canary: zzqcanary {ms}\n"


def main() -> int:
    host = sys.argv[1] if len(sys.argv) > 1 else "127.0.0.1"
    port = int(sys.argv[2]) if len(sys.argv) > 2 else 9001
    conn = socket.create_connection((host, port))
    try:
        while True:
            conn.sendall(LINE.format(ms=int(time.time() * 1000)).encode())
            time.sleep(1)
    finally:
        conn.close()


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (BrokenPipeError, ConnectionError, KeyboardInterrupt):
        raise SystemExit(0)
