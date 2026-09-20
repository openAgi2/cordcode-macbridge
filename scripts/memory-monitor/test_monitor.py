#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""monitor.py 测试（followups v9 §2.3.3 测试矩阵）。

分层：parser fixture（真实归档输出脱敏）与纯算法 fixture（epoch/阈值/counter
schema）分开；调度/恢复用 fake clock + tmp 数据根；锁与 dirty-run 用真实子进程
（含 kill -9）。
运行：/usr/bin/python3 -m unittest discover -s scripts/memory-monitor -p 'test_monitor.py' -v
"""

import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import monitor  # noqa: E402

MONITOR_PY = os.path.join(os.path.dirname(os.path.abspath(__file__)), "monitor.py")


def make_runtime_files(root, url="http://127.0.0.1:1", pid=41028,
                       epoch="89ea99d5-ac27-4442-b46c-ee2880d4ed2f",
                       token="SECRET-TOKEN-XYZ"):
    runtime_path = os.path.join(root, "runtime.json")
    token_path = os.path.join(root, "management-token")
    with open(runtime_path, "w") as handle:
        json.dump({"type": "runtime_ready", "port": 8777, "bridgeEpoch": epoch,
                   "drivers": ["claude"], "managementUrl": url, "pid": pid}, handle)
    with open(token_path, "w") as handle:
        handle.write(token + "\n")
    return runtime_path, token_path


def valid_diag_payload(overrides=None):
    payload = {
        "startedAt": "2026-09-20T03:19:24Z",
        "memory": {
            "sys": 100 << 20, "heapSys": 80 << 20, "heapInuse": 50 << 20,
            "heapIdle": 30 << 20, "heapReleased": 10 << 20,
            "sysMinusHeapReleased": 90 << 20, "heapObjects": 1000,
            "stackSys": 1 << 20, "numGC": 5,
        },
        "processUserCPUSeconds": 12.5,
        "processSystemCPUSeconds": 3.5,
        "processCPUAvailable": True,
        "agentBackgroundScans:codex": {
            "scans": 3, "successes": 3, "failures": 0,
            "turnItemRequests": 10, "scannedTurns": 40},
    }
    if overrides:
        for key, value in overrides.items():
            if value is None:
                payload.pop(key, None)
            else:
                payload[key] = value
    return payload


# ---------------------------------------------------------------------------
# 纯算法 fixture：epoch 转换（活体 + 固定 + 零值分支）
# ---------------------------------------------------------------------------

class EpochTests(unittest.TestCase):
    def test_live_fixture(self):
        # 活体 fixture（§2.3.1）：真实 runtime UUID → 派生 uint64
        self.assertEqual(
            monitor.derive_epoch("68fef32a-ef11-4c08-9392-bc1b7e32712e"),
            2011574066258607221)

    def test_fixed_fixture_deterministic(self):
        value = monitor.derive_epoch("89ea99d5-ac27-4442-b46c-ee2880d4ed2f")
        self.assertIsInstance(value, int)
        self.assertGreater(value, 0)
        self.assertEqual(value, monitor.derive_epoch(
            "89ea99d5-ac27-4442-b46c-ee2880d4ed2f"))

    def test_zero_digest_maps_to_one(self):
        # hash 桩全零前 8 字节 → 1（零值保护分支）
        class ZeroDigest:
            def digest(self):
                return b"\x00" * 32

        with mock.patch.object(monitor.hashlib, "sha256",
                               return_value=ZeroDigest()):
            self.assertEqual(monitor.derive_epoch("any-uuid"), 1)


# ---------------------------------------------------------------------------
# URL 严格校验 fixture（§2.3.1）
# ---------------------------------------------------------------------------

class UrlValidationTests(unittest.TestCase):
    def test_valid_loopback(self):
        self.assertIsNone(monitor.validate_management_url("http://127.0.0.1:58425"))
        self.assertIsNone(monitor.validate_management_url("http://[::1]:58425"))
        self.assertIsNone(monitor.validate_management_url("http://127.0.0.1:1/"))

    def test_rejections(self):
        cases = {
            "https://127.0.0.1:58425": "scheme_not_http",
            "http://user:pass@127.0.0.1:58425": "userinfo_present",
            "http://example.com:58425": "host_not_loopback",
            "http://localhost:58425": "host_not_loopback",
            "http://169.254.169.254:80": "host_not_loopback",
            "http://127.0.0.1:58425/internal": "path_not_root",
            "http://127.0.0.1:58425?x=1": "query_or_fragment",
            "http://127.0.0.1:58425#frag": "query_or_fragment",
            "http://127.0.0.1:notaport": "port_invalid",
            "http://127.0.0.1:": "port_missing",
            "http://127.0.0.1:0": "port_out_of_range",
            # 65536：urlsplit 解析阶段即抛 ValueError（与非法端口同分支）
            "http://127.0.0.1:65536": "port_invalid",
        }
        for url, expected in cases.items():
            self.assertEqual(monitor.validate_management_url(url), expected,
                             "URL %s 应判 %s" % (url, expected))


# ---------------------------------------------------------------------------
# vmmap / ps parser fixture（真实归档输出脱敏，2026-09-20 本机 vmmap --summary）
# ---------------------------------------------------------------------------

REAL_VMMAP_FRAGMENT = """
ReadOnly portion of Libraries: Total=291.2M resident=36.7M swapped=0K or=0K
Writable regions: Total=1.1G written=64.7M resident=64.7M swapped=0K or=0K

__DATA_CONST                         37.0M    10.1M       0K       0K       0K       0K       0K     1058
__TEXT                                1.4G   216.6M       0K       0K       0K       0K       0K     1146
mapped file                         377.5M    1584K       0K       0K       0K       0K       0K       52
===========                        ======= ========    =====  ======= ========   ======    =====  =======
TOTAL                               388.9G   305.0M    20.1M    28.0M       0K    1824K     384K     6845

REGION TYPE                      VIRTUAL   RESIDENT     DIRTY    SWAPPED ALLOCATION      BYTES DIRTY+SWAP  REGION COUNT
MALLOC ZONE                          SIZE       SIZE       SIZE       SIZE      COUNT  ALLOCATED  FRAG SIZE  % FRAG   COUNT
DefaultMallocZone_0x10134c000       75.0M      13.9M      13.9M      16.2M     125854      20.7M      9632K     32%      91
"""

REAL_VMMAP_FOOTPRINT = """Physical footprint:         48.0M
Physical footprint (peak):  82.3M
"""


class VmmapParserTests(unittest.TestCase):
    def test_real_archived_output(self):
        metrics = monitor.parse_vmmap_summary(
            REAL_VMMAP_FOOTPRINT + REAL_VMMAP_FRAGMENT)
        self.assertEqual(metrics["footprintCurrent"], int(48.0 * 1024 * 1024))
        self.assertEqual(metrics["footprintPeak"], round(82.3 * 1024 * 1024))
        self.assertIsNone(metrics["swapped"], "无 TOTAL SWAPPED 行 → unavailable")

    def test_with_swapped_line(self):
        output = ("Physical footprint:         1.5G\n"
                  "Physical footprint (peak):  2.0G\n"
                  "TOTAL SWAPPED:              512K\n")
        metrics = monitor.parse_vmmap_summary(output)
        self.assertEqual(metrics["footprintCurrent"],
                         int(1.5 * 1024 * 1024 * 1024))
        self.assertEqual(metrics["footprintPeak"],
                         int(2.0 * 1024 * 1024 * 1024))
        self.assertEqual(metrics["swapped"], 512 * 1024)

    def test_decimal_and_space_variations(self):
        output = ("Physical footprint:12.5M\n"
                  "Physical footprint (peak):\t1024K\n")
        metrics = monitor.parse_vmmap_summary(output)
        self.assertEqual(metrics["footprintCurrent"], int(12.5 * 1024 * 1024))
        self.assertEqual(metrics["footprintPeak"], 1024 * 1024)

    def test_duplicate_footprint_lines_unavailable(self):
        output = ("Physical footprint:         48.0M\n"
                  "Physical footprint:         50.0M\n"
                  "Physical footprint (peak):  82.3M\n")
        metrics = monitor.parse_vmmap_summary(output)
        self.assertIsNone(metrics["footprintCurrent"], "重复行 → unavailable")
        self.assertEqual(metrics["footprintPeak"], round(82.3 * 1024 * 1024))

    def test_unknown_unit_and_non_numeric_unavailable(self):
        output = ("Physical footprint:         48.0X\n"
                  "Physical footprint (peak):  abc\n")
        metrics = monitor.parse_vmmap_summary(output)
        self.assertIsNone(metrics["footprintCurrent"])
        self.assertIsNone(metrics["footprintPeak"])

    def test_bare_bytes_value(self):
        metrics = monitor.parse_vmmap_summary("Physical footprint:  2352\n")
        self.assertEqual(metrics["footprintCurrent"], 2352)


class PsRssTests(unittest.TestCase):
    def test_real_shape(self):
        # 真实形状：ps -o rss= 无表头，KB
        self.assertEqual(monitor.parse_ps_rss(" 35344\n"), 35344 * 1024)

    def test_invalid(self):
        self.assertIsNone(monitor.parse_ps_rss(""))
        self.assertIsNone(monitor.parse_ps_rss("RSS\n"))
        self.assertIsNone(monitor.parse_ps_rss("12.5"))


# ---------------------------------------------------------------------------
# payload / counter schema fixture（§2.3.2 R7-N1）
# ---------------------------------------------------------------------------

class PayloadValidationTests(unittest.TestCase):
    def test_valid_payload(self):
        diag = monitor.validate_diagnostics_payload(valid_diag_payload())
        self.assertEqual(diag["startedAt"], "2026-09-20T03:19:24Z")
        self.assertEqual(diag["memory"]["sysMinusHeapReleased"], 90 << 20)
        self.assertTrue(diag["cpu"]["available"])
        self.assertEqual(diag["scanCounters"]["codex"]["scans"], 3)

    def test_missing_or_wrong_type_fields_rejected(self):
        for field in ("startedAt", "memory", "processUserCPUSeconds",
                      "processSystemCPUSeconds", "processCPUAvailable"):
            with self.subTest(field=field):
                with self.assertRaises(monitor.MonitorError) as ctx:
                    monitor.validate_diagnostics_payload(
                        valid_diag_payload({field: None}))
                self.assertEqual(ctx.exception.args[0], "payload_invalid")
        # memory 子字段缺失/类型错
        for field in monitor.DIAG_MEMORY_FIELDS:
            payload = valid_diag_payload()
            payload["memory"] = dict(payload["memory"])
            del payload["memory"][field]
            with self.subTest(memory_field=field):
                with self.assertRaises(monitor.MonitorError):
                    monitor.validate_diagnostics_payload(payload)
        payload = valid_diag_payload()
        payload["memory"]["sys"] = "100"
        with self.assertRaises(monitor.MonitorError):
            monitor.validate_diagnostics_payload(payload)
        payload = valid_diag_payload()
        payload["processCPUAvailable"] = "yes"
        with self.assertRaises(monitor.MonitorError):
            monitor.validate_diagnostics_payload(payload)

    def test_cpu_unavailable_flags_but_sample_valid(self):
        payload = valid_diag_payload()
        payload["processCPUAvailable"] = False
        diag = monitor.validate_diagnostics_payload(payload)
        self.assertFalse(diag["cpu"]["available"])

    def test_counter_schema(self):
        # 缺整个对象 = 无证据（不是错误）
        payload = valid_diag_payload()
        del payload["agentBackgroundScans:codex"]
        diag = monitor.validate_diagnostics_payload(payload)
        self.assertEqual(diag["scanCounters"], {})

        # 字段缺失 → 该 backend counter unavailable
        payload = valid_diag_payload()
        payload["agentBackgroundScans:codex"] = {"scans": 3}
        diag = monitor.validate_diagnostics_payload(payload)
        self.assertTrue(diag["scanCounters"]["codex"]["unavailable"])

        # 类型错（bool/负数/字符串）→ unavailable
        for bad in ({"scans": True}, {"scans": -1}, {"scans": "3"}):
            payload = valid_diag_payload()
            payload["agentBackgroundScans:codex"] = bad
            diag = monitor.validate_diagnostics_payload(payload)
            self.assertTrue(diag["scanCounters"]["codex"]["unavailable"])

        # 非 map → unavailable
        payload = valid_diag_payload()
        payload["agentBackgroundScans:codex"] = 42
        diag = monitor.validate_diagnostics_payload(payload)
        self.assertTrue(diag["scanCounters"]["codex"]["unavailable"])


def sample_with(sys_released, slot=0, footprint_current=None,
                footprint_peak=None, scans_first=0, scans_last=0):
    first = {"memory": {"sysMinusHeapReleased": sys_released},
             "cpu": {"userSeconds": 0, "systemSeconds": 0, "available": True},
             "scanCounters": {"codex": {"scans": scans_first,
                                        "turnItemRequests": 0,
                                        "scannedTurns": 0}}}
    last = {"memory": {"sysMinusHeapReleased": sys_released},
            "cpu": {"userSeconds": 0, "systemSeconds": 0, "available": True},
            "scanCounters": {"codex": {"scans": scans_last,
                                       "turnItemRequests": 0,
                                       "scannedTurns": 0}}}
    sample = dict(first)
    sample["slot"] = slot
    sample["footprint"] = {"footprintCurrent": footprint_current,
                           "footprintPeak": footprint_peak}
    return first, last, sample


class TierTests(unittest.TestCase):
    def test_scan_evidenced(self):
        first, last, _ = sample_with(0, scans_first=3, scans_last=5)
        self.assertEqual(monitor.classify_generation_tier(first, last),
                         "scan-evidenced")

    def test_counter_regression_not_evidence(self):
        first, last, _ = sample_with(0, scans_first=5, scans_last=3)
        self.assertEqual(monitor.classify_generation_tier(first, last),
                         "idle/low-activity")

    def test_cpu_active_only(self):
        first, last, _ = sample_with(0)
        first["cpu"] = {"userSeconds": 0, "systemSeconds": 0, "available": True}
        last["cpu"] = {"userSeconds": 40, "systemSeconds": 25, "available": True}
        self.assertEqual(monitor.classify_generation_tier(first, last),
                         "cpu-active-only")

    def test_cpu_unavailable_falls_back(self):
        first, last, _ = sample_with(0)
        first["cpu"]["available"] = False
        last["cpu"] = {"userSeconds": 100, "systemSeconds": 100,
                       "available": True}
        self.assertEqual(monitor.classify_generation_tier(first, last),
                         "idle/low-activity")

    def test_multi_backend_mixed_evidence(self):
        first = {"scanCounters": {
            "codex": {"unavailable": True},
            "grok": {"scans": 1, "turnItemRequests": 0, "scannedTurns": 0}}}
        last = {"scanCounters": {
            "codex": {"unavailable": True},
            "grok": {"scans": 2, "turnItemRequests": 0, "scannedTurns": 0}}}
        self.assertTrue(monitor.scan_evidenced(first, last))


# ---------------------------------------------------------------------------
# 告警边界 fixture（§2.3.5 ③边界）
# ---------------------------------------------------------------------------

def trend_samples(values):
    samples = []
    for i, value in enumerate(values):
        _, _, sample = sample_with(value, slot=i)
        samples.append(sample)
    return samples


class AlertBoundaryTests(unittest.TestCase):
    MI = 8 * 1024 * 1024

    def test_trend_delta_exactly_threshold_triggers(self):
        values = [0, self.MI, 2 * self.MI, 3 * self.MI]
        self.assertIn("trend", monitor.evaluate_alerts(trend_samples(values)))

    def test_trend_delta_one_byte_below_not_triggers(self):
        values = [0, self.MI - 1, 2 * self.MI - 2, 3 * self.MI - 3]
        self.assertNotIn("trend", monitor.evaluate_alerts(trend_samples(values)))

    def test_trend_non_strictly_rising_not_triggers(self):
        values = [0, self.MI, self.MI, 3 * self.MI]
        self.assertNotIn("trend", monitor.evaluate_alerts(trend_samples(values)))
        values = [0, self.MI, self.MI + 1, self.MI]
        self.assertNotIn("trend", monitor.evaluate_alerts(trend_samples(values)))

    def test_trend_insufficient_steps_not_triggers(self):
        values = [0, self.MI, 2 * self.MI]
        self.assertNotIn("trend", monitor.evaluate_alerts(trend_samples(values)))

    def test_trend_non_consecutive_slots_not_triggers(self):
        values = [0, self.MI, 2 * self.MI, 3 * self.MI]
        samples = trend_samples(values)
        samples[2]["slot"] = 4   # slot 序列断裂（0,1,4,3 → 排序后 0,1,3,4）
        self.assertNotIn("trend", monitor.evaluate_alerts(samples))

    def test_absolute_thresholds(self):
        _, _, sample = sample_with(monitor.ALERT_SYS_MINUS_RELEASED_B)
        self.assertNotIn("absolute_sys_minus_released",
                         monitor.evaluate_alerts([sample]))
        _, _, sample = sample_with(monitor.ALERT_SYS_MINUS_RELEASED_B + 1)
        self.assertIn("absolute_sys_minus_released",
                      monitor.evaluate_alerts([sample]))

    def test_footprint_thresholds_current_and_peak(self):
        _, _, sample = sample_with(
            0, footprint_current=monitor.ALERT_FOOTPRINT_B)
        self.assertNotIn("footprint_peak", monitor.evaluate_alerts([sample]))
        _, _, sample = sample_with(
            0, footprint_current=monitor.ALERT_FOOTPRINT_B + 1)
        self.assertIn("footprint_peak", monitor.evaluate_alerts([sample]))
        _, _, sample = sample_with(
            0, footprint_peak=monitor.ALERT_FOOTPRINT_B + 1)
        self.assertIn("footprint_peak", monitor.evaluate_alerts([sample]))
        # unavailable 指标不触发
        _, _, sample = sample_with(0, footprint_current=None,
                                   footprint_peak=None)
        self.assertNotIn("footprint_peak", monitor.evaluate_alerts([sample]))


# ---------------------------------------------------------------------------
# slot 边界（§2.3.3）
# ---------------------------------------------------------------------------

class SlotTests(unittest.TestCase):
    def test_boundaries(self):
        started = 1000.0
        self.assertEqual(monitor.slot_for_timestamp(started, 1000.0), 0)
        self.assertEqual(monitor.slot_for_timestamp(
            started, 1000.0 + monitor.SLOT_WINDOW_S - 1), 0)
        self.assertIsNone(monitor.slot_for_timestamp(
            started, 1000.0 + monitor.SLOT_WINDOW_S))
        self.assertEqual(monitor.slot_for_timestamp(
            started, 1000.0 + monitor.SLOT_SPACING_S), 1)
        self.assertEqual(monitor.slot_for_timestamp(
            started,
            1000.0 + monitor.SLOT_SPACING_S * 3 + monitor.SLOT_WINDOW_S - 1), 3)
        self.assertIsNone(monitor.slot_for_timestamp(
            started,
            1000.0 + monitor.SLOT_SPACING_S * 3 + monitor.SLOT_WINDOW_S))
        self.assertIsNone(monitor.slot_for_timestamp(started, 999.0))


class CompletionTests(unittest.TestCase):
    def test_gates(self):
        self.assertFalse(monitor.completion_satisfied(0, 0, 0, 0))
        self.assertFalse(monitor.completion_satisfied(
            monitor.COMPLETION_ELAPSED_S, 7, 20, 7))
        self.assertFalse(monitor.completion_satisfied(
            monitor.COMPLETION_ELAPSED_S - 1, 7, 20, 8))
        self.assertFalse(monitor.completion_satisfied(
            monitor.COMPLETION_ELAPSED_S, 6, 20, 8))
        self.assertFalse(monitor.completion_satisfied(
            monitor.COMPLETION_ELAPSED_S, 7, 19, 8))
        self.assertTrue(monitor.completion_satisfied(
            monitor.COMPLETION_ELAPSED_S, 7, 20, 8))


# ---------------------------------------------------------------------------
# 六步事务（monkeypatch http；真实 tmp bootstrap 文件）
# ---------------------------------------------------------------------------

class TransactionTests(unittest.TestCase):
    def setUp(self):
        self.root = tempfile.mkdtemp()
        self.runtime_path, self.token_path = make_runtime_files(self.root)
        self.epoch = monitor.derive_epoch("89ea99d5-ac27-4442-b46c-ee2880d4ed2f")
        self.status_payload = {"runtimeIdentity": {"pid": 41028,
                                                   "bridgeEpoch": self.epoch}}
        self.vmmap_output = REAL_VMMAP_FOOTPRINT
        self.addCleanup(shutil.rmtree, self.root, True)

    def fake_http(self, status_sequence, diag_payload=None):
        """按调用序返回 /internal/status 与 /internal/diagnostics 的载荷。"""
        calls = {"n": 0}

        def _get(url, token, timeout=monitor.HTTP_TIMEOUT_S):
            calls["n"] += 1
            if "/internal/status" in url:
                payload = status_sequence[calls["n"]
                                          ] if isinstance(status_sequence, dict) \
                    else status_sequence[min(calls["n"] - 1,
                                             len(status_sequence) - 1)]
                return 200, payload
            return 200, diag_payload if diag_payload is not None \
                else valid_diag_payload()

        return _get

    def fake_commands(self, vmmap_output=None, ps_rss=" 35344\n",
                      vmmap_rc=0, side_effect=None):
        def _run(argv, timeout=monitor.COMMAND_TIMEOUT_S):
            if side_effect is not None:
                result = side_effect(argv)
                if result is not None:
                    return result
            if argv[0] == "vmmap":
                return vmmap_rc, vmmap_output or self.vmmap_output
            if argv[0] == "ps":
                return 0, ps_rss
            if argv[0] == "sysctl":
                return 0, "vm.loadavg: { 5.43 6.37 5.72 }"
            return 0, ""

        return _run

    def test_valid_sample(self):
        with mock.patch.object(monitor, "http_get_json",
                               self.fake_http([self.status_payload,
                                               self.status_payload])):
            result, started_at = monitor.run_transaction(
                self.runtime_path, self.token_path,
                command_runner=self.fake_commands())
        self.assertTrue(result.valid, result.reason)
        self.assertEqual(started_at, "2026-09-20T03:19:24Z")
        sample = result.sample
        self.assertEqual(sample["pid"], 41028)
        self.assertEqual(sample["epoch"], self.epoch)
        self.assertEqual(sample["footprint"]["footprintCurrent"],
                         int(48.0 * 1024 * 1024))
        self.assertEqual(sample["rss"], 35344 * 1024)
        self.assertEqual(sample["memory"]["sysMinusHeapReleased"], 90 << 20)

    def test_bootstrap_mismatch_after_one_reread(self):
        wrong_identity = {"runtimeIdentity": {"pid": 999,
                                              "bridgeEpoch": self.epoch}}
        with mock.patch.object(monitor, "http_get_json",
                               self.fake_http([wrong_identity,
                                               wrong_identity])):
            result, _ = monitor.run_transaction(
                self.runtime_path, self.token_path,
                command_runner=self.fake_commands())
        self.assertFalse(result.valid)
        self.assertEqual(result.reason, "bootstrap_mismatch")

    def test_identity_changed_between_a_and_b(self):
        other = {"runtimeIdentity": {"pid": 41028, "bridgeEpoch": self.epoch + 1}}
        with mock.patch.object(monitor, "http_get_json",
                               self.fake_http([self.status_payload, other])):
            result, _ = monitor.run_transaction(
                self.runtime_path, self.token_path,
                command_runner=self.fake_commands())
        self.assertFalse(result.valid)
        self.assertEqual(result.reason, "identity_changed")

    def test_bootstrap_rewritten_mid_transaction(self):
        def rewrite_token(argv, timeout=monitor.COMMAND_TIMEOUT_S):
            if argv[0] == "vmmap":
                with open(self.token_path, "w") as handle:
                    handle.write("ROTATED-TOKEN\n")
            return None

        commands = self.fake_commands(side_effect=rewrite_token)
        with mock.patch.object(monitor, "http_get_json",
                               self.fake_http([self.status_payload,
                                               self.status_payload])):
            result, _ = monitor.run_transaction(
                self.runtime_path, self.token_path,
                command_runner=commands)
        self.assertFalse(result.valid)
        self.assertEqual(result.reason, "bootstrap_rewritten")

    def test_started_at_mismatch(self):
        payload = valid_diag_payload()
        payload["startedAt"] = "2026-09-19T00:00:00Z"
        with mock.patch.object(monitor, "http_get_json",
                               self.fake_http([self.status_payload,
                                               self.status_payload],
                                              diag_payload=payload)):
            result, _ = monitor.run_transaction(
                self.runtime_path, self.token_path,
                recorded_started_at="2026-09-20T03:19:24Z",
                command_runner=self.fake_commands())
        self.assertFalse(result.valid)
        self.assertEqual(result.reason, "started_at_mismatch")

    def test_command_failure_rejects_whole_transaction(self):
        with mock.patch.object(monitor, "http_get_json",
                               self.fake_http([self.status_payload,
                                               self.status_payload])):
            result, _ = monitor.run_transaction(
                self.runtime_path, self.token_path,
                command_runner=self.fake_commands(vmmap_rc=1))
        self.assertFalse(result.valid)
        self.assertEqual(result.reason, "command_failed:vmmap")

    def test_auth_rejected_and_redirect_rejected(self):
        for reason in ("auth_rejected", "redirect_rejected"):
            with self.subTest(reason=reason):
                def _raise(url, token, timeout=monitor.HTTP_TIMEOUT_S):
                    raise monitor.MonitorError(reason)
                with mock.patch.object(monitor, "http_get_json", _raise):
                    result, _ = monitor.run_transaction(
                        self.runtime_path, self.token_path,
                        command_runner=self.fake_commands())
                self.assertFalse(result.valid)
                self.assertEqual(result.reason, reason)

    def test_payload_invalid(self):
        payload = valid_diag_payload()
        del payload["memory"]
        with mock.patch.object(monitor, "http_get_json",
                               self.fake_http([self.status_payload,
                                               self.status_payload],
                                              diag_payload=payload)):
            result, _ = monitor.run_transaction(
                self.runtime_path, self.token_path,
                command_runner=self.fake_commands())
        self.assertFalse(result.valid)
        self.assertEqual(result.reason, "payload_invalid")


# ---------------------------------------------------------------------------
# journal crash-recovery（§2.3.3 四个 crash point）
# ---------------------------------------------------------------------------

class JournalCrashPointTests(unittest.TestCase):
    def setUp(self):
        self.root = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.root, True)
        self.journal = monitor.Journal(self.root)
        self.journal.open_for_append()
        self.sample_record = {
            "recordKind": "sample", "pid": 41028, "epoch": 12345, "slot": 0,
            "sample": {"startedAt": "2026-09-20T03:19:24Z",
                       "memory": {"sysMinusHeapReleased": 1}},
        }
        self.stop_record = {
            "recordKind": "stop", "pid": 0, "epoch": 0, "slot": -1,
            "reason": "alert", "alerts": ["trend"],
        }

    def make_monitor(self):
        mon = monitor.Monitor(self.root)
        mon.journal = self.journal
        return mon

    def test_crash_point_1_before_append_state_only(self):
        # ①append 前：state 声称有样本但 JSONL 没有 → JSONL 是真相 → 不恢复
        state = {"generations": {"41028:12345": {
            "startedAt": "x", "slots": {"0": self.sample_record["sample"]}}}}
        self.journal.write_state(state)
        mon = self.make_monitor()
        mon.recover_from_journal()
        self.assertNotIn("41028:12345", mon.state["generations"],
                         "state 不得覆盖日志：JSONL 无该事实则不存在")

    def test_crash_point_2_after_append_before_state(self):
        # ②append 后 / state 前：JSONL 有、state 陈旧 → 重放恢复，恰好一个
        self.journal.append_record(self.sample_record)
        self.journal.write_state({"generations": {}})
        mon = self.make_monitor()
        mon.recover_from_journal()
        gen = mon.state["generations"]["41028:12345"]
        self.assertEqual(len(gen["slots"]), 1)
        self.assertIn("0", gen["slots"])

    def test_crash_point_3_tmp_leftover_before_rename(self):
        # ③state temp 写后 / rename 前：tmp 残留不得让下一次写入失败
        self.journal.append_record(self.sample_record)
        self.journal.write_state({"generations": {}})
        # 模拟 crash 留下 tmp（内容已写但未 rename）
        tmp_path = self.journal.state_path + ".tmp"
        with open(tmp_path, "w") as handle:
            handle.write("{partial")
        mon = self.make_monitor()
        mon.recover_from_journal()
        gen = mon.state["generations"]["41028:12345"]
        self.assertEqual(len(gen["slots"]), 1)
        # 下一次原子写必须成功（tmp 被清理）
        mon.journal.write_state({"probe": True})
        self.assertFalse(os.path.exists(tmp_path))
        with open(self.journal.state_path) as handle:
            self.assertEqual(json.load(handle), {"probe": True})

    def test_crash_point_4_after_rename(self):
        # ④rename 后：JSONL 与 state 一致 → 恢复后恰好一个有效事实
        self.journal.append_record(self.sample_record)
        self.journal.write_state({"generations": {"41028:12345": {
            "startedAt": "x", "slots": {"0": self.sample_record["sample"]}}}})
        mon = self.make_monitor()
        mon.recover_from_journal()
        gen = mon.state["generations"]["41028:12345"]
        self.assertEqual(len(gen["slots"]), 1)

    def test_partial_tail_truncated_and_counted(self):
        # partial tail（无结尾换行）→ 截断 + corrupt_line 计数
        self.journal.append_record(self.sample_record)
        with open(self.journal.jsonl_path, "a") as handle:
            handle.write('{"recordKind": "samp')   # 无换行的残行
        mon = self.make_monitor()
        records, counts = mon.journal.recover()
        self.assertEqual(counts["corrupt_line"], 1)
        self.assertEqual(len(records), 1)

    def test_exact_duplicate_lines_deduped(self):
        line = json.dumps(self.sample_record, ensure_ascii=False,
                          sort_keys=True) + "\n"
        with open(self.journal.jsonl_path, "w") as handle:
            handle.write(line)
            handle.write(line)
        records, counts = self.journal.recover()
        self.assertEqual(len(records), 1)
        self.assertEqual(counts["duplicate"], 1)

    def test_alert_stop_not_lost_after_recovery(self):
        self.journal.append_record(self.stop_record)
        mon = self.make_monitor()
        mon.recover_from_journal()
        self.assertTrue(mon.state["alertStop"])

    def test_restart_mid_generation_restores_slot_scheduling(self):
        # 回归：journal 重放必须从嵌套 sample 恢复 startedAt/startedAtTs，
        # 否则重启后该代际后续 slot 事务全部误判 out_of_slot。
        self.journal.append_record(self.sample_record)
        mon = self.make_monitor()
        mon.recover_from_journal()
        gen = mon.state["generations"]["41028:12345"]
        self.assertEqual(gen["startedAt"], "2026-09-20T03:19:24Z")
        self.assertIsNotNone(gen["startedAtTs"])
        # 恢复的 startedAtTs 能正确归属后续 slot（t_1 = startedAt+30min）
        slot = monitor.slot_for_timestamp(gen["startedAtTs"],
                                          gen["startedAtTs"] + monitor.SLOT_SPACING_S)
        self.assertEqual(slot, 1)

    def test_transient_count_recovered(self):
        self.journal.append_record({
            "recordKind": "transient_crash", "pid": 0, "epoch": 0, "slot": -1,
            "count": 3})
        mon = self.make_monitor()
        mon.recover_from_journal()
        self.assertEqual(mon.state["transientCount"], 3)


# ---------------------------------------------------------------------------
# 真实子进程：锁 / dirty-run（含 kill -9）
# ---------------------------------------------------------------------------

class SubprocessTestBase(unittest.TestCase):
    def setUp(self):
        self.root = tempfile.mkdtemp()
        self.data_root = os.path.join(self.root, "monitor-data")
        os.makedirs(self.data_root)
        self.runtime_path, self.token_path = make_runtime_files(self.root)
        self.addCleanup(shutil.rmtree, self.root, True)

    def spawn_monitor(self, hang=False, one_cycle=False, extra_env=None):
        env = dict(os.environ)
        env["CORDCODE_MONITOR_TEST_RUNTIME_JSON"] = self.runtime_path
        env["CORDCODE_MONITOR_TEST_TOKEN"] = self.token_path
        if hang:
            env["CORDCODE_MONITOR_TEST_HANG"] = "1"
        if one_cycle:
            env["CORDCODE_MONITOR_TEST_ONE_CYCLE"] = "1"
        if extra_env:
            env.update(extra_env)
        return subprocess.Popen(
            [sys.executable, MONITOR_PY, "run",
             "--data-root", self.data_root],
            env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            text=True)

    def read_jsonl(self):
        path = os.path.join(self.data_root, "samples.jsonl")
        if not os.path.exists(path):
            return []
        with open(path) as handle:
            return [json.loads(line) for line in handle if line.strip()]


class DualProcessLockTests(SubprocessTestBase):
    def test_second_process_cannot_acquire_while_first_holds(self):
        first = self.spawn_monitor(hang=True)
        try:
            deadline = time.time() + 15
            lock_path = os.path.join(self.data_root, "monitor.lock")
            content = ""
            while time.time() < deadline:
                if os.path.exists(lock_path):
                    try:
                        with open(lock_path) as handle:
                            content = handle.read().strip()
                    except OSError:
                        content = ""
                    if content:
                        break
                time.sleep(0.2)
            self.assertTrue(content, "第一进程应持有锁并写入 PID")

            # 持锁期间反复 rename 替换 state 文件，第二进程始终取不到锁
            state_path = os.path.join(self.data_root, "state.json")
            for _ in range(3):
                tmp = state_path + ".churn"
                with open(tmp, "w") as handle:
                    handle.write("{}")
                os.replace(tmp, state_path)
                second = self.spawn_monitor(one_cycle=True)
                _, stderr = second.communicate(timeout=30)
                self.assertEqual(second.returncode, 0)
                self.assertIn("another instance", stderr)
        finally:
            first.kill()
            first.wait(timeout=10)

    def test_lock_released_after_kill9(self):
        first = self.spawn_monitor(hang=True)
        try:
            deadline = time.time() + 15
            lock_path = os.path.join(self.data_root, "monitor.lock")
            while time.time() < deadline:
                try:
                    with open(lock_path) as handle:
                        if handle.read().strip():
                            break
                except OSError:
                    pass
                time.sleep(0.2)
        finally:
            first.kill()
            first.wait(timeout=10)
        second = self.spawn_monitor(one_cycle=True)
        _, stderr = second.communicate(timeout=30)
        self.assertEqual(second.returncode, 0)
        self.assertNotIn("another instance", stderr)


class DirtyRunTests(SubprocessTestBase):
    def test_kill9_accumulates_and_escalates_to_fatal(self):
        # 连续 5 次 transient（第 6 次启动发现第 5 个未清 marker）→ fatal。
        # 等待条件用 JSONL transient 计数而非 marker 存在性：上一轮残留的
        # dirty marker 会立即满足存在性检查，可能在 enter_run 前就误杀。
        for i in range(5):
            proc = self.spawn_monitor(hang=True)
            deadline = time.time() + 20
            while time.time() < deadline:
                if i == 0:
                    marker = os.path.join(self.data_root, "run_in_progress")
                    if os.path.exists(marker):
                        try:
                            with open(marker) as handle:
                                json.loads(handle.read())
                                break
                        except (OSError, ValueError):
                            pass
                else:
                    transients = [r for r in self.read_jsonl()
                                  if r.get("recordKind") == "transient_crash"]
                    if len(transients) >= i:
                        break
                time.sleep(0.2)
            proc.kill()
            proc.wait(timeout=10)
        sixth = self.spawn_monitor()
        _, stderr = sixth.communicate(timeout=30)
        self.assertEqual(sixth.returncode, 0, "fatal stop 必须 exit 0")
        fatal_path = os.path.join(self.data_root, "fatal.json")
        self.assertTrue(os.path.exists(fatal_path), "连续 5 次 → fatal.json")
        with open(fatal_path) as handle:
            fatal_doc = json.load(handle)
        self.assertEqual(fatal_doc["reason"], "consecutive_transient_crashes")
        self.assertFalse(os.path.exists(
            os.path.join(self.data_root, "run_in_progress")),
            "fatal 后 dirty marker 必须清除")
        transients = [r for r in self.read_jsonl()
                      if r.get("recordKind") == "transient_crash"]
        self.assertEqual(len(transients), 5)
        self.assertEqual(transients[-1]["count"], 5)

    def test_fatal_marker_stops_without_counting_transient(self):
        fatal_path = os.path.join(self.data_root, "fatal.json")
        with open(fatal_path, "w") as handle:
            json.dump({"reason": "consecutive_transient_crashes"}, handle)
        proc = self.spawn_monitor()
        _, _ = proc.communicate(timeout=30)
        self.assertEqual(proc.returncode, 0)
        self.assertEqual(self.read_jsonl(), [],
                         "fatal stop 不得再计 transient")

    def test_alert_stop_marker_stops_without_counting(self):
        stop_path = os.path.join(self.data_root, "stop.json")
        with open(stop_path, "w") as handle:
            json.dump({"reason": "alert", "alerts": ["trend"]}, handle)
        proc = self.spawn_monitor()
        proc.communicate(timeout=30)
        self.assertEqual(proc.returncode, 0)
        self.assertEqual(self.read_jsonl(), [])

    def test_completed_stop_marker_stops(self):
        stop_path = os.path.join(self.data_root, "stop.json")
        with open(stop_path, "w") as handle:
            json.dump({"reason": "completed"}, handle)
        proc = self.spawn_monitor()
        proc.communicate(timeout=30)
        self.assertEqual(proc.returncode, 0)


class MilestoneTests(SubprocessTestBase):
    def setUp(self):
        super().setUp()
        self.fake_now = [1000.0]
        self.mon = monitor.Monitor(
            self.data_root, runtime_json_path=self.runtime_path,
            token_path=self.token_path,
            now_fn=lambda: self.fake_now[0],
            sleep_fn=lambda s: self.fake_now.__setitem__(0, self.fake_now[0] + s))

    def test_milestone_clears_dirty_and_resets_count(self):
        self.mon.journal.open_for_append()
        self.assertEqual(self.mon.enter_run(), "run")
        self.assertTrue(os.path.exists(self.mon.dirty_marker_path))
        self.mon.state["transientCount"] = 3   # 模拟此前累计
        self.mon.run_start_ts = self.mon.now()
        self.mon.successful_transactions_this_run = 1
        self.fake_now[0] += monitor.DISCOVERY_INTERVAL_S + 1
        self.mon.check_healthy_milestone()
        self.assertFalse(os.path.exists(self.mon.dirty_marker_path))
        self.assertEqual(self.mon.state["transientCount"], 0)
        self.assertTrue(self.mon.milestone_reached)

    def test_milestone_requires_sample_or_idle_check(self):
        self.mon.journal.open_for_append()
        self.mon.enter_run()
        self.mon.run_start_ts = self.mon.now()
        self.fake_now[0] += monitor.DISCOVERY_INTERVAL_S + 1
        self.mon.check_healthy_milestone()
        self.assertTrue(os.path.exists(self.mon.dirty_marker_path),
                        "无采样且无 idle 检查 → milestone 未达")

    def test_milestone_requires_full_discovery_cycle(self):
        self.mon.journal.open_for_append()
        self.mon.enter_run()
        self.mon.run_start_ts = self.mon.now()
        self.mon.successful_transactions_this_run = 1
        self.fake_now[0] += 10   # 不足 60s
        self.mon.check_healthy_milestone()
        self.assertTrue(os.path.exists(self.mon.dirty_marker_path))


# ---------------------------------------------------------------------------
# launchd plist / defaults 探测 / token 安全
# ---------------------------------------------------------------------------

class PlistTests(unittest.TestCase):
    def test_build_plist_contract(self):
        install_script = ("/example/CordCode Link/memory-monitor/monitor.py")
        data_root = ("/example/CordCode Link/memory-monitor/")
        plist = monitor.build_plist(install_script, data_root)
        self.assertEqual(plist["Label"], "org.openagi.cordcode.link.memory-monitor")
        self.assertEqual(plist["ProgramArguments"],
                         ["/usr/bin/python3", install_script])
        self.assertEqual(plist["WorkingDirectory"], data_root)
        self.assertEqual(plist["StandardOutPath"],
                         os.path.join(data_root, "monitor.log"))
        self.assertEqual(plist["StandardErrorPath"],
                         os.path.join(data_root, "monitor.log"))
        self.assertEqual(plist["KeepAlive"], {"SuccessfulExit": False})
        self.assertEqual(plist["ThrottleInterval"], 30)


class DefaultsProbeTests(unittest.TestCase):
    def test_missing_key_is_code_default(self):
        # 本机实测：缺键 defaults read 非零退出
        def runner(argv, timeout=15):
            return 1, ""
        policy = monitor.probe_restart_policy(command_runner=runner)
        self.assertEqual(policy["autoRestartEnabled"], {
            "rawPresence": False, "effectiveValue": True,
            "source": "code_default"})
        self.assertEqual(policy["autoRestartIntervalMinutes"], {
            "rawPresence": False, "effectiveValue": 120,
            "source": "code_default"})

    def test_present_user_set(self):
        def runner(argv, timeout=15):
            key = argv[3]
            return 0, ("1" if key == "autoRestartEnabled" else "45")
        policy = monitor.probe_restart_policy(command_runner=runner)
        self.assertEqual(policy["autoRestartEnabled"], {
            "rawPresence": True, "effectiveValue": True, "source": "user_set"})
        self.assertEqual(policy["autoRestartIntervalMinutes"], {
            "rawPresence": True, "effectiveValue": 45, "source": "user_set"})

    def test_wrong_type_records_fallback_effective_value(self):
        def runner(argv, timeout=15):
            key = argv[3]
            return 0, ("maybe" if key == "autoRestartEnabled" else "1.5h")
        policy = monitor.probe_restart_policy(command_runner=runner)
        # Round 9 终审注记 2：同时记录运行时代码实际采用的 fallback
        self.assertEqual(policy["autoRestartEnabled"], {
            "rawPresence": True, "effectiveValue": True,
            "source": "invalid_type_fallback"})
        self.assertEqual(policy["autoRestartIntervalMinutes"], {
            "rawPresence": True, "effectiveValue": 120,
            "source": "invalid_type_fallback"})

    def test_command_failure_unavailable(self):
        def runner(argv, timeout=15):
            raise monitor.MonitorError("command_timeout:defaults")
        policy = monitor.probe_restart_policy(command_runner=runner)
        self.assertEqual(policy["autoRestartEnabled"]["source"], "unavailable")
        self.assertEqual(policy["autoRestartEnabled"]["effectiveValue"], True)


class TokenSafetyTests(SubprocessTestBase):
    def test_token_never_in_artifacts(self):
        # 失败事务（端口 1 拒连）+ 成功记录路径都不泄漏 token
        result, _ = monitor.run_transaction(
            self.runtime_path, self.token_path)
        self.assertFalse(result.valid)
        self.assertNotIn("SECRET-TOKEN-XYZ", str(result.reason))
        proc = self.spawn_monitor(one_cycle=True)
        stdout, stderr = proc.communicate(timeout=30)
        self.assertNotIn("SECRET-TOKEN-XYZ", stdout)
        self.assertNotIn("SECRET-TOKEN-XYZ", stderr)
        jsonl_path = os.path.join(self.data_root, "samples.jsonl")
        if os.path.exists(jsonl_path):
            with open(jsonl_path) as handle:
                self.assertNotIn("SECRET-TOKEN-XYZ", handle.read())
        state_path = os.path.join(self.data_root, "state.json")
        if os.path.exists(state_path):
            with open(state_path) as handle:
                self.assertNotIn("SECRET-TOKEN-XYZ", handle.read())

    def test_http_error_messages_carry_no_token(self):
        try:
            monitor.http_get_json("http://127.0.0.1:1/x", "SECRET-TOKEN-XYZ")
            self.fail("应抛 MonitorError")
        except monitor.MonitorError as exc:
            self.assertNotIn("SECRET-TOKEN-XYZ", str(exc))
            self.assertNotIn("SECRET-TOKEN-XYZ", repr(exc))


if __name__ == "__main__":
    unittest.main(verbosity=2)
