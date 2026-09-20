#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""CordCode Link memory monitor — followups v9 §2.3（docs/2026-09-20-memory-followups.md）。

多代际内存复采：六步原子采样事务、JSONL journal（唯一 durable truth）、
dirty-run 跨实例 crash 计数、launchd 运行单元、三层负载门、三类 OR 告警、
完成条件（168h 前向墙钟 + 7 日历日 + ≥20 有效代际 + ≥8 scan-evidenced）。

只用系统标准库；由 /usr/bin/python3 执行（plist ProgramArguments 契约）。
token 只存在于进程内存变量：不进 argv、环境变量、临时文件、错误文本、JSONL。

用法：
  monitor.py            # 运行（launchd 默认入口，无参数 = run）
  monitor.py run
  monitor.py probe      # 对活体 runtime 跑一笔完整事务并打印结果（不写 journal）
  monitor.py install    # 复制安装副本 + 生成 plist + launchctl bootstrap
  monitor.py uninstall  # launchctl bootout（数据根保留，owner 决定删除）
  monitor.py report     # 从 journal 汇总当前状态
"""

import fcntl
import hashlib
import json
import os
import plistlib
import shutil
import stat
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from typing import Any, Callable, Dict, List, Optional, Sequence, Tuple

# ---------------------------------------------------------------------------
# 常量（§2.3 预固定；变更须修订文档）
# ---------------------------------------------------------------------------

HTTP_TIMEOUT_S = 10          # 单 HTTP 请求
COMMAND_TIMEOUT_S = 15       # 单本地命令（vmmap/ps/sysctl/defaults）
TRANSACTION_TIMEOUT_S = 90   # 整笔事务（含 identity B 与 bootstrap 重读）

DISCOVERY_INTERVAL_S = 60    # 代际发现节奏
SLOT_SPACING_S = 30 * 60    # slot k = startedAt + 30k 分钟（k=0..3）
SLOT_WINDOW_S = 15 * 60     # slot 窗口 [t_k, t_k+15min)
SLOT_COUNT = 4

ALERT_SYS_MINUS_RELEASED_B = 268_435_456   # ① 256MiB，严格大于
ALERT_FOOTPRINT_B = 314_572_800           # ② 300MiB，current 或 lifetime peak
ALERT_TREND_DELTA_B = 8_388_608           # ③ 每步 ≥ 8MiB
ALERT_TREND_SAMPLES = 4                   # ③ 4 连续有效样本 → 3 个相邻 delta
CPU_ACTIVE_DELTA_S = 60                   # cpu-active-only 门（provisional）

COMPLETION_ELAPSED_S = 168 * 3600         # ≥168h 前向墙钟
COMPLETION_CALENDAR_DAYS = 7              # ≥7 本地日历日
COMPLETION_VALID_GENERATIONS = 20         # ≥20 有效代际（≥3 有效样本）
COMPLETION_SCAN_EVIDENCED = 8             # ≥8 scan-evidenced 代际
VALID_GENERATION_MIN_SAMPLES = 3

TRANSIENT_FATAL_THRESHOLD = 5             # 连续 transient ≥5 → fatal

APP_SUPPORT_DIR = os.path.join(
    os.path.expanduser("~"), "Library", "Application Support", "CordCode Link")
RUNTIME_JSON_PATH = os.path.join(APP_SUPPORT_DIR, "runtime.json")
MANAGEMENT_TOKEN_PATH = os.path.join(APP_SUPPORT_DIR, "management-token")

DEFAULT_DATA_ROOT = os.path.join(APP_SUPPORT_DIR, "memory-monitor")
PLIST_LABEL = "org.openagi.cordcode.link.memory-monitor"
PLIST_PATH = os.path.join(
    os.path.expanduser("~"), "Library", "LaunchAgents",
    PLIST_LABEL + ".plist")
PYTHON_BIN = "/usr/bin/python3"

APP_DOMAIN = "org.openagi.cordcode.link"   # project.pbxproj:524 实证
RESTART_POLICY_KEYS = {
    "autoRestartEnabled": (True, "bool"),
    "autoRestartIntervalMinutes": (120, "int"),
}

RECORD_SAMPLE = "sample"
RECORD_REJECTED = "rejected"
RECORD_MISSED = "missed"
RECORD_DUPLICATE = "duplicate"
RECORD_CORRUPT_LINE = "corrupt_line"
RECORD_RESTART_POLICY = "restart_policy"
RECORD_TRANSIENT = "transient_crash"
RECORD_FATAL = "fatal"
RECORD_STOP = "stop"
RECORD_COMPLETION = "completion"
RECORD_CLOCK_ANOMALY = "clock_anomaly"
RECORD_OUT_OF_SLOT = "out_of_slot"

# 诊断 payload 必需字段（§2.3.2；缺失/类型错 → payload_invalid，不以零值制造有效样本）
DIAG_MEMORY_FIELDS = (
    "sys", "heapSys", "heapInuse", "heapIdle", "heapReleased",
    "sysMinusHeapReleased", "heapObjects", "stackSys", "numGC")
DIAG_NUMERIC_FIELDS = ("processUserCPUSeconds", "processSystemCPUSeconds")
SCAN_COUNTER_FIELDS = ("scans", "turnItemRequests", "scannedTurns")


class MonitorError(Exception):
    """带 reason code 的可记录失败（不崩溃）。"""


class UnsafePathError(MonitorError):
    """数据根/文件不可信（owner 错/类型错/symlink/不可写）→ 启动前安全失败。"""


# ---------------------------------------------------------------------------
# 纯函数：epoch 转换 / URL 校验 / 解析器（合成与真实 fixture 均可测）
# ---------------------------------------------------------------------------

def derive_epoch(uuid_str: str) -> int:
    """与 go-bridge managementBridgeEpoch（main.go:815-822）逐字节同算法：
    SHA-256(uuid) 前 8 字节 big-endian uint64，0→1。"""
    digest = hashlib.sha256(uuid_str.encode("utf-8")).digest()
    value = int.from_bytes(digest[:8], "big")
    return 1 if value == 0 else value


def validate_management_url(url: str) -> Optional[str]:
    """§2.3.1 URL 严格校验。返回 None=通过；否则返回违规原因（不发任何请求）。"""
    try:
        parts = urllib.parse.urlsplit(url)
    except ValueError:
        return "unparsable"
    if parts.scheme != "http":
        return "scheme_not_http"
    if parts.username is not None or parts.password is not None:
        return "userinfo_present"
    if parts.hostname is None:
        return "no_host"
    if parts.hostname not in ("127.0.0.1", "::1"):
        return "host_not_loopback"
    if parts.path not in ("", "/"):
        return "path_not_root"
    if parts.query or parts.fragment:
        return "query_or_fragment"
    try:
        port = parts.port
    except ValueError:
        return "port_invalid"
    if port is None:
        return "port_missing"
    if not (1 <= port <= 65535):
        return "port_out_of_range"
    return None


_VMMAP_UNITS = {"K": 1024, "M": 1024 * 1024, "G": 1024 * 1024 * 1024}


def parse_size(text: str) -> Optional[int]:
    """vmmap 二进制惯例单位：K/M/G；无后缀=字节；支持小数与空格变化。
    未知单位/非数字 → None（指标级 unavailable，不按 0）。"""
    token = text.strip()
    if not token:
        return None
    unit = ""
    if token[-1].upper() in _VMMAP_UNITS:
        unit = token[-1].upper()
        token = token[:-1].strip()
    if not token:
        return None
    try:
        value = float(token)
    except ValueError:
        return None
    if value < 0:
        return None
    return int(round(value * _VMMAP_UNITS[unit])) if unit else int(round(value))


def parse_vmmap_summary(output: str) -> Dict[str, Optional[int]]:
    """解析 vmmap --summary：current `Physical footprint:`、
    `Physical footprint (peak)`、`TOTAL SWAPPED`（报告用，可缺）。
    指标级 unavailable（None）；重复行 → None。"""
    result: Dict[str, Optional[int]] = {
        "footprintCurrent": None, "footprintPeak": None, "swapped": None}
    seen = {"footprintCurrent": 0, "footprintPeak": 0, "swapped": 0}
    for line in output.splitlines():
        stripped = line.strip()
        if stripped.startswith("Physical footprint (peak)"):
            seen["footprintPeak"] += 1
            if seen["footprintPeak"] == 1:
                parts = stripped.split(":", 1)
                if len(parts) == 2:
                    result["footprintPeak"] = parse_size(parts[1])
        elif stripped.startswith("Physical footprint:"):
            seen["footprintCurrent"] += 1
            if seen["footprintCurrent"] == 1:
                parts = stripped.split(":", 1)
                if len(parts) == 2:
                    result["footprintCurrent"] = parse_size(parts[1])
        elif stripped.startswith("TOTAL SWAPPED"):
            seen["swapped"] += 1
            if seen["swapped"] == 1:
                parts = stripped.split(":", 1)
                if len(parts) == 2:
                    result["swapped"] = parse_size(parts[1])
    for key, count in seen.items():
        if count > 1:
            result[key] = None   # 重复行 → unavailable
    return result


def parse_ps_rss(output: str) -> Optional[int]:
    """`ps -o rss=` 无表头形状；KB→B ×1024。非数字 → None。"""
    token = output.strip()
    if not token:
        return None
    try:
        return int(token) * 1024
    except ValueError:
        return None


def parse_loadavg(output: str) -> Optional[str]:
    """sysctl vm.loadavg —— 仅系统背景，原样归档。"""
    stripped = output.strip()
    return stripped or None


def _is_number(value: Any) -> bool:
    return isinstance(value, (int, float)) and not isinstance(value, bool)


def validate_diagnostics_payload(payload: Any) -> Dict[str, Any]:
    """§2.3.2 HTTP JSON 必需字段/类型校验。失败抛 MonitorError(payload_invalid)。
    返回规范化 dict：memory/cpu/startedAt/scanCounters。"""
    if not isinstance(payload, dict):
        raise MonitorError("payload_invalid")
    started_at = payload.get("startedAt")
    if not isinstance(started_at, str) or not started_at:
        raise MonitorError("payload_invalid")
    memory = payload.get("memory")
    if not isinstance(memory, dict):
        raise MonitorError("payload_invalid")
    for field in DIAG_MEMORY_FIELDS:
        if not _is_number(memory.get(field)):
            raise MonitorError("payload_invalid")
    for field in DIAG_NUMERIC_FIELDS:
        if not _is_number(payload.get(field)):
            raise MonitorError("payload_invalid")
    cpu_available = payload.get("processCPUAvailable")
    if not isinstance(cpu_available, bool):
        raise MonitorError("payload_invalid")

    # agentBackgroundScans:<backendID> wire schema（R7-N1）
    scan_counters: Dict[str, Dict[str, Any]] = {}
    for key, value in payload.items():
        if not key.startswith("agentBackgroundScans:"):
            continue
        backend = key.split(":", 1)[1]
        if not isinstance(value, dict):
            scan_counters[backend] = {"unavailable": True}
            continue
        entry: Dict[str, Any] = {"unavailable": False}
        for field in SCAN_COUNTER_FIELDS:
            raw = value.get(field)
            if isinstance(raw, bool) or not isinstance(raw, int) or raw < 0:
                entry["unavailable"] = True
                break
            entry[field] = raw
        scan_counters[backend] = entry
    return {
        "startedAt": started_at,
        "memory": {f: memory[f] for f in DIAG_MEMORY_FIELDS},
        "cpu": {
            "userSeconds": payload["processUserCPUSeconds"],
            "systemSeconds": payload["processSystemCPUSeconds"],
            "available": cpu_available,
        },
        "scanCounters": scan_counters,
    }


def scan_evidenced(first: Optional[Dict[str, Any]],
                   last: Optional[Dict[str, Any]]) -> bool:
    """代际 scan-evidenced：任一 backend 同名 counter 首末可比较且 delta > 0。"""
    if not first or not last:
        return False
    for backend, first_entry in first.get("scanCounters", {}).items():
        last_entry = last.get("scanCounters", {}).get(backend)
        if not last_entry:
            continue
        if first_entry.get("unavailable") or last_entry.get("unavailable"):
            continue
        for field in SCAN_COUNTER_FIELDS:
            if field in first_entry and field in last_entry:
                if last_entry[field] - first_entry[field] > 0:
                    return True
    return False


def cpu_active(first: Optional[Dict[str, Any]],
               last: Optional[Dict[str, Any]]) -> bool:
    """cpu-active-only：CPU 累计 delta ≥ 60s（provisional）。CPU 不可用 → False。"""
    if not first or not last:
        return False
    if not first.get("cpu", {}).get("available", False):
        return False
    if not last.get("cpu", {}).get("available", False):
        return False
    delta = ((last["cpu"]["userSeconds"] + last["cpu"]["systemSeconds"])
             - (first["cpu"]["userSeconds"] + first["cpu"]["systemSeconds"]))
    return delta >= CPU_ACTIVE_DELTA_S


def classify_generation_tier(first: Optional[Dict[str, Any]],
                             last: Optional[Dict[str, Any]]) -> str:
    if scan_evidenced(first, last):
        return "scan-evidenced"
    if cpu_active(first, last):
        return "cpu-active-only"
    return "idle/low-activity"


def slot_for_timestamp(started_at_ts: float, ts: float) -> Optional[int]:
    """slot k=0..3，窗口 [t_k, t_k+15min)。窗口外 → None（out_of_slot）。"""
    for k in range(SLOT_COUNT):
        t_k = started_at_ts + SLOT_SPACING_S * k
        if t_k <= ts < t_k + SLOT_WINDOW_S:
            return k
    return None


def evaluate_alerts(samples: List[Dict[str, Any]]) -> List[str]:
    """三类独立 OR（§2.3.5）。samples 为同代际按 slot 升序的有效样本。"""
    triggered: List[str] = []
    for sample in samples:
        sys_released = sample.get("memory", {}).get("sysMinusHeapReleased")
        if isinstance(sys_released, (int, float)) and sys_released > ALERT_SYS_MINUS_RELEASED_B:
            triggered.append("absolute_sys_minus_released")
            break
    for sample in samples:
        footprint = sample.get("footprint", {})
        current = footprint.get("footprintCurrent")
        peak = footprint.get("footprintPeak")
        for value in (current, peak):
            if isinstance(value, (int, float)) and value > ALERT_FOOTPRINT_B:
                triggered.append("footprint_peak")
                break
        else:
            continue
        break
    if len(samples) >= ALERT_TREND_SAMPLES:
        window = samples[-ALERT_TREND_SAMPLES:]
        slots = [s.get("slot") for s in window]
        consecutive = all(
            slots[i + 1] is not None and slots[i] is not None
            and slots[i + 1] - slots[i] == 1 for i in range(len(slots) - 1))
        if consecutive:
            values = [s.get("memory", {}).get("sysMinusHeapReleased") for s in window]
            if all(isinstance(v, (int, float)) for v in values):
                deltas = [values[i + 1] - values[i] for i in range(len(values) - 1)]
                if deltas and all(d >= ALERT_TREND_DELTA_B for d in deltas):
                    triggered.append("trend")
    return triggered


def completion_satisfied(elapsed_s: float, calendar_days: int,
                         valid_generations: int, scan_evidenced_count: int) -> bool:
    return (elapsed_s >= COMPLETION_ELAPSED_S
            and calendar_days >= COMPLETION_CALENDAR_DAYS
            and valid_generations >= COMPLETION_VALID_GENERATIONS
            and scan_evidenced_count >= COMPLETION_SCAN_EVIDENCED)


# ---------------------------------------------------------------------------
# 安全文件操作（§2.3.1 N2：O_NOFOLLOW + fstat；0600 从创建瞬间起）
# ---------------------------------------------------------------------------

def _open_nofollow(path: str, flags: int, mode: int = 0o600) -> int:
    fd = os.open(path, flags | os.O_NOFOLLOW, mode)
    st = os.fstat(fd)
    if not stat.S_ISREG(st.st_mode):
        os.close(fd)
        raise UnsafePathError("not_regular_file:%s" % path)
    return fd


def safe_read_file(path: str) -> bytes:
    fd = _open_nofollow(path, os.O_RDONLY)
    with os.fdopen(fd, "rb") as handle:
        return handle.read()


def atomic_write(path: str, data: bytes, mode: int = 0o600) -> None:
    """tmp（创建即 mode，O_EXCL|O_NOFOLLOW）→ fsync → replace → fsync dir。
    上次 crash 残留的 tmp 不是已提交事实，先清除（unlink 不跟随 symlink）。"""
    tmp_path = path + ".tmp"
    if os.path.exists(tmp_path) or os.path.islink(tmp_path):
        os.unlink(tmp_path)
    fd = _open_nofollow(tmp_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, mode)
    try:
        with os.fdopen(fd, "wb") as handle:
            handle.write(data)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(tmp_path, path)
        dir_fd = os.open(os.path.dirname(path), os.O_RDONLY)
        try:
            os.fsync(dir_fd)
        finally:
            os.close(dir_fd)
    finally:
        if os.path.exists(tmp_path):
            try:
                os.unlink(tmp_path)
            except OSError:
                pass


def validate_data_root(data_root: str, create: bool = True) -> None:
    """启动权限验证：owner=当前 uid；仅 mode 过宽 → chmod 收紧；
    其余（symlink/owner 错/类型错/不可写）→ UnsafePathError（启动前安全失败，
    不写任何文件）。"""
    parent = os.path.dirname(data_root)
    if not os.path.isdir(parent):
        raise UnsafePathError("parent_missing:%s" % parent)
    if os.path.islink(data_root):
        raise UnsafePathError("data_root_symlink")
    if not os.path.exists(data_root):
        if not create:
            raise UnsafePathError("data_root_missing")
        os.mkdir(data_root, 0o700)
    st = os.lstat(data_root)
    if not stat.S_ISDIR(st.st_mode):
        raise UnsafePathError("data_root_not_dir")
    if st.st_uid != os.geteuid():
        raise UnsafePathError("data_root_owner_mismatch")
    if st.st_mode & 0o077:
        os.chmod(data_root, 0o700)
    if not os.access(data_root, os.W_OK | os.X_OK):
        raise UnsafePathError("data_root_not_writable")


# ---------------------------------------------------------------------------
# HTTP 客户端（进程内；拒绝 redirect；token 不进错误文本）
# ---------------------------------------------------------------------------

class _RedirectRejected(Exception):
    def __init__(self, status: int):
        super().__init__("redirect_%d" % status)
        self.status = status


class _NoRedirectHandler(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise _RedirectRejected(code)


def http_get_json(url: str, token: str, timeout: float = HTTP_TIMEOUT_S) -> Tuple[int, Any]:
    """GET + Bearer。返回 (status, parsed_json_or_None)。
    401/403 → MonitorError(auth_rejected)；3xx → MonitorError(redirect_rejected)；
    网络错误 → MonitorError(command_failed)；超时 → MonitorError(command_timeout)。
    错误文本只含 status/reason code，不含 headers/token。"""
    request = urllib.request.Request(url, method="GET")
    request.add_header("Authorization", "Bearer " + token)
    # loopback 直连：禁用一切代理（http_proxy 等环境变量不得劫持本机请求）
    opener = urllib.request.build_opener(
        urllib.request.ProxyHandler({}), _NoRedirectHandler)
    try:
        with opener.open(request, timeout=timeout) as response:
            body = response.read(1 << 20)
            status = response.status
    except _RedirectRejected as exc:
        raise MonitorError("redirect_rejected") from exc
    except urllib.error.HTTPError as exc:
        if exc.code in (401, 403):
            raise MonitorError("auth_rejected")
        raise MonitorError("command_failed:http_%d" % exc.code) from exc
    except urllib.error.URLError as exc:
        reason = getattr(exc, "reason", None)
        if isinstance(reason, (TimeoutError, OSError)) and "timed out" in str(reason).lower():
            raise MonitorError("command_timeout") from exc
        raise MonitorError("command_failed:network") from exc
    except TimeoutError as exc:
        raise MonitorError("command_timeout") from exc
    try:
        return status, json.loads(body.decode("utf-8")) if body else None
    except (ValueError, UnicodeDecodeError) as exc:
        raise MonitorError("payload_invalid") from exc


def run_local_command(argv: Sequence[str], timeout: float = COMMAND_TIMEOUT_S) -> Tuple[int, str]:
    """本地命令（vmmap/ps/sysctl/defaults）。超时 → MonitorError(command_timeout)
    并回收本命令创建的子进程。"""
    try:
        proc = subprocess.run(
            list(argv), capture_output=True, text=True, timeout=timeout)
    except subprocess.TimeoutExpired as exc:
        raise MonitorError("command_timeout:%s" % argv[0]) from exc
    except OSError as exc:
        raise MonitorError("command_failed:%s" % argv[0]) from exc
    return proc.returncode, proc.stdout


# ---------------------------------------------------------------------------
# Bootstrap 读取（runtime.json + management-token）
# ---------------------------------------------------------------------------

class Bootstrap:
    __slots__ = ("management_url", "pid", "bridge_epoch_uuid", "token")

    def __init__(self, management_url: str, pid: int,
                 bridge_epoch_uuid: str, token: str):
        self.management_url = management_url
        self.pid = pid
        self.bridge_epoch_uuid = bridge_epoch_uuid
        self.token = token

    def identity(self) -> Tuple[int, int]:
        return (self.pid, derive_epoch(self.bridge_epoch_uuid))

    def stable_view(self) -> Tuple[str, int, str, str]:
        """URL/pid/原始 UUID/token —— bootstrap 重读一致性比较用（不落盘）。"""
        return (self.management_url, self.pid, self.bridge_epoch_uuid, self.token)


def read_bootstrap(runtime_json_path: str,
                   token_path: str) -> Bootstrap:
    """§2.3.1 步骤 1。缺文件 → bootstrap_unavailable；JSON/字段/URL 违规 →
    bootstrap_invalid；token 缺失/空 → management_token_unavailable。"""
    try:
        raw = safe_read_file(runtime_json_path)
    except (OSError, UnsafePathError) as exc:
        raise MonitorError("bootstrap_unavailable") from exc
    try:
        doc = json.loads(raw.decode("utf-8"))
    except (ValueError, UnicodeDecodeError) as exc:
        raise MonitorError("bootstrap_invalid") from exc
    if not isinstance(doc, dict):
        raise MonitorError("bootstrap_invalid")
    url = doc.get("managementUrl")
    pid = doc.get("pid")
    epoch_uuid = doc.get("bridgeEpoch")
    if not isinstance(url, str) or not isinstance(pid, int) or isinstance(pid, bool) \
            or not isinstance(epoch_uuid, str) or not epoch_uuid:
        raise MonitorError("bootstrap_invalid")
    violation = validate_management_url(url)
    if violation:
        raise MonitorError("bootstrap_invalid:%s" % violation)
    try:
        token_bytes = safe_read_file(token_path)
    except (OSError, UnsafePathError) as exc:
        raise MonitorError("management_token_unavailable") from exc
    token = token_bytes.decode("utf-8", errors="strict").strip()
    if not token:
        raise MonitorError("management_token_unavailable")
    return Bootstrap(url, pid, epoch_uuid, token)


# ---------------------------------------------------------------------------
# 六步采样事务（§2.3.1）
# ---------------------------------------------------------------------------

class TransactionResult:
    def __init__(self, valid: bool, reason: Optional[str] = None,
                 sample: Optional[Dict[str, Any]] = None):
        self.valid = valid
        self.reason = reason
        self.sample = sample


def fetch_identity(boot: Bootstrap) -> Tuple[int, int]:
    """GET /internal/status → (pid, uint64Epoch)。"""
    status, payload = http_get_json(
        boot.management_url.rstrip("/") + "/internal/status", boot.token)
    if not isinstance(payload, dict):
        raise MonitorError("payload_invalid")
    identity = payload.get("runtimeIdentity")
    if not isinstance(identity, dict):
        raise MonitorError("payload_invalid")
    pid = identity.get("pid")
    epoch = identity.get("bridgeEpoch")
    if not isinstance(pid, int) or isinstance(pid, bool) \
            or not isinstance(epoch, int) or isinstance(epoch, bool):
        raise MonitorError("payload_invalid")
    return (pid, epoch)


def run_transaction(runtime_json_path: str, token_path: str,
                    recorded_started_at: Optional[str] = None,
                    command_runner: Callable[[Sequence[str]], Tuple[int, str]] = run_local_command
                    ) -> Tuple[TransactionResult, Optional[str]]:
    """六步事务。返回 (result, startedAt_or_None)——startedAt 供代际首次记录。
    任何一步失败 → rejected（reason code），绝不拼部分字段。"""
    deadline = time.monotonic() + TRANSACTION_TIMEOUT_S

    def check_deadline() -> None:
        if time.monotonic() > deadline:
            raise MonitorError("command_timeout:transaction")

    try:
        return _run_transaction_steps(
            runtime_json_path, token_path, recorded_started_at,
            command_runner, check_deadline)
    except MonitorError as exc:
        # 任何一步失败 → rejected（reason code），绝不拼部分字段，
        # 也不得上抛成 transient crash。
        return TransactionResult(False, exc.args[0] if exc.args else "unknown"), None


def _run_transaction_steps(runtime_json_path: str, token_path: str,
                           recorded_started_at: Optional[str],
                           command_runner: Callable[[Sequence[str]], Tuple[int, str]],
                           check_deadline: Callable[[], None]
                           ) -> Tuple[TransactionResult, Optional[str]]:
    # 步骤 1：bootstrap（含鉴权）
    boot = read_bootstrap(runtime_json_path, token_path)

    # 步骤 2：identity A
    identity_a = fetch_identity(boot)
    if identity_a != boot.identity():
        # 重读 runtime.json 一次，仍不匹配 → bootstrap_mismatch
        boot_b = read_bootstrap(runtime_json_path, token_path)
        if identity_a != boot_b.identity():
            return TransactionResult(False, "bootstrap_mismatch"), None
        boot = boot_b

    # 步骤 3：数据采集
    check_deadline()
    status, diag_raw = http_get_json(
        boot.management_url.rstrip("/") + "/internal/diagnostics/runtime", boot.token)
    try:
        diag = validate_diagnostics_payload(diag_raw)
    except MonitorError as exc:
        return TransactionResult(False, exc.args[0] if exc.args else "payload_invalid"), None

    check_deadline()
    rc, vmmap_out = command_runner(["vmmap", "--summary", str(identity_a[0])])
    if rc != 0:
        return TransactionResult(False, "command_failed:vmmap"), None
    vmmap_metrics = parse_vmmap_summary(vmmap_out)

    check_deadline()
    rc, ps_out = command_runner(["ps", "-o", "rss=", str(identity_a[0])])
    if rc != 0:
        return TransactionResult(False, "command_failed:ps"), None
    rss = parse_ps_rss(ps_out)
    if rss is None:
        return TransactionResult(False, "command_failed:ps"), None

    check_deadline()
    rc, loadavg_out = command_runner(["sysctl", "vm.loadavg"])
    if rc != 0:
        return TransactionResult(False, "command_failed:sysctl"), None
    loadavg = parse_loadavg(loadavg_out)

    # 步骤 4：identity B
    check_deadline()
    identity_b = fetch_identity(boot)

    # 步骤 5：bootstrap 重读（含 token）
    check_deadline()
    boot_reread = read_bootstrap(runtime_json_path, token_path)
    if boot_reread.stable_view() != boot.stable_view():
        return TransactionResult(False, "bootstrap_rewritten"), None

    # 步骤 6：提交判定
    if identity_a != identity_b:
        return TransactionResult(False, "identity_changed"), None
    if recorded_started_at is not None and diag["startedAt"] != recorded_started_at:
        return TransactionResult(False, "started_at_mismatch"), None

    sample = {
        "pid": identity_a[0],
        "epoch": identity_a[1],
        "ts": time.time(),
        "startedAt": diag["startedAt"],
        "memory": diag["memory"],
        "cpu": diag["cpu"],
        "scanCounters": diag["scanCounters"],
        "footprint": vmmap_metrics,
        "rss": rss,
        "loadavg": loadavg,
    }
    return TransactionResult(True, None, sample), diag["startedAt"]


# ---------------------------------------------------------------------------
# Journal（§2.3.3：JSONL 唯一 durable truth；append+fsync → state 快照）
# ---------------------------------------------------------------------------

class Journal:
    def __init__(self, data_root: str):
        self.data_root = data_root
        self.jsonl_path = os.path.join(data_root, "samples.jsonl")
        self.state_path = os.path.join(data_root, "state.json")
        self._jsonl_fd: Optional[int] = None

    # -- 写入 --

    def open_for_append(self) -> None:
        self._jsonl_fd = _open_nofollow(
            self.jsonl_path, os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o600)

    def append_record(self, record: Dict[str, Any]) -> None:
        """append 完整 JSONL 行 → flush + fsync →（调用方随后写 state 快照）。
        唯一键 (pid, epoch, slot, recordKind) 由调用方填入记录。"""
        if self._jsonl_fd is None:
            self.open_for_append()
        line = (json.dumps(record, ensure_ascii=False, sort_keys=True) + "\n")
        data = line.encode("utf-8")
        os.write(self._jsonl_fd, data)
        os.fsync(self._jsonl_fd)

    def write_state(self, state: Dict[str, Any]) -> None:
        atomic_write(self.state_path, json.dumps(state, ensure_ascii=False).encode("utf-8"))

    # -- 启动恢复 --

    def recover(self) -> Tuple[List[Dict[str, Any]], Dict[str, int]]:
        """截断 partial tail（记 corrupt_line）→ 重放 JSONL → 完整行确定性去重
        （保留首条、计数 duplicate）。返回 (records, counts)。"""
        counts = {"corrupt_line": 0, "duplicate": 0}
        if not os.path.exists(self.jsonl_path):
            return [], counts
        raw = safe_read_file(self.jsonl_path)
        # partial tail：不以换行结尾 → 截断到最后一行边界
        if raw and not raw.endswith(b"\n"):
            cut = raw.rfind(b"\n") + 1
            fd = _open_nofollow(self.jsonl_path, os.O_WRONLY)
            try:
                os.ftruncate(fd, cut)
                os.fsync(fd)
            finally:
                os.close(fd)
            counts["corrupt_line"] += 1
            raw = raw[:cut]
        records: List[Dict[str, Any]] = []
        seen_lines = set()
        for line in raw.decode("utf-8", errors="replace").splitlines():
            if not line.strip():
                continue
            if line in seen_lines:
                counts["duplicate"] += 1
                continue
            try:
                record = json.loads(line)
            except ValueError:
                counts["corrupt_line"] += 1
                continue
            if not isinstance(record, dict):
                counts["corrupt_line"] += 1
                continue
            seen_lines.add(line)
            records.append(record)
        return records, counts


# ---------------------------------------------------------------------------
# Restart policy 只读探测（§2.3.3 N1：缺键=code_default）
# ---------------------------------------------------------------------------

def parse_defaults_bool(raw: str) -> Optional[bool]:
    if raw in ("0", "1"):
        return raw == "1"
    return None


def parse_defaults_int(raw: str) -> Optional[int]:
    if raw.isdigit():
        return int(raw)
    return None


def probe_restart_policy(
    domain: str = APP_DOMAIN,
    command_runner: Callable[[Sequence[str]], Tuple[int, str]] = run_local_command,
) -> Dict[str, Dict[str, Any]]:
    """只读探测 {rawPresence, effectiveValue, source}（不得修改偏好）。
    键缺失/domain 缺失 → code_default；类型错 → invalid_type_fallback
    （记录运行时代码实际采用的 fallback effective value）；命令失败 →
    unavailable。"""
    result: Dict[str, Dict[str, Any]] = {}
    for key, (code_default, kind) in RESTART_POLICY_KEYS.items():
        try:
            rc, out = command_runner(["defaults", "read", domain, key])
        except MonitorError:
            result[key] = {
                "rawPresence": "unknown", "effectiveValue": code_default,
                "source": "unavailable"}
            continue
        if rc != 0:
            # 本机实测：缺键与缺 domain 的 stderr 均含 "does not exist"
            result[key] = {
                "rawPresence": False, "effectiveValue": code_default,
                "source": "code_default"}
            continue
        raw = out.strip()
        if kind == "bool":
            parsed = parse_defaults_bool(raw)
        else:
            parsed = parse_defaults_int(raw)
        if parsed is None:
            result[key] = {
                "rawPresence": True, "effectiveValue": code_default,
                "source": "invalid_type_fallback"}
        else:
            result[key] = {
                "rawPresence": True, "effectiveValue": parsed,
                "source": "user_set"}
    return result


# ---------------------------------------------------------------------------
# Monitor 主类（可注入 time/http/command 供测试）
# ---------------------------------------------------------------------------

class Monitor:
    def __init__(self, data_root: str,
                 runtime_json_path: str = RUNTIME_JSON_PATH,
                 token_path: str = MANAGEMENT_TOKEN_PATH,
                 now_fn: Callable[[], float] = time.time,
                 sleep_fn: Callable[[float], None] = time.sleep,
                 command_runner: Callable[[Sequence[str]], Tuple[int, str]] = run_local_command):
        self.data_root = data_root
        self.runtime_json_path = runtime_json_path
        self.token_path = token_path
        self.now = now_fn
        self.sleep = sleep_fn
        self.command_runner = command_runner
        self.journal = Journal(data_root)
        self.lock_fd: Optional[int] = None
        # state（可由 JSONL 重放重建）
        self.state: Dict[str, Any] = {
            "transientCount": 0,
            "elapsedSeconds": 0.0,
            "lastObservedWallclock": None,
            "calendarDays": [],
            "generations": {},       # genKey → {startedAt, slots:{k:sample}, tier, ...}
            "alertStop": False,
            "completed": False,
            "runGeneration": 0,
        }
        self.current_gen_key: Optional[str] = None
        self.milestone_reached = False
        self.run_start_ts: Optional[float] = None
        self.successful_samples_this_run = 0
        self.idle_checks_this_run = 0

    # -- 路径 --

    @property
    def lock_path(self) -> str:
        return os.path.join(self.data_root, "monitor.lock")

    @property
    def dirty_marker_path(self) -> str:
        return os.path.join(self.data_root, "run_in_progress")

    @property
    def fatal_marker_path(self) -> str:
        return os.path.join(self.data_root, "fatal.json")

    @property
    def stop_marker_path(self) -> str:
        return os.path.join(self.data_root, "stop.json")

    # -- 锁（独立稳定 inode；生命周期内绝不 rename/unlink）--

    def acquire_lock(self) -> bool:
        try:
            fd = _open_nofollow(
                self.lock_path, os.O_RDWR | os.O_CREAT, 0o600)
        except (OSError, UnsafePathError):
            return False
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except OSError:
            os.close(fd)
            return False
        st = os.fstat(fd)
        if st.st_mode & 0o077:
            os.fchmod(fd, 0o600)
        os.ftruncate(fd, 0)
        os.write(fd, ("%d\n" % os.getpid()).encode("ascii"))
        self.lock_fd = fd
        return True

    def release_lock(self) -> None:
        if self.lock_fd is not None:
            try:
                fcntl.flock(self.lock_fd, fcntl.LOCK_UN)
            finally:
                os.close(self.lock_fd)
                self.lock_fd = None

    # -- journal 记录辅助 --

    def _record(self, record_kind: str, pid: int = 0, epoch: int = 0,
                slot: int = -1, **fields: Any) -> None:
        record = {
            "recordKind": record_kind, "pid": pid, "epoch": epoch,
            "slot": slot, "seq": int(self.now() * 1000),
        }
        record.update(fields)
        self.journal.append_record(record)

    def _record_rejected(self, reason: Optional[str], **fields: Any) -> None:
        """rejected 记录按原因去重：idle 周期反复失败同一原因不刷屏
        （原因变化或恢复后再失败会再记一条）。"""
        key = reason or "unknown"
        if self.state.get("lastRejectedReason") == key:
            return
        self.state["lastRejectedReason"] = key
        self._record(RECORD_REJECTED, reason=key, **fields)

    def _snapshot_state(self) -> None:
        self.journal.write_state(self.state)

    # -- 启动恢复（重放 JSONL 重建 state）--

    def recover_from_journal(self) -> None:
        records, counts = self.journal.recover()
        for record in records:
            kind = record.get("recordKind")
            if kind == RECORD_SAMPLE:
                gen_key = "%s:%s" % (record.get("pid"), record.get("epoch"))
                gen = self.state["generations"].setdefault(gen_key, {
                    "startedAt": record.get("startedAt"),
                    "slots": {}, "tier": None})
                slot = record.get("slot")
                if slot is not None and slot not in gen["slots"]:
                    gen["slots"][str(slot)] = record
            elif kind == RECORD_TRANSIENT:
                self.state["transientCount"] = max(
                    self.state["transientCount"], int(record.get("count", 0)))
            elif kind == RECORD_FATAL:
                self.state["transientCount"] = 0
            elif kind == RECORD_STOP:
                self.state["alertStop"] = True
            elif kind == RECORD_COMPLETION:
                self.state["completed"] = True
        # 重放后重算代际 tier 与计数
        for gen in self.state["generations"].values():
            samples = [gen["slots"][k] for k in sorted(gen["slots"], key=int)]
            if len(samples) >= 2:
                gen["tier"] = classify_generation_tier(samples[0], samples[-1])
        if counts["corrupt_line"]:
            self._record(RECORD_CORRUPT_LINE, count=counts["corrupt_line"])
        if counts["duplicate"]:
            self._record(RECORD_DUPLICATE, count=counts["duplicate"])
        self._snapshot_state()

    # -- dirty-run 协议（§2.3.3 R8-B2）--

    def enter_run(self) -> str:
        """启动进入主循环前调用。返回 'run' / 'fatal-stop' / 'alert-stop' /
        'completed-stop'。"""
        # fatal / stop 标志：立即退出，不计 transient
        if os.path.exists(self.fatal_marker_path):
            return "fatal-stop"
        if os.path.exists(self.stop_marker_path):
            try:
                stop_doc = json.loads(
                    safe_read_file(self.stop_marker_path).decode("utf-8"))
            except (OSError, ValueError):
                stop_doc = {}
            if stop_doc.get("reason") == "completed":
                return "completed-stop"
            return "alert-stop"
        # 未清除的 dirty marker → 前一 run 计一次 transient
        if os.path.exists(self.dirty_marker_path):
            self.state["transientCount"] = int(self.state["transientCount"]) + 1
            self._record(RECORD_TRANSIENT, count=self.state["transientCount"])
            if self.state["transientCount"] >= TRANSIENT_FATAL_THRESHOLD:
                self._record(RECORD_FATAL,
                             reason="consecutive_transient_crashes",
                             count=self.state["transientCount"])
                atomic_write(self.fatal_marker_path, json.dumps({
                    "reason": "consecutive_transient_crashes",
                    "count": self.state["transientCount"],
                }).encode("utf-8"))
                self._clear_dirty_marker()
                self.state["transientCount"] = 0
                self._snapshot_state()
                return "fatal-stop"
            self._snapshot_state()
        # 原子写 dirty marker（run generation）
        self.state["runGeneration"] = int(self.state.get("runGeneration", 0)) + 1
        atomic_write(self.dirty_marker_path, json.dumps({
            "runGeneration": self.state["runGeneration"],
            "pid": os.getpid(),
            "startedAtWallclock": self.now(),
        }).encode("utf-8"))
        self._snapshot_state()
        return "run"

    def _clear_dirty_marker(self) -> None:
        try:
            os.unlink(self.dirty_marker_path)
        except FileNotFoundError:
            pass

    def check_healthy_milestone(self) -> None:
        """存活一个完整 discovery cycle（≥60s）且 ≥1 成功采样事务或 ≥1 完整
        idle 检查 → 清 marker + 重置连续失败计数。"""
        if self.milestone_reached or self.run_start_ts is None:
            return
        if self.now() - self.run_start_ts < DISCOVERY_INTERVAL_S:
            return
        if self.successful_samples_this_run < 1 and self.idle_checks_this_run < 1:
            return
        self._clear_dirty_marker()
        self.state["transientCount"] = 0
        self.milestone_reached = True
        self._snapshot_state()

    # -- 墙钟 elapsed / 日历日（§2.3.3 时钟跳变）--

    def observe_wallclock(self) -> None:
        now = self.now()
        last = self.state.get("lastObservedWallclock")
        if last is not None:
            d = now - last
            if d < 0:
                self._record(RECORD_CLOCK_ANOMALY, kind="backward")
            elif d > 24 * 3600:
                self._record(RECORD_CLOCK_ANOMALY, kind="forward")
            else:
                self.state["elapsedSeconds"] = self.state["elapsedSeconds"] + d
        self.state["lastObservedWallclock"] = now
        day = time.strftime("%Y-%m-%d", time.localtime(now))
        if day not in self.state["calendarDays"]:
            self.state["calendarDays"].append(day)
        self._snapshot_state()

    # -- 代际发现 --

    def _bootstrap_fingerprint(self) -> Optional[str]:
        try:
            raw = safe_read_file(self.runtime_json_path)
        except (OSError, UnsafePathError):
            return None
        return hashlib.sha256(raw).hexdigest()

    def _generation_from_sample(self, sample: Dict[str, Any]) -> str:
        return "%s:%s" % (sample["pid"], sample["epoch"])

    def _mark_missed_slots(self, gen: Dict[str, Any],
                           started_at_ts: float, now_ts: float) -> None:
        for k in range(SLOT_COUNT):
            t_k = started_at_ts + SLOT_SPACING_S * k
            if t_k + SLOT_WINDOW_S <= now_ts and str(k) not in gen["slots"]:
                if not gen.get("missedRecorded", {}).get(str(k)):
                    gen.setdefault("missedRecorded", {})[str(k)] = True
                    self._record(RECORD_MISSED, pid=gen.get("pid", 0),
                                 epoch=gen.get("epoch", 0), slot=k)

    # -- 采样槽执行 --

    def _run_slot_transaction(self, gen_key: str) -> None:
        gen = self.state["generations"][gen_key]
        started_at = gen.get("startedAt")
        recorded_started_at = started_at if started_at else None
        result, started_at_value = run_transaction(
            self.runtime_json_path, self.token_path,
            recorded_started_at=recorded_started_at,
            command_runner=self.command_runner)
        now_ts = self.now()
        if not result.valid:
            self._record_rejected(result.reason,
                                  slot=self._current_slot_hint(gen, now_ts))
            self._snapshot_state()
            return
        sample = result.sample
        if started_at is None:
            gen["startedAt"] = started_at_value
            gen["startedAtTs"] = self._parse_rfc3339(started_at_value)
        # slot 归属
        started_at_ts = gen.get("startedAtTs")
        slot = slot_for_timestamp(started_at_ts, now_ts) if started_at_ts else None
        if slot is None:
            self._record(RECORD_OUT_OF_SLOT, pid=sample["pid"],
                         epoch=sample["epoch"])
            self._snapshot_state()
            return
        if str(slot) in gen["slots"]:
            self._record(RECORD_DUPLICATE, pid=sample["pid"],
                         epoch=sample["epoch"], slot=slot)
            self._snapshot_state()
            return
        sample["slot"] = slot
        gen["slots"][str(slot)] = sample
        self._record(RECORD_SAMPLE, pid=sample["pid"], epoch=sample["epoch"],
                     slot=slot, sample=sample)
        self.successful_samples_this_run += 1
        # 代际 tier 重算（首末有效样本）
        samples = [gen["slots"][k] for k in sorted(gen["slots"], key=int)]
        if len(samples) >= 2:
            gen["tier"] = classify_generation_tier(samples[0], samples[-1])
        self._snapshot_state()
        # 告警评估（三类独立 OR）
        alerts = evaluate_alerts(samples)
        if alerts:
            self._trigger_alert_stop(alerts)
        else:
            self._check_completion()

    def _current_slot_hint(self, gen: Dict[str, Any], now_ts: float) -> int:
        started_at_ts = gen.get("startedAtTs")
        if started_at_ts is None:
            return -1
        slot = slot_for_timestamp(started_at_ts, now_ts)
        return slot if slot is not None else -1

    @staticmethod
    def _parse_rfc3339(value: str) -> Optional[float]:
        try:
            import datetime
            return datetime.datetime.strptime(
                value, "%Y-%m-%dT%H:%M:%S%z").timestamp()
        except (ValueError, TypeError):
            try:
                import datetime
                return datetime.datetime.fromisoformat(
                    value.replace("Z", "+00:00")).timestamp()
            except (ValueError, TypeError):
                return None

    def _trigger_alert_stop(self, alerts: List[str]) -> None:
        self._record(RECORD_STOP, reason="alert", alerts=alerts)
        self.state["alertStop"] = True
        atomic_write(self.stop_marker_path, json.dumps({
            "reason": "alert", "alerts": alerts,
            "wallclock": self.now(),
        }).encode("utf-8"))
        self._clear_dirty_marker()
        self._snapshot_state()

    def _check_completion(self) -> None:
        valid_gens = 0
        scan_count = 0
        for gen in self.state["generations"].values():
            if len(gen.get("slots", {})) >= VALID_GENERATION_MIN_SAMPLES:
                valid_gens += 1
            if gen.get("tier") == "scan-evidenced":
                scan_count += 1
        if completion_satisfied(
                self.state["elapsedSeconds"], len(self.state["calendarDays"]),
                valid_gens, scan_count):
            self._record(RECORD_COMPLETION, validGenerations=valid_gens,
                         scanEvidenced=scan_count,
                         elapsedSeconds=self.state["elapsedSeconds"])
            self.state["completed"] = True
            atomic_write(self.stop_marker_path, json.dumps({
                "reason": "completed",
                "wallclock": self.now(),
            }).encode("utf-8"))
            self._clear_dirty_marker()
            self._snapshot_state()

    # -- 主循环 --

    def run_forever(self) -> int:
        """返回进程退出码：0 = clean/fatal/安全失败；1 = transient（launchd 重启）。"""
        # 启动前安全验证（§2.3.1 N2：失败不写任何文件，exit 0 + stderr）
        try:
            validate_data_root(self.data_root)
        except UnsafePathError as exc:
            sys.stderr.write("memory-monitor: pre-startup safe failure: %s\n"
                             % exc.args[0])
            return 0
        # launchd 在脚本校验前已创建 monitor.log（默认 umask）；目录 0700 之外
        # 再把文件本身收紧到 0600（§2.3.1 归档权限）。
        log_path = os.path.join(self.data_root, "monitor.log")
        try:
            log_stat = os.lstat(log_path)
            if stat.S_ISREG(log_stat.st_mode) and log_stat.st_uid == os.geteuid() \
                    and log_stat.st_mode & 0o077:
                os.chmod(log_path, 0o600)
        except OSError:
            pass
        if not self.acquire_lock():
            sys.stderr.write("memory-monitor: another instance holds the lock\n")
            return 0
        try:
            self.journal.open_for_append()
            self.recover_from_journal()
            mode = self.enter_run()
            if mode != "run":
                return 0
            self.run_start_ts = self.now()
            last_fingerprint = None
            while True:
                self.observe_wallclock()
                self.check_healthy_milestone()
                fingerprint = self._bootstrap_fingerprint()
                # 指纹变化 → 新代际发现；无 active 代际（idle/此前发现失败）→
                # 每 cycle 重试发现（bootstrap 可读即试一笔，读失败即 idle 检查）。
                if fingerprint != last_fingerprint or self.current_gen_key is None:
                    last_fingerprint = fingerprint
                    self._on_generation_discovery()
                gen_key = self.current_gen_key
                if gen_key and gen_key in self.state["generations"]:
                    gen = self.state["generations"][gen_key]
                    started_at_ts = gen.get("startedAtTs")
                    if started_at_ts is not None:
                        now_ts = self.now()
                        self._mark_missed_slots(gen, started_at_ts, now_ts)
                        slot = slot_for_timestamp(started_at_ts, now_ts)
                        if slot is not None and str(slot) not in gen["slots"]:
                            self._run_slot_transaction(gen_key)
                else:
                    self.idle_checks_this_run += 1
                if self.state.get("alertStop") or self.state.get("completed"):
                    return 0
                # 测试钩子：模拟启动后立即崩溃（kill -9 形状）
                if os.environ.get("CORDCODE_MONITOR_TEST_HANG") == "1":
                    time.sleep(3600)
                    return 1
                # 测试钩子：跑完一个完整 discovery cycle 后退出（验证用）
                if os.environ.get("CORDCODE_MONITOR_TEST_ONE_CYCLE") == "1":
                    return 0
                self.sleep(DISCOVERY_INTERVAL_S)
        except KeyboardInterrupt:
            return 0
        except Exception as exc:  # transient crash：非零退出，launchd 重启
            sys.stderr.write("memory-monitor: transient crash: %s: %s\n"
                             % (type(exc).__name__, exc))
            return 1
        finally:
            self.release_lock()

    def _on_generation_discovery(self) -> None:
        """新代际发现流程：只读探测 restart policy + 立即执行首个可用 slot 事务。"""
        policy = probe_restart_policy(command_runner=self.command_runner)
        self._record(RECORD_RESTART_POLICY, policy=policy)
        self.current_gen_key = None
        # 立即执行首个可用 slot 事务（无 active 代际时先试一笔，成功即建立代际）
        result, started_at_value = run_transaction(
            self.runtime_json_path, self.token_path,
            recorded_started_at=None, command_runner=self.command_runner)
        if not result.valid:
            self._record_rejected(result.reason)
            self._snapshot_state()
            return
        sample = result.sample
        gen_key = self._generation_from_sample(sample)
        started_at_ts = self._parse_rfc3339(started_at_value or "")
        gen = self.state["generations"].setdefault(gen_key, {
            "startedAt": started_at_value, "startedAtTs": started_at_ts,
            "slots": {}, "tier": None, "pid": sample["pid"],
            "epoch": sample["epoch"]})
        self.current_gen_key = gen_key
        now_ts = self.now()
        slot = slot_for_timestamp(started_at_ts, now_ts) if started_at_ts else None
        if slot is None:
            self._record(RECORD_OUT_OF_SLOT, pid=sample["pid"],
                         epoch=sample["epoch"])
        elif str(slot) in gen["slots"]:
            self._record(RECORD_DUPLICATE, pid=sample["pid"],
                         epoch=sample["epoch"], slot=slot)
        else:
            sample["slot"] = slot
            gen["slots"][str(slot)] = sample
            self._record(RECORD_SAMPLE, pid=sample["pid"],
                         epoch=sample["epoch"], slot=slot, sample=sample)
            self.successful_samples_this_run += 1
        self._mark_missed_slots(gen, started_at_ts, now_ts) if started_at_ts else None
        self._snapshot_state()


# ---------------------------------------------------------------------------
# launchd 安装/卸载（§2.3.3）
# ---------------------------------------------------------------------------

def build_plist(install_script_path: str, data_root: str) -> Dict[str, Any]:
    return {
        "Label": PLIST_LABEL,
        "ProgramArguments": [PYTHON_BIN, install_script_path],
        "WorkingDirectory": data_root,
        "StandardOutPath": os.path.join(data_root, "monitor.log"),
        "StandardErrorPath": os.path.join(data_root, "monitor.log"),
        "KeepAlive": {"SuccessfulExit": False},
        "ThrottleInterval": 30,
    }


def cmd_install(repo_script_path: str, data_root: str = DEFAULT_DATA_ROOT) -> int:
    try:
        validate_data_root(data_root)
    except UnsafePathError as exc:
        sys.stderr.write("install: data root unsafe: %s\n" % exc.args[0])
        return 1
    install_dir = os.path.dirname(data_root)
    os.makedirs(install_dir, exist_ok=True)
    install_script = os.path.join(data_root, "monitor.py")
    shutil.copyfile(repo_script_path, install_script)
    os.chmod(install_script, 0o700)
    plist = build_plist(install_script, data_root)
    os.makedirs(os.path.dirname(PLIST_PATH), exist_ok=True)
    with open(PLIST_PATH, "wb") as handle:
        plistlib.dump(plist, handle)
    os.chmod(PLIST_PATH, 0o600)
    uid = os.getuid()
    rc = subprocess.run(
        ["launchctl", "bootstrap", "gui/%d" % uid, PLIST_PATH]).returncode
    if rc != 0:
        sys.stderr.write("install: launchctl bootstrap failed (%d)\n" % rc)
        return 1
    print("installed: %s (data root %s)" % (PLIST_LABEL, data_root))
    return 0


def cmd_uninstall() -> int:
    uid = os.getuid()
    rc = subprocess.run(
        ["launchctl", "bootout", "gui/%d" % uid, PLIST_PATH]).returncode
    if rc != 0:
        sys.stderr.write("uninstall: launchctl bootout failed (%d)\n" % rc)
        return 1
    print("uninstalled: %s (data root %s 保留，owner 决定删除)"
          % (PLIST_LABEL, DEFAULT_DATA_ROOT))
    return 0


def cmd_report(data_root: str = DEFAULT_DATA_ROOT) -> int:
    journal = Journal(data_root)
    records, counts = journal.recover()
    generations: Dict[str, Dict[str, Any]] = {}
    rejected: List[str] = []
    alerts: List[Dict[str, Any]] = []
    for record in records:
        kind = record.get("recordKind")
        if kind == RECORD_SAMPLE:
            gen_key = "%s:%s" % (record.get("pid"), record.get("epoch"))
            gen = generations.setdefault(gen_key, {"slots": {}, "startedAt": None})
            gen["slots"][record.get("slot")] = record.get("sample", {})
            if gen["startedAt"] is None:
                gen["startedAt"] = record.get("sample", {}).get("startedAt")
        elif kind == RECORD_REJECTED:
            rejected.append(str(record.get("reason")))
        elif kind == RECORD_STOP:
            alerts.append(record)
    valid_gens = 0
    scan_gens = 0
    cpu_gens = 0
    for gen_key, gen in generations.items():
        samples = [gen["slots"][k] for k in sorted(gen["slots"], key=lambda x: (x is None, x))]
        if len(samples) >= VALID_GENERATION_MIN_SAMPLES:
            valid_gens += 1
        if len(samples) >= 2:
            tier = classify_generation_tier(samples[0], samples[-1])
            if tier == "scan-evidenced":
                scan_gens += 1
            elif tier == "cpu-active-only":
                cpu_gens += 1
    print(json.dumps({
        "generations": len(generations),
        "validGenerations": valid_gens,
        "scanEvidenced": scan_gens,
        "cpuActiveOnly": cpu_gens,
        "rejected": len(rejected),
        "rejectedReasons": sorted(set(rejected)),
        "alerts": alerts,
        "journalCounts": counts,
    }, ensure_ascii=False, indent=2))
    return 0


def cmd_probe() -> int:
    """对活体 runtime 跑一笔完整六步事务并打印结果（不写 journal/数据根）。"""
    result, started_at = run_transaction(
        RUNTIME_JSON_PATH, MANAGEMENT_TOKEN_PATH)
    if result.valid:
        sample = result.sample
        print(json.dumps({
            "valid": True,
            "pid": sample["pid"], "epoch": sample["epoch"],
            "startedAt": sample["startedAt"],
            "sysMinusHeapReleased": sample["memory"]["sysMinusHeapReleased"],
            "footprint": sample["footprint"], "rss": sample["rss"],
            "cpu": sample["cpu"], "scanCounters": sample["scanCounters"],
        }, ensure_ascii=False, indent=2))
        return 0
    print(json.dumps({"valid": False, "reason": result.reason},
                     ensure_ascii=False))
    return 0


def main(argv: List[str]) -> int:
    command = argv[1] if len(argv) > 1 else "run"
    data_root = DEFAULT_DATA_ROOT
    args = argv[2:]
    if "--data-root" in args:
        idx = args.index("--data-root")
        if idx + 1 < len(args):
            data_root = args[idx + 1]
    # 测试钩子（仅测试使用；生产 plist 不设这些环境变量）
    runtime_json = os.environ.get(
        "CORDCODE_MONITOR_TEST_RUNTIME_JSON", RUNTIME_JSON_PATH)
    token_path = os.environ.get(
        "CORDCODE_MONITOR_TEST_TOKEN", MANAGEMENT_TOKEN_PATH)
    if command in ("run",):
        monitor = Monitor(data_root, runtime_json_path=runtime_json,
                          token_path=token_path)
        return monitor.run_forever()
    if command == "probe":
        return cmd_probe()
    if command == "install":
        return cmd_install(os.path.abspath(__file__), data_root)
    if command == "uninstall":
        return cmd_uninstall()
    if command == "report":
        return cmd_report(data_root)
    sys.stderr.write("unknown command: %s\n" % command)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
