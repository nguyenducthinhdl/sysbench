#!/usr/bin/env python3
"""Deterministic corpus for the columnar / full-text comparison.

One seeded stream. Cuts at 1, 2, 5, 10, and 20 GB are nested byte prefixes of
that stream, each ending on a layer boundary so every prefix still spans the
same seven logical days. A layer is a few thousand events; timestamps inside a
layer are sorted, and successive layers walk the week again. The file is
therefore layer-major rather than one global time sort: a global sort would
make the 1 GB prefix the first few hours, and the window axis would collapse
into the size axis.

Needle strings are the corpus-spec names (``zzq_s5`` and the rest). ClickHouse
``splitByNonAlpha`` splits on underscore, so each name is two tokens. They are
planted together, surrounded by non-alphanumeric characters, and nowhere else.

INC-2 only: payment-api rejection rate moves from 2% to 6% over 48 hours while
traffic keeps the diurnal curve. The typed flag is the ``status`` field.
"""

from __future__ import annotations

import argparse
import bisect
import hashlib
import json
import random
import subprocess
import sys
import time
from collections import Counter
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from pathlib import Path

GENERATOR_VERSION = "1"
GB = 1_000_000_000
STANDARD_GB = (1, 2, 5, 10, 20)
UTC = timezone.utc
START = datetime(2026, 9, 1, tzinfo=UTC)
WINDOW = timedelta(days=7)
INC2_ONSET = START + timedelta(days=2)  # 2026-09-03, inside the middle five days
INC2_END = INC2_ONSET + timedelta(hours=48)
HOUR_WEIGHT = (
    2, 1, 1, 1, 1, 1,
    2, 2, 2, 3, 3, 3,
    3, 3, 3, 3, 3, 2,
    2, 2, 2, 2, 2, 2,
)
BURST_HOURS = {10, 11}
CLUSTERS = ("dc1", "dc2")
NAMESPACES = ("payments", "orders", "inventory", "web", "platform")
APPS = ("checkout-api", "payment-api", "orders-api", "web-bff")
PODS = ("p0", "p1", "p2", "p3", "p4")
METHODS = ("GET", "POST", "PUT", "DELETE")
PATHS = ("/v1/charge", "/v1/refund", "/v1/order", "/v1/item", "/v1/health") + tuple(
    f"/v1/p{i:03d}" for i in range(195)
)
STRUCTURED = (
    "charge failed for order {o}: upstream returned {code}",
    "payment accepted for order {o} in {d} ms",
    "checkout started for order {o}",
    "retry scheduled for order {o} attempt {a}",
    "inventory reserved sku {s}",
)
FREEFORM = (
    "customer asked for a receipt after checkout",
    "on call noted a slow response and left a note",
    "deploy finished and the pod stayed up",
    "queue depth looked high for a few minutes",
    "user retried the request and it went through",
    "disk latency spiked then recovered",
    "the worker restarted after a timeout",
    "config reload completed without a restart",
    "cache miss on the hot key during the peak",
    "downstream replied late but the call succeeded",
    "session ended when the client closed the socket",
    "rollover moved the index to the next shard",
)
NEEDLE_KEYS = (
    "zzq_s5",
    "zzq_s7",
    "zzq_s9",
    "zzq_pa",
    "zzq_pb",
    "zzq_pa_zzq_pb",
    "abc_decoy",
)


class CorpusAssertionError(Exception):
    """A corpus-spec assertion failed. The process exits non-zero."""


@dataclass(frozen=True)
class Stream:
    cluster: str
    namespace: str
    app: str
    pod: str
    host: str
    hot: bool

    @property
    def key(self) -> str:
        return f"{self.cluster}/{self.namespace}/{self.app}/{self.pod}"


def build_streams() -> list[Stream]:
    streams: list[Stream] = []
    for cluster in CLUSTERS:
        for namespace in NAMESPACES:
            for app in APPS:
                for pod_id in PODS:
                    hot = cluster == "dc1" and pod_id == "p0"
                    streams.append(
                        Stream(
                            cluster,
                            namespace,
                            app,
                            f"{app}-{pod_id}",
                            f"node-{len(streams) % 60:03d}",
                            hot,
                        )
                    )
    if len(streams) != 200 or sum(s.hot for s in streams) != 20:
        raise CorpusAssertionError("baseline stream catalog is not 2x5x4x5")
    return streams


def cumulative(weights: list[int]) -> tuple[list[int], int]:
    total = 0
    ends: list[int] = []
    for weight in weights:
        total += weight
        ends.append(total)
    return ends, total


def build_week() -> tuple[list[tuple[int, int, int]], list[int], int]:
    minutes: list[tuple[int, int, int]] = []
    weights: list[int] = []
    for day in range(7):
        for hour in range(24):
            for minute in range(60):
                minutes.append((day, hour, minute))
                weights.append(HOUR_WEIGHT[hour])
    ends, total = cumulative(weights)
    return minutes, ends, total


def pick(ends: list[int], total: int, ordinal: int) -> int:
    return bisect.bisect_right(ends, ordinal % total)


def level_of(ordinal: int) -> str:
    residue = ordinal % 200
    if residue < 156:
        return "info"
    if residue < 184:
        return "warn"
    if residue < 196:
        return "error"
    if residue < 199:
        return "debug"
    return "fatal"


def kind_of(ordinal: int) -> str:
    residue = ordinal % 100
    if residue < 55:
        return "structured"
    if residue < 70:
        return "access"
    if residue < 90:
        return "freeform"
    return "stack"


def needles_of(ordinal: int) -> list[str]:
    """Planted whole-token names for this ordinal. At most one."""
    n1 = ordinal + 1
    if n1 % 100_000 == 50_000:
        return ["zzq_s5"]
    if n1 % 10_000_000 == 2_500_000:
        return ["zzq_s7"]
    if n1 % 1_000_000_000 == 500_000_000:
        return ["zzq_s9"]
    if n1 % 10_000_000 == 7_500_000:
        return ["zzq_pa", "zzq_pb"]
    if n1 % 100_000 == 10_000:
        return ["zzq_pa"]
    if n1 % 100_000 == 90_000:
        return ["zzq_pb"]
    return []


def decoy_of(ordinal: int) -> str | None:
    n1 = ordinal + 1
    if n1 % 100_000 != 30_000:
        return None
    return "traceabc" if (n1 // 100_000) % 2 == 0 else "xxabcyy"


def count_mod(events: int, modulus: int, residue: int) -> int:
    """How many n in ``0 .. events-1`` have ``n % modulus == residue``."""
    full, rem = divmod(events, modulus)
    return full + (1 if residue < rem else 0)


def count_residue(events: int, modulus: int, residue: int) -> int:
    if events <= 0:
        return 0
    if residue == 0:
        return events // modulus
    if events < residue:
        return 0
    return (events - residue) // modulus + 1


def expected_needles(events: int) -> dict[str, int]:
    both = count_residue(events, 10_000_000, 7_500_000)
    return {
        "zzq_s5": count_residue(events, 100_000, 50_000),
        "zzq_s7": count_residue(events, 10_000_000, 2_500_000),
        "zzq_s9": count_residue(events, 1_000_000_000, 500_000_000),
        "zzq_pa": count_residue(events, 100_000, 10_000) + both,
        "zzq_pb": count_residue(events, 100_000, 90_000) + both,
        "zzq_pa_zzq_pb": both,
        "abc_decoy": count_residue(events, 100_000, 30_000),
    }


def inc2_rate(hour_index: int) -> tuple[int, int]:
    """2% at hour 0 through 6% at hour 47, as an exact rational."""
    num = 20_000 * 47 + 40_000 * hour_index
    den = 47 * 1_000_000
    return num, den


def take_rejected(index: int, num: int, den: int) -> bool:
    return (index + 1) * num // den > index * num // den


def fmt_ts(ts: datetime) -> str:
    return (
        f"{ts.year:04d}-{ts.month:02d}-{ts.day:02d}T"
        f"{ts.hour:02d}:{ts.minute:02d}:{ts.second:02d}.{ts.microsecond:06d}Z"
    )


def attr_count(ordinal: int) -> int:
    residue = ordinal % 100
    if residue < 50:
        return 1
    if residue < 80:
        return 2
    if residue < 90:
        return 3
    if residue < 95:
        return 4
    if residue < 99:
        return 5
    return 6


def hex_id(rng: random.Random, nbytes: int) -> str:
    for _ in range(8):
        value = rng.getrandbits(nbytes * 8).to_bytes(nbytes, "big").hex()
        if "abc" not in value:
            return value
    raise CorpusAssertionError("trace id kept containing abc")


def assign_levels(events: list[dict]) -> None:
    """Keep the exact level multiset, and prefer burst hours for errors."""
    bag = [level_of(event["ordinal"]) for event in events]
    error_n = sum(1 for label in bag if label == "error")
    rest = [label for label in bag if label != "error"]
    burst_idx = [i for i, event in enumerate(events) if event["ts"].hour in BURST_HOURS]
    target = min(len(burst_idx), (error_n * 4) // 5)
    chosen: list[int] = []
    if target:
        step = len(burst_idx) / target
        for k in range(target):
            chosen.append(burst_idx[min(len(burst_idx) - 1, int(k * step))])
        chosen = list(dict.fromkeys(chosen))
        for index in burst_idx:
            if len(chosen) >= target:
                break
            if index not in chosen:
                chosen.append(index)
    assigned = {index: "error" for index in chosen}
    labels = ["error"] * (error_n - len(chosen)) + rest
    slots = [i for i in range(len(events)) if i not in assigned]
    if len(labels) != len(slots):
        raise CorpusAssertionError("level reassignment lost a label")
    for index, label in zip(slots, labels):
        assigned[index] = label
    for index, event in enumerate(events):
        event["level"] = assigned[index]


def static_text_ok() -> None:
    blob = " ".join(
        list(STRUCTURED)
        + list(FREEFORM)
        + list(PATHS)
        + list(APPS)
        + list(NAMESPACES)
        + list(METHODS)
    )
    if "zzq" in blob or "abc" in blob or "connection refused" in blob:
        raise CorpusAssertionError("a static template contains a planted token")


def build_body(event: dict) -> str:
    ordinal = event["ordinal"]
    order = ordinal % 1_000_000
    kind = event["kind"]
    if kind == "structured":
        body = STRUCTURED[ordinal % len(STRUCTURED)].format(
            o=order,
            code=500 if event["level"] in ("error", "fatal") else 200,
            d=event["duration_ms"],
            a=1 + ordinal % 4,
            s=1000 + ordinal % 50,
        )
    elif kind == "access":
        body = (
            f"{event['method']} {event['path']} {event['http_status']} "
            f"{event['duration_ms']} rid={order}"
        )
    elif kind == "freeform":
        body = FREEFORM[ordinal % len(FREEFORM)]
    else:
        nlines = 20 + ordinal % 41
        lines = [f"java.lang.IllegalStateException: order {order} closed"]
        lines.extend(f"at A.a(A:{i})" for i in range(1, nlines))
        body = "\n".join(lines)
    if event["needles"]:
        body += "".join(f" {token}" for token in event["needles"])
    decoy = event["decoy"]
    if decoy == "traceabc":
        body += " traceabc=1"
    elif decoy == "xxabcyy":
        body += " xxabcyy"
    if event["phrase"]:
        body += " connection refused"
    if event["healthcheck"]:
        body += " healthcheck"
    scrub = body
    for token in event["needles"]:
        scrub = scrub.replace(token, " ")
    if "zzq" in scrub:
        raise CorpusAssertionError(f"accidental zzq at ordinal {ordinal}")
    scrub = body.replace("traceabc", " ").replace("xxabcyy", " ")
    if "abc" in scrub:
        raise CorpusAssertionError(f"accidental abc at ordinal {ordinal}")
    return body


def render(event: dict, rng: random.Random) -> tuple[bytes, str]:
    stream: Stream = event["stream"]
    keys = ["attr0000"]
    if event["n_attrs"] > 1:
        keys.append("attr0001")
    while len(keys) < event["n_attrs"]:
        key = f"attr{rng.randrange(20, 2000):04d}"
        if key not in keys:
            keys.append(key)
    attributes = {key: str((event["ordinal"] + i) % 1000) for i, key in enumerate(keys)}
    body = build_body(event)
    obj = {
        "timestamp": fmt_ts(event["ts"]),
        "cluster": stream.cluster,
        "namespace": stream.namespace,
        "app": stream.app,
        "pod": stream.pod,
        "container": "app",
        "host": {"name": stream.host},
        "level": event["level"],
        "trace_id": event["trace_id"],
        "span_id": event["span_id"],
        "http": {
            "method": event["method"],
            "path": event["path"],
            "status": event["http_status"],
        },
        "duration_ms": event["duration_ms"],
        "attributes": attributes,
        "status": "rejected" if event["rejected"] else "ok",
        "body": body,
    }
    return json.dumps(obj, separators=(",", ":")).encode("utf-8") + b"\n", body


def new_hour() -> dict[str, int]:
    return {
        "events": 0,
        "dc1": 0,
        "error_checkout": 0,
        "stack": 0,
        "zzq_s5": 0,
        "zzq_s5_dc1": 0,
        "zzq_s7": 0,
        "zzq_s9": 0,
        "zzq_pa": 0,
        "zzq_pb": 0,
        "zzq_both": 0,
        "abc": 0,
        "phrase": 0,
        "s5_health": 0,
        "s5_health_dc1": 0,
    }


class Tally:
    def __init__(self) -> None:
        self.events = 0
        self.levels: Counter[str] = Counter()
        self.kinds: Counter[str] = Counter()
        self.streams: set[str] = set()
        self.by_hour = [new_hour() for _ in range(168)]
        self.inc_total = [0] * 48
        self.inc_rejected = [0] * 48
        self.min_ts: datetime | None = None
        self.max_ts: datetime | None = None
        self.sample_trace_id: str | None = None
        self.sample_stack_trace_id: str | None = None
        self.sample_stack_body: str | None = None
        self.sample_stack_newlines = 0

    def add(self, event: dict, body: str) -> None:
        stream: Stream = event["stream"]
        ts: datetime = event["ts"]
        self.events += 1
        self.levels[event["level"]] += 1
        self.kinds[event["kind"]] += 1
        self.streams.add(stream.key)
        if self.min_ts is None or ts < self.min_ts:
            self.min_ts = ts
        if self.max_ts is None or ts > self.max_ts:
            self.max_ts = ts
        if event["ordinal"] == 0:
            self.sample_trace_id = event["trace_id"]
        hour = int((ts - START).total_seconds() // 3600)
        if not 0 <= hour < 168:
            raise CorpusAssertionError(f"timestamp outside the seven-day window: {ts}")
        bucket = self.by_hour[hour]
        bucket["events"] += 1
        dc1 = stream.cluster == "dc1"
        if dc1:
            bucket["dc1"] += 1
        if event["level"] == "error" and stream.app == "checkout-api":
            bucket["error_checkout"] += 1
        if event["kind"] == "stack":
            bucket["stack"] += 1
            if self.sample_stack_trace_id is None:
                self.sample_stack_trace_id = event["trace_id"]
                self.sample_stack_body = body
                self.sample_stack_newlines = body.count("\n")
        names = event["needles"]
        if "zzq_s5" in names:
            bucket["zzq_s5"] += 1
            if dc1:
                bucket["zzq_s5_dc1"] += 1
            if event["healthcheck"]:
                bucket["s5_health"] += 1
                if dc1:
                    bucket["s5_health_dc1"] += 1
        if "zzq_s7" in names:
            bucket["zzq_s7"] += 1
        if "zzq_s9" in names:
            bucket["zzq_s9"] += 1
        if "zzq_pa" in names:
            bucket["zzq_pa"] += 1
        if "zzq_pb" in names:
            bucket["zzq_pb"] += 1
        if "zzq_pa" in names and "zzq_pb" in names:
            bucket["zzq_both"] += 1
        if event["decoy"]:
            bucket["abc"] += 1
        if event["phrase"]:
            bucket["phrase"] += 1
        if stream.app == "payment-api" and INC2_ONSET <= ts < INC2_END:
            inc_h = int((ts - INC2_ONSET).total_seconds() // 3600)
            self.inc_total[inc_h] += 1
            if event["rejected"]:
                self.inc_rejected[inc_h] += 1


class ShardHasher:
    def __init__(self, shard_bytes: int) -> None:
        self.shard_bytes = shard_bytes
        self.whole = hashlib.sha256()
        self.shard = hashlib.sha256()
        self.shards: list[dict] = []
        self.filled = 0
        self.shard_events = 0
        self.total = 0
        self.events = 0

    def write(self, line: bytes) -> None:
        self.whole.update(line)
        self.shard.update(line)
        self.filled += len(line)
        self.shard_events += 1
        self.total += len(line)
        self.events += 1

    def rotate(self, force: bool = False) -> None:
        if self.filled == 0:
            return
        if force or self.filled >= self.shard_bytes:
            self.shards.append(
                {
                    "index": len(self.shards),
                    "bytes": self.filled,
                    "events": self.shard_events,
                    "sha256": self.shard.hexdigest(),
                }
            )
            self.shard = hashlib.sha256()
            self.filled = 0
            self.shard_events = 0

    def finish(self) -> None:
        self.rotate(force=True)


def label_for(target: int) -> str:
    if target % GB == 0 and target // GB in STANDARD_GB:
        return f"{target // GB}gb"
    return f"b{target}"


def git_commit() -> str:
    try:
        return subprocess.check_output(
            ["git", "rev-parse", "HEAD"],
            cwd=Path(__file__).resolve().parents[2],
            text=True,
            stderr=subprocess.DEVNULL,
        ).strip()
    except (subprocess.CalledProcessError, FileNotFoundError):
        return "unknown"


def sum_hours(by_hour: list[dict[str, int]], key: str, start: int = 0, end: int = 168) -> int:
    return sum(bucket[key] for bucket in by_hour[start:end])


def rate_rises_count_falls(hours: list[dict]) -> bool:
    for left, right in zip(hours, hours[1:]):
        if right["intended_rate"] > left["intended_rate"] and right["rejected"] < left["rejected"]:
            return True
    return False


def incident_document(tally: Tally, streams: list[Stream]) -> dict:
    hours = []
    for index in range(48):
        num, den = inc2_rate(index)
        total = tally.inc_total[index]
        rejected = tally.inc_rejected[index]
        if rejected != total * num // den:
            raise CorpusAssertionError(
                f"INC-2 hour {index} rejected {rejected} != floor(total * rate)"
            )
        hour_ts = INC2_ONSET + timedelta(hours=index)
        hours.append(
            {
                "hour": fmt_ts(hour_ts),
                "index": index,
                "intended_rate": num / den,
                "total": total,
                "rejected": rejected,
            }
        )
    thresholds = {}
    for threshold in (0.03, 0.04, 0.05):
        for hour in hours:
            if hour["intended_rate"] > threshold:
                thresholds[f"{threshold:.2f}"] = hour["hour"]
                break
        else:
            raise CorpusAssertionError(f"INC-2 never exceeds {threshold}")
    if not (START + timedelta(days=1) <= INC2_ONSET and INC2_END <= START + timedelta(days=6)):
        raise CorpusAssertionError("INC-2 is outside the middle five days")
    return {
        "incidents": [
            {
                "id": "INC-2",
                "type": "gradual_ratio_drift",
                "app": "payment-api",
                "onset": fmt_ts(INC2_ONSET),
                "end": fmt_ts(INC2_END),
                "affected_streams": sorted(
                    stream.key for stream in streams if stream.app == "payment-api"
                ),
                "root_cause": (
                    "payment-api rejection ratio drifts from 2% to 6% over 48h "
                    "while traffic follows a 3x diurnal curve"
                ),
                "rate_start": 0.02,
                "rate_end": 0.06,
                "hours": hours,
                "first_exceeds": thresholds,
                "detectable_from": thresholds["0.03"],
                "absolute_rejection_count_not_monotonic": rate_rises_count_falls(hours),
            }
        ]
    }


def assert_tally(tally: Tally, target: int, strict: bool) -> None:
    expected = expected_needles(tally.events)
    actual = {
        "zzq_s5": sum_hours(tally.by_hour, "zzq_s5"),
        "zzq_s7": sum_hours(tally.by_hour, "zzq_s7"),
        "zzq_s9": sum_hours(tally.by_hour, "zzq_s9"),
        "zzq_pa": sum_hours(tally.by_hour, "zzq_pa"),
        "zzq_pb": sum_hours(tally.by_hour, "zzq_pb"),
        "zzq_pa_zzq_pb": sum_hours(tally.by_hour, "zzq_both"),
        "abc_decoy": sum_hours(tally.by_hour, "abc"),
    }
    if actual != expected:
        raise CorpusAssertionError(f"needle counts {actual} != {expected}")
    if sum(tally.levels.values()) != tally.events:
        raise CorpusAssertionError("level counts do not add up")
    for name, start, end in (
        ("info", 0, 156),
        ("warn", 156, 184),
        ("error", 184, 196),
        ("debug", 196, 199),
        ("fatal", 199, 200),
    ):
        want = sum(count_mod(tally.events, 200, residue) for residue in range(start, end))
        if tally.levels[name] != want:
            raise CorpusAssertionError(f"level {name} is {tally.levels[name]}, want {want}")
    for name, start, end in (
        ("structured", 0, 55),
        ("access", 55, 70),
        ("freeform", 70, 90),
        ("stack", 90, 100),
    ):
        want = sum(count_mod(tally.events, 100, residue) for residue in range(start, end))
        if tally.kinds[name] != want:
            raise CorpusAssertionError(f"content {name} is {tally.kinds[name]}, want {want}")
    if tally.min_ts is None or tally.max_ts is None:
        raise CorpusAssertionError("empty prefix")
    if tally.max_ts - tally.min_ts < timedelta(days=6, hours=23):
        raise CorpusAssertionError("prefix does not span seven logical days")
    if tally.events >= 900 and len(tally.streams) != 200:
        raise CorpusAssertionError(f"stream count {len(tally.streams)} != 200")
    if strict and target >= GB and actual["zzq_s5"] < 20:
        raise CorpusAssertionError(
            f"{label_for(target)} has {actual['zzq_s5']} zzq_s5 hits; the floor is 20"
        )
    payment = sum(tally.inc_total)
    if strict and payment >= 48 * 30:
        incident = incident_document(tally, build_streams())
        if not incident["incidents"][0]["absolute_rejection_count_not_monotonic"]:
            raise CorpusAssertionError("INC-2 rejection count never falls while the rate rises")


def manifest_for(
    tally: Tally,
    target: int,
    hasher: ShardHasher,
    prefix_sha: str,
    seed: int,
    arm: str,
    commit: str,
) -> dict:
    needles = expected_needles(tally.events)
    return {
        "generator_version": GENERATOR_VERSION,
        "git_commit": commit,
        "python": sys.version.split()[0],
        "seed": seed,
        "arm": arm,
        "size_label": label_for(target),
        "target_bytes": target,
        "actual_bytes": hasher.total,
        "event_count": tally.events,
        "sha256": hasher.whole.hexdigest(),
        "sha256_first_target_bytes": prefix_sha,
        "shard_count": len(hasher.shards),
        "shards": hasher.shards,
        "time_start": fmt_ts(START),
        "time_end": fmt_ts(START + WINDOW),
        "min_timestamp": fmt_ts(tally.min_ts) if tally.min_ts else None,
        "max_timestamp": fmt_ts(tally.max_ts) if tally.max_ts else None,
        "stream_count": len(tally.streams),
        "layout": {
            "order": "layer-major",
            "layer_spans_full_window": True,
            "prefixes_are_byte_prefixes": True,
        },
        "needles": needles,
        "counts": {
            "events": tally.events,
            "dc1": sum_hours(tally.by_hour, "dc1"),
            "error_checkout": sum_hours(tally.by_hour, "error_checkout"),
            "stack": sum_hours(tally.by_hour, "stack"),
            "connection_refused": sum_hours(tally.by_hour, "phrase"),
            "zzq_s5_and_healthcheck": sum_hours(tally.by_hour, "s5_health"),
            "zzq_s5_dc1": sum_hours(tally.by_hour, "zzq_s5_dc1"),
            "zzq_s5_dc1_and_healthcheck": sum_hours(tally.by_hour, "s5_health_dc1"),
        },
        "levels": dict(tally.levels),
        "kinds": dict(tally.kinds),
        "by_hour": [
            {"hour": fmt_ts(START + timedelta(hours=i)), **bucket}
            for i, bucket in enumerate(tally.by_hour)
        ],
        "sample_trace_id": tally.sample_trace_id,
        "sample_stack_trace_id": tally.sample_stack_trace_id,
        "sample_stack_newlines": tally.sample_stack_newlines,
        "sample_stack_body": tally.sample_stack_body,
    }


def byte_prefix(smaller: Path, larger: Path) -> None:
    with smaller.open("rb") as left, larger.open("rb") as right:
        while True:
            chunk = left.read(1 << 20)
            if not chunk:
                return
            other = right.read(len(chunk))
            if chunk != other:
                raise CorpusAssertionError(f"{smaller.name} is not a byte prefix of {larger.name}")


def sha256_first(path: Path, nbytes: int) -> str:
    digest = hashlib.sha256()
    remaining = nbytes
    with path.open("rb") as handle:
        while remaining:
            chunk = handle.read(min(1 << 20, remaining))
            if not chunk:
                break
            digest.update(chunk)
            remaining -= len(chunk)
    if remaining:
        raise CorpusAssertionError(f"{path} is shorter than {nbytes} bytes")
    return digest.hexdigest()


def make_layer(
    start_ordinal: int,
    layer_events: int,
    layer_index: int,
    streams: list[Stream],
    stream_ends: list[int],
    stream_total: int,
    minutes: list[tuple[int, int, int]],
    minute_ends: list[int],
    minute_total: int,
    pay_index: dict[tuple, int],
    rng: random.Random,
) -> list[dict]:
    events: list[dict] = []
    for offset in range(layer_events):
        ordinal = start_ordinal + offset
        day, hour, minute = minutes[pick(minute_ends, minute_total, ordinal)]
        stream = streams[pick(stream_ends, stream_total, ordinal)]
        ts = START + timedelta(
            days=day,
            hours=hour,
            minutes=minute,
            microseconds=ordinal % 1_000_000,
        )
        level_bucket = level_of(ordinal)
        http_status = 500 if level_bucket in ("error", "fatal") else 429 if level_bucket == "warn" else 200
        events.append(
            {
                "ordinal": ordinal,
                "ts": ts,
                "stream": stream,
                "kind": kind_of(ordinal),
                "needles": needles_of(ordinal),
                "decoy": decoy_of(ordinal),
                "phrase": (ordinal + 1) % 250 == 13,
                "healthcheck": (
                    "zzq_s5" in needles_of(ordinal) and ((ordinal + 1) // 100_000) % 2 == 0
                )
                or (ordinal + 1) % 80 == 7,
                "n_attrs": attr_count(ordinal),
                "duration_ms": 1 + (ordinal * 37) % 4000,
                "method": METHODS[ordinal % 4],
                "path": PATHS[ordinal % len(PATHS)],
                "http_status": http_status,
                "rejected": False,
                "trace_id": "",
                "span_id": "",
            }
        )
    # Every layer, hence every prefix, includes the first and last minute.
    if len(events) >= 2:
        for event, minute_index in ((events[0], 0), (events[1], len(minutes) - 1)):
            day, hour, minute = minutes[minute_index]
            event["ts"] = START + timedelta(
                days=day,
                hours=hour,
                minutes=minute,
                microseconds=event["ordinal"] % 1_000_000,
            )
    assign_levels(events)
    for event in events:
        if event["level"] in ("error", "fatal"):
            event["http_status"] = 500
        elif event["level"] == "warn":
            event["http_status"] = 429
        else:
            event["http_status"] = 200
    payment = [event for event in events if event["stream"].app == "payment-api"]
    payment.sort(key=lambda event: event["ordinal"])
    for event in payment:
        ts = event["ts"]
        if INC2_ONSET <= ts < INC2_END:
            key = ("inc", int((ts - INC2_ONSET).total_seconds() // 3600))
            num, den = inc2_rate(key[1])
        else:
            key = ("base", ts.year, ts.month, ts.day, ts.hour)
            num, den = 1, 50
        index = pay_index.get(key, 0)
        event["rejected"] = take_rejected(index, num, den)
        pay_index[key] = index + 1
    # Trace ids in ordinal order so the RNG stream does not depend on time order.
    for event in sorted(events, key=lambda event: event["ordinal"]):
        event["trace_id"] = hex_id(rng, 16)
        event["span_id"] = hex_id(rng, 8)
    events.sort(key=lambda event: (event["ts"], event["ordinal"]))
    return events


def generate(
    out_dir: Path,
    seed: int,
    targets: list[int],
    layer_events: int,
    arm: str,
) -> list[Path]:
    if arm != "baseline":
        raise CorpusAssertionError(
            "the pathological 50,000-stream arm stays with the logwar plan"
        )
    if layer_events < 2 or layer_events % 200 != 0:
        raise CorpusAssertionError("layer-events must be a multiple of 200 and at least 2")
    static_text_ok()
    targets = sorted(set(targets))
    if not targets:
        raise CorpusAssertionError("no size targets")
    out_dir.mkdir(parents=True, exist_ok=True)
    streams = build_streams()
    stream_ends, stream_total = cumulative([27 if stream.hot else 2 for stream in streams])
    if stream_total != 900:
        raise CorpusAssertionError("stream weights must put 60% on the hot 10%")
    minutes, minute_ends, minute_total = build_week()
    rng = random.Random(seed)
    pay_index: dict[tuple, int] = {}
    commit = git_commit()
    strict = targets[-1] >= GB

    handles = {}
    hashers = {}
    tallies = {}
    for target in targets:
        directory = out_dir / label_for(target)
        directory.mkdir(parents=True, exist_ok=True)
        handles[target] = (directory / "events.jsonl").open("wb", buffering=8 << 20)
        hashers[target] = ShardHasher(GB)
        tallies[target] = Tally()

    open_targets = list(targets)
    ordinal = 0
    layer_index = 0
    started = time.monotonic()
    max_target = targets[-1]
    try:
        while open_targets:
            events = make_layer(
                ordinal,
                layer_events,
                layer_index,
                streams,
                stream_ends,
                stream_total,
                minutes,
                minute_ends,
                minute_total,
                pay_index,
                rng,
            )
            rendered: list[tuple[dict, bytes, str]] = []
            for event in events:
                line, body = render(event, rng)
                rendered.append((event, line, body))
            for event, line, body in rendered:
                for target in open_targets:
                    hashers[target].write(line)
                    handles[target].write(line)
                    tallies[target].add(event, body)
            for target in open_targets:
                if hashers[target].filled >= GB:
                    hashers[target].rotate()
            ordinal += layer_events
            layer_index += 1
            still_open = []
            for target in open_targets:
                if hashers[target].total >= target:
                    finalize_target(
                        target,
                        handles,
                        hashers,
                        tallies,
                        out_dir,
                        seed,
                        arm,
                        commit,
                        streams,
                        strict,
                    )
                else:
                    still_open.append(target)
            open_targets = still_open
            if strict and ordinal >= 100_000 and tallies[max_target].events:
                mean = hashers[max_target].total / tallies[max_target].events
                projected = (GB / mean) / 100_000
                if projected < 20:
                    raise CorpusAssertionError(
                        f"mean event is {mean:.1f} bytes; 1 GB would hold under 20 zzq_s5 hits"
                    )
            if layer_index % 20 == 0 or not open_targets:
                elapsed = max(time.monotonic() - started, 0.001)
                written = hashers[max_target].total
                print(
                    f"layers={layer_index} events={ordinal} "
                    f"bytes={written} ({written / GB:.3f} GB) "
                    f"{written / elapsed / 1e6:.1f} MB/s",
                    file=sys.stderr,
                )
    finally:
        for handle in handles.values():
            if not handle.closed:
                handle.close()

    paths = [out_dir / label_for(target) / "events.jsonl" for target in targets]
    largest = paths[-1]
    for path in paths[:-1]:
        byte_prefix(path, largest)
    for target, path in zip(targets, paths):
        digest = sha256_first(path, target)
        manifest_path = path.parent / "manifest.json"
        manifest = json.loads(manifest_path.read_text())
        if manifest["sha256_first_target_bytes"] != digest:
            raise CorpusAssertionError("prefix hash does not match the byte stream")
        if sha256_first(largest, target) != digest:
            raise CorpusAssertionError("size cut is not a prefix of the full stream")
    return paths


def finalize_target(
    target: int,
    handles: dict,
    hashers: dict,
    tallies: dict,
    out_dir: Path,
    seed: int,
    arm: str,
    commit: str,
    streams: list[Stream],
    strict: bool,
) -> None:
    handles[target].flush()
    handles[target].close()
    hashers[target].finish()
    tally: Tally = tallies[target]
    assert_tally(tally, target, strict)
    prefix_sha = sha256_first(out_dir / label_for(target) / "events.jsonl", target)
    manifest = manifest_for(tally, target, hashers[target], prefix_sha, seed, arm, commit)
    incident = incident_document(tally, streams)
    directory = out_dir / label_for(target)
    (directory / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    (directory / "incidents.json").write_text(json.dumps(incident, indent=2) + "\n")
    needles = manifest["needles"]
    print(
        f"closed {label_for(target)}: events={tally.events} bytes={hashers[target].total} "
        f"zzq_s5={needles['zzq_s5']} zzq_s7={needles['zzq_s7']} "
        f"pair={needles['zzq_pa_zzq_pb']} abc={needles['abc_decoy']}",
        file=sys.stderr,
    )


def parse_targets(args: argparse.Namespace) -> list[int]:
    if args.targets_bytes:
        return [int(piece) for piece in args.targets_bytes.split(",") if piece]
    return [size * GB for size in STANDARD_GB if size <= args.max_gb]


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Emit the nested columnar/full-text corpus")
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--seed", type=int, default=20260901)
    parser.add_argument("--arm", default="baseline", choices=("baseline", "pathological"))
    parser.add_argument("--max-gb", type=int, default=20)
    parser.add_argument("--targets-bytes", default="", help="comma-separated exact byte cuts")
    parser.add_argument("--layer-events", type=int, default=8000)
    args = parser.parse_args(argv)
    if args.arm == "pathological":
        print(
            "the pathological 50,000-stream arm stays with the logwar plan; "
            "this generator emits the baseline arm only",
            file=sys.stderr,
        )
        return 2
    try:
        generate(args.out, args.seed, parse_targets(args), args.layer_events, args.arm)
    except CorpusAssertionError as exc:
        print(f"assertion failed: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
