import assert from 'node:assert/strict';
import test from 'node:test';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

test('renders test and build output verbatim while leaving metadata out of logs', () => {
  const events = [
    { Action: 'start', Package: 'fixture' },
    { Action: 'output', Output: '=== RUN   TestExample\n' },
    { Action: 'attr', Key: 'acceptance_mode', Value: 'nonblocking' },
    { Action: 'output', Output: '    fixture_test.go:12: "hello" \\ 世界\nsecond line\n' },
    { Action: 'build-output', Output: 'fixture.go:1: build diagnostic\n' },
    { Action: 'output', Output: 'partial' },
    { Action: 'output', Output: ' line\n' },
    { Action: 'output', Output: '--- FAIL: TestExample (0.00s)\n' },
    { Action: 'fail', Package: 'fixture' },
  ];

  const run = spawnSync(process.execPath, [fileURLToPath(new URL('./test-output.mjs', import.meta.url))], {
    input: events.map(event => JSON.stringify(event)).join('\n'), encoding: 'utf8', env: {},
  });

  assert.equal(run.status, 0, run.stderr);
  assert.equal(run.stdout, events.map(event => event.Output ?? '').join(''));
});
