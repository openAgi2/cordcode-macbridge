#!/usr/bin/env python3
"""Sanitize DSH Gate 0 dumps and assert post-sanitize semantic consistency.

Round-3 audit P0-2: a line-level regex cannot catch paths split across token
deltas, and breaking the chunk↔assembled equivalence invalidates the §3.7
"8/8 equal" evidence. This version:

  1. scrubs host-absolute paths in each JSON line (continuous strings);
  2. after rewrite, asserts per (turn,step): chunk text/reasoning concatenation
     == assembled message text/reasoning, and chunk usage == assembled usage;
  3. asserts no host path remains in any string value (not just per-line grep).

The dumps regenerated with path-free prompts keep paths out of deltas, so a
continuous-string scrub is sufficient and does not break equivalence.
"""
import glob
import json
import re
import sys

PATTERNS = [
    (re.compile(r'/private/var/folders/[A-Za-z0-9_/.-]+/T/dsh-conn-cwd-\d+'), '<CWD>'),
    (re.compile(r'/var/folders/[A-Za-z0-9_/.-]+/T/dsh-conn-cwd-\d+'), '<CWD>'),
    (re.compile(r'/tmp/dsh-(?:dump-test|sandbox-probe)\.txt'), '<TMPFILE>'),
]
LEAK = re.compile(r'/private/var/folders|/var/folders/[A-Za-z0-9]+/[A-Za-z0-9]+/|/Users/[A-Za-z]|/tmp/dsh-')


def scrub_str(s):
    if not isinstance(s, str):
        return s
    for pat, rep in PATTERNS:
        s = pat.sub(rep, s)
    return s


def walk_scrub(obj):
    if isinstance(obj, str):
        return scrub_str(obj)
    if isinstance(obj, dict):
        return {k: walk_scrub(v) for k, v in obj.items()}
    if isinstance(obj, list):
        return [walk_scrub(x) for x in obj]
    return obj


def assert_consistency(events, fname):
    """Per (turn,step): chunk delta concat == assembled; usage double-source equal."""
    import collections
    steps = collections.defaultdict(lambda: {'text': '', 'reason': '', 'atext': '', 'areason': '', 'cusage': None, 'ausage': None})
    for ev in events:
        e = ev.get('event', {})
        t = e.get('type'); d = e.get('data', {})
        if t == 'assistant/chunk':
            key = (d.get('turn'), d.get('step')); c = d.get('chunk', {})
            if c.get('type') == 'text-delta':
                steps[key]['text'] += c.get('text', '')
            elif c.get('type') == 'reasoning-delta':
                steps[key]['reason'] += c.get('text', '')
            elif c.get('type') == 'usage':
                steps[key]['cusage'] = c.get('usage')
        elif t == 'assistant/message':
            key = (d.get('turn'), d.get('step'))
            for b in d.get('message', {}).get('content', []):
                if b.get('type') == 'text':
                    steps[key]['atext'] += b.get('text', '')
                elif b.get('type') == 'reasoning':
                    steps[key]['areason'] += b.get('text', '')
            if 'usage' in d:
                steps[key]['ausage'] = d.get('usage')
    bad = []
    for k, s in steps.items():
        # peer-existence assertions (round4 P1): a step with content/usage delta
        # MUST have an assembled peer; missing peer means lost frames, not success.
        if s['text'] and not s['atext']:
            bad.append(f'{fname} step{k}: text delta present but assembled text missing (peer lost)')
        if s['reason'] and not s['areason']:
            bad.append(f'{fname} step{k}: reasoning delta present but assembled reasoning missing')
        if s['cusage'] and not s['ausage']:
            bad.append(f'{fname} step{k}: chunk usage present but assembled usage missing')
        if s['atext'] and s['text'] != s['atext']:
            bad.append(f'{fname} step{k}: text chunk!=assembled ({len(s["text"])} vs {len(s["atext"])})')
        if s['areason'] and s['reason'] != s['areason']:
            bad.append(f'{fname} step{k}: reasoning chunk!=assembled')
        if s['cusage'] and s['ausage'] and s['cusage'] != s['ausage']:
            bad.append(f'{fname} step{k}: usage chunk!=assembled')
    return bad


def main():
    files = sorted(glob.glob('dumps/*.jsonl'))
    if not files:
        print('no dumps/*.jsonl'); return 1
    all_bad = []
    for f in files:
        lines = open(f, encoding='utf-8').read().splitlines()
        out = []
        for line in lines:
            if not line.strip():
                out.append(line); continue
            obj = json.loads(line)
            obj = walk_scrub(obj)          # scrub all string values recursively
            out.append(json.dumps(obj, ensure_ascii=False))
        text = '\n'.join(out) + '\n'
        open(f, 'w', encoding='utf-8').write(text)
        events = [json.loads(l) for l in out if l.strip()]
        all_bad += assert_consistency(events, f)
        if LEAK.search(text):
            all_bad.append(f'{f}: host path remains after sanitize')
    print('sanitized:', len(files), 'files')
    if all_bad:
        print('CONSISTENCY/LEAK FAILURES:')
        for b in all_bad:
            print('  -', b)
        return 2
    print('assert: chunk==assembled, usage double-source equal, no host path — ALL PASS')
    return 0


if __name__ == '__main__':
    sys.exit(main())
