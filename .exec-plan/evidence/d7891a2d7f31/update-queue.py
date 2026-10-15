"""Persist one execution-queue transition; internal evidence tooling only."""
import datetime
import json
import sys
from pathlib import Path

root = Path(__file__).resolve().parents[3]
path = root / '.exec-plan/state/plan-d7891a2d7f31.json'
state = json.loads(path.read_text())
todo = next(t for t in state['todos'] if t['id'] == sys.argv[1])
status = sys.argv[2]
assert status in ('pending', 'in_progress', 'done', 'blocked')
if status == 'done':
    assert len(sys.argv) > 4, 'completion needs summary and artifacts'
    artifacts = sys.argv[4:]
    for artifact in artifacts:
        assert artifact.startswith(('git:', 'cmd:')) or (root / artifact).exists(), artifact
    todo['verification'].update(status='present', summary=sys.argv[3], artifacts=artifacts,
                                checked_at=datetime.datetime.now(datetime.timezone.utc).isoformat())
elif status == 'blocked':
    todo['notes'] = sys.argv[3]
todo['status'] = status
state['updated_at'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
temporary = path.with_suffix('.tmp')
temporary.write_text(json.dumps(state, ensure_ascii=False, indent=2) + '\n')
temporary.replace(path)
print(todo['id'], status)
