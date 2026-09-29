#!/usr/bin/env python3
"""Create the production HDFS service schema from the LogHub sample."""

from __future__ import annotations

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from hdfs_service.__main__ import main

if __name__ == "__main__":
    raise SystemExit(main())
