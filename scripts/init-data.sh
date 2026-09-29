#!/usr/bin/env bash
# Fetch LogHub HDFS_v1 into data/. The zip is ~178 MB and is not in git.
#
#   git clone https://github.com/nguyenducthinhdl/sysbench.git
#   cd sysbench
#   ./scripts/init-data.sh
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DATA="${ROOT}/data"
ZIP="${DATA}/HDFS_v1.zip"
LOG="${DATA}/HDFS.log"
ZIP_URL="https://zenodo.org/records/8196385/files/HDFS_v1.zip?download=1"
LOG_SHA256="0783096174d7832c618337f9609e06e04abd86ddd7089b3c12b407e63bfebc52"

mkdir -p "$DATA"
cd "$DATA"

if [[ -f "$LOG" ]]; then
  actual=$(shasum -a 256 "$LOG" | awk '{print $1}')
  if [[ "$actual" == "$LOG_SHA256" ]]; then
    echo "data/HDFS.log already present and checksum matches."
    exit 0
  fi
  echo "data/HDFS.log exists but checksum does not match. Re-download." >&2
fi

if [[ ! -f "$ZIP" ]]; then
  echo "Downloading HDFS_v1.zip from Zenodo (~178 MB)..."
  if command -v curl >/dev/null; then
    curl -fL --retry 3 --retry-delay 2 -o "$ZIP" "$ZIP_URL"
  elif command -v wget >/dev/null; then
    wget -O "$ZIP" "$ZIP_URL"
  else
    echo "need curl or wget" >&2
    exit 1
  fi
fi

echo "Unpacking HDFS.log and preprocessed/ into data/ ..."
# Do not extract the zip's README.md; it would overwrite data/README.md in this repo.
unzip -o -q "$ZIP" "HDFS.log" "preprocessed/*" -d "$DATA"

actual=$(shasum -a 256 "$LOG" | awk '{print $1}')
if [[ "$actual" != "$LOG_SHA256" ]]; then
  echo "HDFS.log checksum mismatch after unzip: $actual" >&2
  echo "expected: $LOG_SHA256" >&2
  exit 1
fi

echo "Ready. data/HDFS.log is 11,175,629 lines."
echo "Next: phase1a/macos/driver/replay.sh a2m 1 throttled"
