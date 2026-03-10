# dumpstore Ansible callback plugin — outputs one NDJSON line per task result.
# Ansible finds this automatically because it lives in callback_plugins/ next to the playbooks.
from __future__ import annotations
import json, sys
from ansible.plugins.callback import CallbackBase

DOCUMENTATION = '''
    name: ndjson
    type: stdout
    short_description: One JSON line per task result, flushed immediately.
    description:
      - Used by dumpstore to stream Ansible task progress line by line.
'''

class CallbackModule(CallbackBase):
    CALLBACK_TYPE = 'stdout'
    CALLBACK_NAME = 'ndjson'
    CALLBACK_NEEDS_ENABLED = False

    def _emit(self, task_name, status, result_dict):
        msg = result_dict.get('msg', '')
        stdout = result_dict.get('stdout', '')
        if not msg and status in ('failed', 'unreachable'):
            parts = [result_dict.get('stderr', ''), stdout]
            msg = ' '.join(p for p in parts if p).strip()
        sys.stdout.write(json.dumps({'task': task_name, 'status': status, 'msg': msg, 'stdout': stdout}) + '\n')
        sys.stdout.flush()

    def v2_runner_on_ok(self, result):
        status = 'changed' if result._result.get('changed') else 'ok'
        self._emit(result.task_name, status, result._result)

    def v2_runner_on_failed(self, result, ignore_errors=False):
        self._emit(result.task_name, 'failed', result._result)

    def v2_runner_on_skipped(self, result):
        self._emit(result.task_name, 'skipped', result._result)

    def v2_runner_on_unreachable(self, result):
        self._emit(result.task_name, 'unreachable', result._result)
