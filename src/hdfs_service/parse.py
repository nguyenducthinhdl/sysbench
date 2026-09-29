"""Parse LogHub HDFS lines and attach the instance IP as the event host.

The structure file fixes the columns (Date, Time, Pid, Level, Component, Content).
It does not name the machine that wrote the line. Elasticsearch logsdb sorts on
``host.name`` before ``@timestamp``, so every event still carries a host, and that
host is the instance IP when the line identifies one.

Instance address, in order:

1. A DataNode self-prefix at the start of Content (``10.x.x.x:50010:...``).
2. The ``dest`` address on a receive line, which is the local DataNode.
3. The address in ``blockMap updated:``, the DataNode the NameNode registered.

``from`` and ``to`` addresses are peers. They are kept on ``attributes.peer_ip``
and are not used as the host.
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path

STRUCTURE_COLUMNS = ["Date", "Time", "Pid", "Level", "Component", "Content"]

_LINE = re.compile(
    r"^(?P<date>\d{6}) (?P<time>\d{6}) (?P<pid>\d+) "
    r"(?P<level>[A-Z]+) (?P<component>\S+): (?P<content>.*)$"
)
_IPV4 = r"(?:\d{1,3}\.){3}\d{1,3}"
_LEADING = re.compile(rf"^({_IPV4}):(\d+):")
_DEST = re.compile(rf"dest:\s*/({_IPV4})(?::(\d+))?")
_BLOCKMAP = re.compile(rf"blockMap updated:\s*({_IPV4})(?::(\d+))?")
_ANY_IP = re.compile(_IPV4)
_BLOCK_ID = re.compile(r"\b(blk_-?\d+)\b")
_SIZE = re.compile(r"\bsize\s+(\d+)\b")

_NAMENODE_COMPONENTS = {"dfs.FSNamesystem"}


@dataclass(frozen=True)
class LogLine:
    date: str
    time: str
    pid: int
    level: str
    component: str
    content: str


@dataclass(frozen=True)
class Service:
    cluster: str = "dc1"
    namespace: str = "hdfs"
    container: str = "hdfs"


def load_structure(path: Path) -> list[str]:
    """Return the example row. The header is the column contract."""
    text = path.read_text(encoding="utf-8")
    rows = [line.split("\t") for line in text.splitlines() if line.strip()]
    if len(rows) < 2:
        raise ValueError(f"{path}: expected a header and one example row")
    header, example = rows[0], rows[1]
    if header != STRUCTURE_COLUMNS:
        raise ValueError(
            f"{path}: header {header} does not match {STRUCTURE_COLUMNS}"
        )
    if len(example) != len(header):
        raise ValueError(f"{path}: example row has {len(example)} fields, expected {len(header)}")
    return example


def parse_line(raw: str) -> LogLine:
    match = _LINE.match(raw.rstrip("\n"))
    if match is None:
        raise ValueError(f"line does not match the HDFS structure: {raw.rstrip()[:160]}")
    return LogLine(
        date=match.group("date"),
        time=match.group("time"),
        pid=int(match.group("pid")),
        level=match.group("level"),
        component=match.group("component"),
        content=match.group("content"),
    )


def line_fields(line: LogLine) -> list[str]:
    return [line.date, line.time, str(line.pid), line.level, line.component, line.content]


def _ipv4(value: str) -> bool:
    parts = value.split(".")
    if len(parts) != 4:
        return False
    try:
        return all(0 <= int(part) <= 255 and str(int(part)) == part for part in parts)
    except ValueError:
        return False


def instance_address(content: str) -> tuple[str, str] | None:
    """Return ``(ip, port)`` for the HDFS instance this line is about."""
    for pattern in (_LEADING, _DEST, _BLOCKMAP):
        match = pattern.search(content)
        if match and _ipv4(match.group(1)):
            return match.group(1), match.group(2) or ""
    return None


def _peer_ip(content: str, host_ip: str) -> str:
    for candidate in _ANY_IP.findall(content):
        if candidate != host_ip and _ipv4(candidate):
            return candidate
    return ""


def _app(component: str) -> str:
    if component in _NAMENODE_COMPONENTS:
        return "namenode"
    return "datanode"


def _timestamp(line: LogLine) -> str:
    moment = datetime.strptime(line.date + line.time, "%y%m%d%H%M%S").replace(
        tzinfo=timezone.utc
    )
    return moment.strftime("%Y-%m-%dT%H:%M:%S.000000Z")


def to_event(line: LogLine, service: Service) -> dict:
    """Shape one line as a production log event. Host is the instance IP."""
    app = _app(line.component)
    found = instance_address(line.content)
    host_ip = found[0] if found else ""
    host: dict[str, str] = {"name": host_ip}
    if host_ip:
        host["ip"] = host_ip
        pod = f"{app}-{host_ip.replace('.', '-')}"
    else:
        pod = f"{app}-unattributed"

    attributes: dict[str, str] = {}
    block = _BLOCK_ID.search(line.content)
    block_id = block.group(1) if block else ""
    size = _SIZE.search(line.content)
    if size:
        attributes["size"] = size.group(1)
    peer = _peer_ip(line.content, host_ip)
    if peer:
        attributes["peer_ip"] = peer
    if found and found[1]:
        attributes["instance_port"] = found[1]

    return {
        "timestamp": _timestamp(line),
        "cluster": service.cluster,
        "namespace": service.namespace,
        "app": app,
        "pod": pod,
        "container": service.container,
        "host": host,
        "level": line.level.lower(),
        "pid": line.pid,
        "component": line.component,
        "block_id": block_id,
        "attributes": attributes,
        "body": line.content,
    }


def load_log(path: Path) -> list[LogLine]:
    lines: list[LogLine] = []
    errors: list[str] = []
    text = path.read_text(encoding="utf-8")
    for number, raw in enumerate(text.splitlines(), start=1):
        if not raw.strip():
            continue
        try:
            lines.append(parse_line(raw))
        except ValueError as exc:
            errors.append(f"{path}:{number}: {exc}")
    if errors:
        preview = "\n".join(errors[:10])
        extra = f"\n... {len(errors) - 10} more" if len(errors) > 10 else ""
        raise ValueError(f"{len(errors)} lines failed to parse\n{preview}{extra}")
    if not lines:
        raise ValueError(f"{path}: no log lines")
    return lines
