#!/usr/bin/env python3
"""Small-corpus checks for tools/gen-corpus. No docker, no gigabyte files."""

from __future__ import annotations

import json
import subprocess
import sys
import tempfile
import unittest
from datetime import timedelta
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import gen_corpus as g


class NeedleTests(unittest.TestCase):
    def test_positions_do_not_share_an_event(self) -> None:
        for ordinal in range(2_000_000):
            names = g.needles_of(ordinal)
            decoy = g.decoy_of(ordinal)
            self.assertLessEqual(len(names), 2)
            if decoy:
                self.assertEqual(names, [])
            if len(names) == 2:
                self.assertEqual(names, ["zzq_pa", "zzq_pb"])

    def test_residue_counts_match_a_scan(self) -> None:
        events = 250_000
        scanned = {"zzq_s5": 0, "zzq_s7": 0, "zzq_s9": 0, "zzq_pa": 0, "zzq_pb": 0, "zzq_pa_zzq_pb": 0, "abc_decoy": 0}
        for ordinal in range(events):
            names = g.needles_of(ordinal)
            if "zzq_s5" in names:
                scanned["zzq_s5"] += 1
            if "zzq_s7" in names:
                scanned["zzq_s7"] += 1
            if "zzq_s9" in names:
                scanned["zzq_s9"] += 1
            if "zzq_pa" in names:
                scanned["zzq_pa"] += 1
            if "zzq_pb" in names:
                scanned["zzq_pb"] += 1
            if names == ["zzq_pa", "zzq_pb"]:
                scanned["zzq_pa_zzq_pb"] += 1
            if g.decoy_of(ordinal):
                scanned["abc_decoy"] += 1
        self.assertEqual(scanned, g.expected_needles(events))

    def test_inc2_rate_endpoints_and_a_falling_count(self) -> None:
        self.assertEqual(g.inc2_rate(0)[0] / g.inc2_rate(0)[1], 0.02)
        self.assertEqual(g.inc2_rate(47)[0] / g.inc2_rate(47)[1], 0.06)
        hours = []
        for index in range(48):
            num, den = g.inc2_rate(index)
            when = g.INC2_ONSET + timedelta(hours=index)
            total = g.HOUR_WEIGHT[when.hour] * 1000
            hours.append(
                {
                    "intended_rate": num / den,
                    "total": total,
                    "rejected": total * num // den,
                }
            )
        self.assertTrue(g.rate_rises_count_falls(hours))

    def test_static_templates_are_clean(self) -> None:
        g.static_text_ok()
        self.assertEqual(len(g.build_streams()), 200)


class GenerateTests(unittest.TestCase):
    def test_nested_prefixes_and_assertions(self) -> None:
        targets = [300_000, 700_000, 1_200_000, 1_800_000, 2_400_000]
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp)
            paths = g.generate(out, seed=20260901, targets=targets, layer_events=400, arm="baseline")
            self.assertEqual(len(paths), 5)
            largest = paths[-1].read_bytes()
            for target, path in zip(targets, paths):
                data = path.read_bytes()
                self.assertTrue(largest.startswith(data))
                self.assertGreaterEqual(len(data), target)
                manifest = json.loads((path.parent / "manifest.json").read_text())
                incident = json.loads((path.parent / "incidents.json").read_text())
                self.assertEqual(manifest["event_count"], data.count(b"\n"))
                self.assertEqual(manifest["needles"], g.expected_needles(manifest["event_count"]))
                self.assertEqual(manifest["sha256_first_target_bytes"], g.sha256_first(path, target))
                self.assertGreaterEqual(
                    manifest["stream_count"],
                    200 if manifest["event_count"] >= 900 else 1,
                )
                self.assertEqual(len(incident["incidents"]), 1)
                inc = incident["incidents"][0]
                self.assertEqual(inc["id"], "INC-2")
                self.assertEqual(len(inc["hours"]), 48)
                self.assertAlmostEqual(inc["hours"][0]["intended_rate"], 0.02)
                self.assertAlmostEqual(inc["hours"][-1]["intended_rate"], 0.06)
                self.assertGreaterEqual(manifest["sample_stack_newlines"], 19)
                # One physical line per event, and the stack body round-trips.
                found = False
                for line in data.splitlines():
                    event = json.loads(line)
                    self.assertNotIn("\n", line.decode())
                    if event["trace_id"] == manifest["sample_stack_trace_id"]:
                        self.assertEqual(event["body"], manifest["sample_stack_body"])
                        self.assertGreaterEqual(event["body"].count("\n"), 19)
                        found = True
                        break
                self.assertTrue(found)
            # Same seed, same bytes.
            again = Path(tmp) / "again"
            g.generate(again, seed=20260901, targets=targets[:1], layer_events=400, arm="baseline")
            self.assertEqual(paths[0].read_bytes(), (again / "b300000" / "events.jsonl").read_bytes())

    def test_pathological_arm_is_refused(self) -> None:
        script = Path(__file__).resolve().parent / "gen_corpus.py"
        result = subprocess.run(
            [sys.executable, str(script), "--out", "/tmp/unused-corpus", "--arm", "pathological"],
            check=False,
            capture_output=True,
            text=True,
        )
        self.assertEqual(result.returncode, 2)


if __name__ == "__main__":
    unittest.main()
