# sysbench

On-prem logging-engine comparison. The ClickHouse ingest-architecture shakedown
(Phase 1A) lives in [`phase1a/`](phase1a/README.md).

## Clone and fetch the log file

The raw LogHub dump is not in git (it is larger than 2 MB). After clone, pull it
from Zenodo:

```bash
git clone https://github.com/nguyenducthinhdl/sysbench.git
cd sysbench
./scripts/init-data.sh
```

That downloads [`HDFS_v1.zip`](https://zenodo.org/records/8196385/files/HDFS_v1.zip?download=1)
(~178 MB) into `data/` and unpacks `HDFS.log`. Direct link, record page, and checksums:
[`data/README.md`](data/README.md).

There is no `git clone --init` flag. `git clone --recurse-submodules` is for submodules;
this repo does not use them. The init step is the script above.

## Smoke-test ClickHouse on a Mac

Docker (or Colima) must be running.

```bash
./scripts/init-data.sh
phase1a/macos/driver/replay.sh a2m 1 throttled
```

On the Linux rig: [`phase1a/linux/driver/replay.sh`](phase1a/linux/driver/replay.sh).
