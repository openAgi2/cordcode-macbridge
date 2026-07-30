# Claude source-shape fixtures（合成；IR-6）

> **作者归属**：agent 生成。**全部为手工合成样本，零用户内容**——uuid/message.id/timestamp/body 均为占位符，按 `2026-07-30-remote-web-…-investigation.md` 观察到的形状构造。不来自任何真实 `~/.claude` transcript。
> 用途：H3/H4 修复的定向测试 fixture + source-shape 行为表（IR-5）验证。CI 测这些 fixture，**不**测当前 `~/.claude`。

## Fixture 清单（manifest）

| file | shape category | 关键关系 | 期望 mapper 行为（IR-1 v1 policy） |
|---|---|---|---|
| `exact-text-replay.jsonl` | H3 exact replay | row2=row1 同 `uuid`+`message.id`+`timestamp`+body，仅 `parentUuid` 不同（reparent 到 turn B） | 第 2 occurrence = `accepted_source_only`；同 graph-resolved turn 内已含该 UUID → content no-op（不得 X+X） |
| `tool-result-extension.jsonl` | tool_result 1→2 blocks | 同 `uuid` tool_result，第 2 occurrence 增 1 block（`call-1` 不变） | 只 reduce 新增 block；不重发首个 result |
| `branch-fileorder-interleave.jsonl` | H4 file-order ≠ parent-chain | `a-2` 的 file-order currentTurn=`u-B`（最近 user），但 parent-chain `a-2→a-1→u-A` → turn A | turn placement = parent-chain user（A），**不**用 file-order（B） |

每条 fixture 均为合法 Claude JSONL（`type`/`uuid`/`message.{id,role,content}`/`parentUuid`/`timestamp`），LF 结尾、完整 record。

## 复算脚本（live `~/.claude` 统计，owner 本地核对用；非 CI）

以下脚本读取 `~/.claude/projects/**/*.jsonl` 复算 H3/H4 的 corpus 统计。`~/.claude` 持续写入，**输出必须绑定采样时间**（见 IR-7 manifest 字段），不得当永久常量。

```python
import json, glob, os, collections
files = glob.glob(os.path.expanduser("~/.claude/projects/**/*.jsonl"), recursive=True)
def text_of(rec):
    msg = rec.get("message") if isinstance(rec.get("message"), dict) else None
    if not msg: return None
    c = msg.get("content")
    if isinstance(c, list):
        ts = [b.get("text","") for b in c if isinstance(b,dict) and b.get("type")=="text"]
        if ts: return "".join(ts)
    return None
# (a) same-file top-level UUID reuse (H3 replay phenomenon)
uuid_groups = collections.Counter(); uuid_type = collections.Counter()
for fp in files:
    byuuid = collections.defaultdict(list)
    try:
        for line in open(fp, errors="replace"):
            line=line.strip()
            if not line: continue
            try: rec=json.loads(line)
            except: continue
            u=rec.get("uuid")
            if isinstance(u,str) and u: byuuid[u].append(rec)
    except: continue
    for u, recs in byuuid.items():
        if len(recs)>1 and len(set(json.dumps(r,sort_keys=True) for r in recs))>1:
            uuid_groups[os.path.basename(fp)] += 1
            msg=recs[0].get("message",{}) if isinstance(recs[0].get("message"),dict) else {}
            c=msg.get("content"); kinds=set()
            if isinstance(c,list):
                for b in c:
                    if isinstance(b,dict): kinds.add(b.get("type","?"))
            uuid_type["attachment" if "attachment" in kinds else ("tool" if kinds&{"tool_use","tool_result"} else ("text" if "text" in kinds else "other"))] += 1
print("same-file top-level UUID reuse groups:", sum(uuid_groups.values()), "files:", len(uuid_groups), "by type:", dict(uuid_type))
# (b) file-order vs parent-chain user mismatch (H4) — index ALL rows so chain traverses structural nodes
total=0; mismatch=0
for fp in files:
    byuuid={}; order=[]
    try:
        for i,line in enumerate(open(fp, errors="replace")):
            line=line.strip()
            if not line: continue
            try: rec=json.loads(line)
            except: continue
            u=rec.get("uuid")
            if isinstance(u,str) and u: byuuid[u]={"role":rec.get("type"),"parent":rec.get("parentUuid")}
    except: continue
    fo_user=None
    try:
        for line in open(fp, errors="replace"):
            line=line.strip()
            if not line: continue
            try: rec=json.loads(line)
            except: continue
            role=rec.get("type")
            if role not in ("user","assistant"): continue
            msg=rec.get("message") if isinstance(rec.get("message"),dict) else None
            ut=None
            if msg:
                c=msg.get("content")
                if isinstance(c,list):
                    ts=[b.get("text","") for b in c if isinstance(b,dict) and b.get("type")=="text"]
                    if ts: ut="".join(ts)
            if role=="user" and ut and ut.strip(): fo_user=rec.get("uuid"); continue
            if role!="assistant": continue
            puid=rec.get("parentUuid"); chain=None; seen=set()
            while puid and puid not in seen:
                seen.add(puid); pr=byuuid.get(puid)
                if not pr: break
                if pr["role"]=="user": chain=puid; break
                puid=pr["parent"]
            if chain is None: continue
            total+=1
            if fo_user!=chain: mismatch+=1
    except: continue
print("H4: resolvable=", total, " file-order!=parent-chain=", mismatch)
```

> **口径漂移说明**：H4 的精确 mismatch 数依赖「file-order currentTurnID 推进条件」（哪些 user row 推进、resume/internal row 是否跳过）。本脚本用「非空 text user row 推进」的保守口径，结果会与按 mapper 真实语义（跳过 internal compact / resume meta）的 audit 口径不同。**以 fixture + 单元测试为准**，不以 live corpus 动态总数为准。
