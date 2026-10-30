import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtempSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';

const root = new URL('../../', import.meta.url);

function make(target) {
  const dir = mkdtempSync(join(tmpdir(), 'acceptance-selection-'));
  const log = join(dir, 'commands');
  const results = join(dir, 'results');
  writeFileSync(join(dir, 'go'), `#!/bin/sh
case "$1" in env) echo fixture; exit 0;; esac
printf '%s|%s\\n' "$TF_ACC" "$*" >> "$COMMAND_LOG"
printf '%s\\n' '{"Action":"pass","Package":"fixture"}'
case "$*" in *coreweave/cks*) exit 1;; esac
`, { mode: 0o755 });
  const run = spawnSync('make', [target, 'SUITES=cks networking', 'TEST_ACC_TIMEOUT=7m',
    'TEST_ACC_PARALLEL=3', `REGRESSION_RESULTS_DIR=${results}`], {
    cwd: root, encoding: 'utf8',
    env: { ...process.env, PATH: `${dir}:${process.env.PATH}`, COMMAND_LOG: log, TF_ACC: '' },
  });
  return { dir, log, results, run };
}

for (const target of ['test', 'testacc']) {
  test(`${target} excludes the known issue group`, () => {
    const fixture = make(target);
    try {
      assert.match(readFileSync(fixture.log, 'utf8'), /-skip \^TestKnownRegressions\$/);
    } finally { rmSync(fixture.dir, { recursive: true, force: true }); }
  });
}

test('regression selection records JSON for every suite and propagates failure', () => {
  const fixture = make('testacc-regressions');
  try {
    assert.notEqual(fixture.run.status, 0);
    const commands = readFileSync(fixture.log, 'utf8').trim().split('\n');
    assert.equal(commands.length, 2);
    for (const command of commands) {
      assert.match(command, /^1\|test /);
      assert.match(command, /-run \^TestKnownRegressions\$/);
      assert.match(command, /-json/);
      assert.match(command, /-count=1/);
      assert.match(command, /-timeout=7m/);
      assert.match(command, /-parallel=3/);
      assert.doesNotMatch(command, /-skip/);
    }
    for (const suite of ['cks', 'networking']) {
      assert.equal(JSON.parse(readFileSync(join(fixture.results, `${suite}.json`), 'utf8')).Action, 'pass');
    }
  } finally { rmSync(fixture.dir, { recursive: true, force: true }); }
});
