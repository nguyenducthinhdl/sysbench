"""Tests for the HDFS production schema and instance-IP host rules."""

from __future__ import annotations

import json
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "src"))

from hdfs_service.__main__ import main
from hdfs_service.parse import (
    Service,
    instance_address,
    line_fields,
    load_log,
    load_structure,
    parse_line,
    to_event,
)
from hdfs_service.schema import (
    clickhouse_ddl,
    elasticsearch_template,
    event_schema,
    loki_mapping,
    victorialogs_mapping,
)

DATA = ROOT / "data"
LOG = DATA / "HDFS_2k.log"
STRUCTURE = DATA / "HDFS_2k_structure.txt"


def event(content: str, component: str = "dfs.DataNode$DataXceiver", level: str = "INFO", pid: int = 1):
    line = parse_line(f"081109 203615 {pid} {level} {component}: {content}")
    return to_event(line, Service())


class ParseLineTests(unittest.TestCase):
    def test_parses_structure_columns(self):
        line = parse_line(
            "081109 203615 148 INFO dfs.DataNode$PacketResponder: "
            "PacketResponder 1 for block blk_38865049064139660 terminating"
        )
        self.assertEqual(
            line_fields(line),
            [
                "081109",
                "203615",
                "148",
                "INFO",
                "dfs.DataNode$PacketResponder",
                "PacketResponder 1 for block blk_38865049064139660 terminating",
            ],
        )

    def test_rejects_a_line_that_breaks_the_structure(self):
        with self.assertRaises(ValueError):
            parse_line("not an hdfs line")


class InstanceHostTests(unittest.TestCase):
    def test_self_prefix_is_the_instance_and_to_is_the_peer(self):
        got = event(
            "10.251.30.85:50010:Got exception while serving "
            "blk_-2918118818249673980 to /10.251.90.64:"
        )
        self.assertEqual(got["host"], {"name": "10.251.30.85", "ip": "10.251.30.85"})
        self.assertEqual(got["pod"], "datanode-10-251-30-85")
        self.assertEqual(got["attributes"]["peer_ip"], "10.251.90.64")
        self.assertEqual(got["attributes"]["instance_port"], "50010")
        self.assertEqual(got["block_id"], "blk_-2918118818249673980")
        self.assertEqual(got["level"], "info")

    def test_dest_is_the_instance_when_src_and_dest_differ(self):
        got = event(
            "Receiving block blk_1 src: /10.1.1.1:33145 dest: /10.2.2.2:50010"
        )
        self.assertEqual(got["host"]["ip"], "10.2.2.2")
        self.assertEqual(got["attributes"]["peer_ip"], "10.1.1.1")
        self.assertEqual(got["attributes"]["instance_port"], "50010")

    def test_same_src_and_dest_does_not_invent_a_peer(self):
        got = event(
            "Receiving block blk_5792489080791696128 "
            "src: /10.251.30.6:33145 dest: /10.251.30.6:50010"
        )
        self.assertEqual(got["host"]["ip"], "10.251.30.6")
        self.assertNotIn("peer_ip", got["attributes"])

    def test_blockmap_address_is_the_namenode_recorded_instance(self):
        got = event(
            "BLOCK* NameSystem.addStoredBlock: blockMap updated: "
            "10.251.73.220:50010 is added to blk_7128370237687728475 size 67108864",
            component="dfs.FSNamesystem",
            pid=35,
        )
        self.assertEqual(got["app"], "namenode")
        self.assertEqual(got["host"], {"name": "10.251.73.220", "ip": "10.251.73.220"})
        self.assertEqual(got["pod"], "namenode-10-251-73-220")
        self.assertEqual(got["attributes"]["size"], "67108864")
        self.assertEqual(got["timestamp"], "2008-11-09T20:36:15.000000Z")

    def test_sender_address_is_not_the_host(self):
        got = event(
            "Received block blk_-1 of size 67108864 from /10.251.42.84",
            component="dfs.DataNode$PacketResponder",
        )
        self.assertEqual(got["host"], {"name": ""})
        self.assertNotIn("ip", got["host"])
        self.assertEqual(got["pod"], "datanode-unattributed")
        self.assertEqual(got["attributes"]["peer_ip"], "10.251.42.84")
        self.assertEqual(got["block_id"], "blk_-1")

    def test_line_with_no_address_stays_unattributed(self):
        got = event(
            "PacketResponder 1 for block blk_38865049064139660 terminating",
            component="dfs.DataNode$PacketResponder",
            pid=148,
        )
        self.assertEqual(instance_address(got["body"]), None)
        self.assertEqual(got["host"], {"name": ""})
        self.assertEqual(got["attributes"], {})

    def test_invalid_leading_address_is_not_used_as_the_host(self):
        got = event("999.1.1.1:50010:Got exception while serving blk_1 to /10.1.2.3:")
        self.assertEqual(got["host"], {"name": ""})
        self.assertEqual(got["attributes"]["peer_ip"], "10.1.2.3")

    def test_self_prefix_wins_over_dest(self):
        found = instance_address(
            "10.0.0.1:50010:Receiving block blk_1 src: /10.0.0.2:1 dest: /10.0.0.3:50010"
        )
        self.assertEqual(found, ("10.0.0.1", "50010"))

    def test_service_identity_is_configurable(self):
        line = parse_line(
            "081109 203615 1 INFO dfs.DataNode: PacketResponder 0 for block blk_1 terminating"
        )
        got = to_event(line, Service(cluster="lab", namespace="storage"))
        self.assertEqual(got["cluster"], "lab")
        self.assertEqual(got["namespace"], "storage")
        self.assertEqual(got["container"], "hdfs")


class LoadTests(unittest.TestCase):
    def test_structure_example_matches_the_sample(self):
        example = load_structure(STRUCTURE)
        lines = load_log(LOG)
        self.assertIn(example, (line_fields(line) for line in lines))
        self.assertEqual(len(lines), 2000)

    def test_structure_header_is_the_contract(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "structure.txt"
            path.write_text("Date\tTime\n081109\t203615\n", encoding="utf-8")
            with self.assertRaises(ValueError) as caught:
                load_structure(path)
        self.assertIn("does not match", str(caught.exception))

    def test_structure_requires_an_example_row(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "structure.txt"
            path.write_text("Date\tTime\tPid\tLevel\tComponent\tContent\n", encoding="utf-8")
            with self.assertRaises(ValueError) as caught:
                load_structure(path)
        self.assertIn("example row", str(caught.exception))

    def test_load_log_reports_the_bad_line(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "bad.log"
            path.write_text(
                "081109 203615 1 INFO dfs.DataNode: ok\n"
                "this line is broken\n",
                encoding="utf-8",
            )
            with self.assertRaises(ValueError) as caught:
                load_log(path)
        self.assertIn("1 lines failed", str(caught.exception))
        self.assertIn("bad.log:2", str(caught.exception))

    def test_load_log_rejects_an_empty_file(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "empty.log"
            path.write_text("\n", encoding="utf-8")
            with self.assertRaises(ValueError) as caught:
                load_log(path)
        self.assertIn("no log lines", str(caught.exception))


class SchemaTests(unittest.TestCase):
    def test_clickhouse_sorts_instance_host_then_time(self):
        ddl = clickhouse_ddl()
        self.assertIn("HostName      LowCardinality(String)", ddl)
        self.assertIn("HostIp        Nullable(IPv4)", ddl)
        self.assertIn("ORDER BY (HostName, Timestamp)", ddl)
        self.assertIn("PARTITION BY toDate(Timestamp)", ddl)
        self.assertIn("TYPE text(tokenizer = 'splitByNonAlpha')", ddl)

    def test_elasticsearch_sorts_host_name_then_timestamp(self):
        template = elasticsearch_template()["index_template"]
        settings = template["template"]["settings"]
        host = template["template"]["mappings"]["properties"]["host"]["properties"]
        self.assertEqual(settings["index.mode"], "logsdb")
        self.assertEqual(settings["index.sort.field"], ["host.name", "@timestamp"])
        self.assertEqual(settings["index.sort.order"], ["asc", "desc"])
        self.assertEqual(host["name"]["type"], "keyword")
        self.assertEqual(host["ip"]["type"], "ip")

    def test_event_schema_requires_host(self):
        schema = event_schema()
        self.assertIn("host", schema["required"])
        self.assertEqual(schema["properties"]["host"]["required"], ["name"])
        self.assertIn("ip", schema["properties"]["host"]["properties"])

    def test_loki_keeps_the_instance_ip_out_of_labels(self):
        labels = loki_mapping()["stream_labels"]
        self.assertEqual(labels, victorialogs_mapping()["stream_fields"])
        self.assertNotIn("host.name", labels)
        self.assertIn("host.name", victorialogs_mapping()["fields"])


class CreateSchemaTests(unittest.TestCase):
    def test_writes_schema_and_events(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp)
            code = main(["--log", str(LOG), "--structure", str(STRUCTURE), "--out", str(out)])
            self.assertEqual(code, 0)
            event_row = json.loads((out / "hdfs_2k.jsonl").read_text(encoding="utf-8").splitlines()[2])
            self.assertEqual(event_row["host"]["ip"], "10.251.73.220")
            self.assertIn("ORDER BY (HostName, Timestamp)", (out / "clickhouse.sql").read_text())
            written = json.loads((out / "event.schema.json").read_text(encoding="utf-8"))
            self.assertEqual(written["required"], event_schema()["required"])

    def test_returns_failure_when_the_example_is_not_in_the_log(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            log = root / "tiny.log"
            structure = root / "structure.txt"
            log.write_text(
                "081109 203615 1 INFO dfs.DataNode: PacketResponder 0 for block blk_1 terminating\n",
                encoding="utf-8",
            )
            structure.write_text(
                "Date\tTime\tPid\tLevel\tComponent\tContent\n"
                "081109\t203615\t9\tINFO\tdfs.DataNode\tother\n",
                encoding="utf-8",
            )
            code = main(
                ["--log", str(log), "--structure", str(structure), "--out", str(root / "out")]
            )
        self.assertEqual(code, 1)


if __name__ == "__main__":
    unittest.main()
