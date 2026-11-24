#!/usr/bin/env python3
"""Load, verify, snapshot storage, and time search for one corpus size.

    python3 columnar-fulltext/driver/run_size.py --size 2
"""

from __future__ import annotations

import argparse
import subprocess
import sys
from pathlib import Path

DRIVER = Path(__file__).resolve().parent


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--size", type=int, required=True)
    args = parser.parse_args()
    py = sys.executable
    steps = [
        [py, str(DRIVER / "gate.py"), "--size", str(args.size), "--skip-freeze"],
        [py, str(DRIVER / "storage.py"), "--size", str(args.size)],
        [py, str(DRIVER / "search_bench.py"), "--size", str(args.size)],
    ]
    for cmd in steps:
        print("+", " ".join(cmd), flush=True)
        result = subprocess.run(cmd)
        if result.returncode != 0:
            return result.returncode
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
