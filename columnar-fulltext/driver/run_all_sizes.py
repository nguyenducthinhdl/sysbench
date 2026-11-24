#!/usr/bin/env python3
"""Run the 2, 5, and 10 GB prefixes in order."""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

DRIVER = Path(__file__).resolve().parent


def main() -> int:
    py = sys.executable
    sizes = [2, 5, 10]
    if sys.argv[1:]:
        sizes = [int(item) for item in sys.argv[1:]]
    for size in sizes:
        print(f"===== size {size}gb =====", flush=True)
        result = subprocess.run([py, str(DRIVER / "run_size.py"), "--size", str(size)])
        if result.returncode != 0:
            print(f"size {size}gb failed with {result.returncode}", flush=True)
            return result.returncode
        print(f"===== size {size}gb done =====", flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
