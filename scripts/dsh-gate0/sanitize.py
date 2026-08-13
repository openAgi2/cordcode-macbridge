#!/usr/bin/env python3
"""Sanitize DSH Gate 0 dumps: replace host-absolute paths with fixed placeholders,
preserving JSON field shape and nesting. Run before committing dumps.

Targets (from round-2 audit):
  - /private/var/folders/.../T/dsh-conn-cwd-NNN  (macOS mkdtemp cwd)  -> <CWD>
  - /var/folders/.../T/dsh-conn-cwd-NNN (symlink form)                -> <CWD>
  - /tmp/dsh-dump-test.txt, /tmp/dsh-sandbox-probe.txt (probe paths)  -> <TMPFILE>

Idempotent. Verifies no host path remains after rewrite.
"""
import glob
import re
import sys

PATTERNS = [
    (re.compile(r'/private/var/folders/[A-Za-z0-9_/.-]+/T/dsh-conn-cwd-\d+'), '<CWD>'),
    (re.compile(r'/var/folders/[A-Za-z0-9_/.-]+/T/dsh-conn-cwd-\d+'), '<CWD>'),
    (re.compile(r'/tmp/dsh-(?:dump-test|sandbox-probe)\.txt'), '<TMPFILE>'),
]
LEAK = re.compile(r'/private/var/folders|/var/folders/[A-Za-z0-9]+/[A-Za-z0-9]+/|/Users/[A-Za-z]')

def main():
    files = sorted(glob.glob('dumps/*.jsonl'))
    if not files:
        print('no dumps/*.jsonl found'); return 1
    bad = []
    for f in files:
        lines = open(f, encoding='utf-8').read().splitlines(keepends=True)
        out = []
        for line in lines:
            for pat, rep in PATTERNS:
                line = pat.sub(rep, line)
            out.append(line)
        text = ''.join(out)
        open(f, 'w', encoding='utf-8').write(text)
        if LEAK.search(text):
            bad.append(f)
    # verify
    print('sanitized:', len(files), 'files')
    if bad:
        print('STILL LEAK host path in:', bad); return 2
    print('verify: no host path remains')
    return 0

if __name__ == '__main__':
    sys.exit(main())
