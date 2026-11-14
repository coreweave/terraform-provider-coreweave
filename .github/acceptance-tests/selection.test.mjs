import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtempSync, writeFileSync, readFileSync, rmSync, mkdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';

const root = new URL('../../', import.meta.url);

const outputEvent = { Action: 'output', Package: 'fixture', Output: '=== RUN   TestNonBlockingAcceptance\n' };
const events = [outputEvent, { Action: 'pass', Package: 'fixture' }];
const goJSON = events.map(event => JSON.stringify(event)).join('\n') + '\n';

function make(target, { suites = 'cks networking', malformed = false, unwritable = false, streaming = false } = {}) {
  const dir = mkdtempSync(join(tmpdir(), 'acceptance-selection-'));
  const log = join(dir, 'commands');
  const results = join(dir, 'results');
  if (unwritable) mkdirSync(join(results, 'networking.json'), { recursive: true });
  writeFileSync(join(dir, 'go'), `#!/bin/sh
case "$1" in env) echo fixture; exit 0;; esac
printf '%s|%s\\n' "$TF_ACC" "$*" >> "$COMMAND_LOG"
printf '%s' '${malformed ? 'not JSON\n' : goJSON}'
${streaming ? `node -e '
const { existsSync } = require("node:fs");
const deadline = setTimeout(() => process.exit(1), 5000);
const timer = setInterval(() => {
  if (existsSync(process.argv[1])) { clearTimeout(deadline); clearInterval(timer); }
}, 10);
' "$RELEASE_FILE" || exit 1` : ''}
case "$*" in *coreweave/cks*) exit 1;; esac
`, { mode: 0o755 });
  const run = (streaming ? spawn : spawnSync)('make', [target, `SUITES=${suites}`, 'TEST_ACC_TIMEOUT=7m',
    'TEST_ACC_PARALLEL=3', `NONBLOCKING_RESULTS_DIR=${results}`], {
    cwd: root, encoding: 'utf8',
    env: { PATH: `${dir}:${process.env.PATH}`, COMMAND_LOG: log, TF_ACC: '', RELEASE_FILE: join(dir, 'release') },
  });
  return { dir, log, results, run };
}

for (const target of ['test', 'testacc']) {
  test(`${target} runs normally without nonblocking tags or exclusion filters`, () => {
    const fixture = make(target);
    try {
      assert.doesNotMatch(readFileSync(fixture.log, 'utf8'), /-skip|-tags|-run/);
    } finally { rmSync(fixture.dir, { recursive: true, force: true }); }
  });
}

test('non-blocking selection records JSON for every suite and propagates failure', () => {
  const fixture = make('testacc-nonblocking');
  try {
    assert.notEqual(fixture.run.status, 0);
    const commands = readFileSync(fixture.log, 'utf8').trim().split('\n');
    assert.equal(commands.length, 2);
    for (const command of commands) {
      assert.match(command, /^1\|test /);
      assert.match(command, /-run \^TestNonBlockingAcceptance\$/);
      assert.match(command, /-tags=nonblocking/);
      assert.match(command, /-json/);
      assert.match(command, /-count=1/);
      assert.match(command, /-timeout=7m/);
      assert.match(command, /-parallel=3/);
      assert.doesNotMatch(command, /-skip/);
    }
    for (const suite of ['cks', 'networking']) {
      assert.equal(readFileSync(join(fixture.results, `${suite}.json`), 'utf8'), goJSON);
    }
    assert.equal(fixture.run.stdout, outputEvent.Output.repeat(2));
  } finally { rmSync(fixture.dir, { recursive: true, force: true }); }
});

test('passing non-blocking suites still succeed', () => {
  const fixture = make('testacc-nonblocking', { suites: 'networking' });
  try {
    assert.equal(fixture.run.status, 0, fixture.run.stderr);
    assert.equal(fixture.run.stdout, outputEvent.Output);
  } finally { rmSync(fixture.dir, { recursive: true, force: true }); }
});

for (const failure of ['malformed', 'unwritable']) {
  test(`non-blocking logging failure (${failure}) fails a passing test command`, () => {
    const fixture = make('testacc-nonblocking', { suites: 'networking', [failure]: true });
    try {
      assert.notEqual(fixture.run.status, 0);
    } finally { rmSync(fixture.dir, { recursive: true, force: true }); }
  });
}

test('non-blocking logs stream before the test command finishes', async () => {
  const fixture = make('testacc-nonblocking', { suites: 'networking', streaming: true });
  let stdout = '';
  let stderr = '';
  fixture.run.stdout.setEncoding('utf8');
  fixture.run.stdout.on('data', chunk => {
    stdout += chunk;
    if (stdout.includes(outputEvent.Output)) writeFileSync(join(fixture.dir, 'release'), '');
  });
  fixture.run.stderr.setEncoding('utf8');
  fixture.run.stderr.on('data', chunk => { stderr += chunk; });
  try {
    const [code] = await once(fixture.run, 'close');
    assert.equal(code, 0, stderr);
    assert.equal(stdout, outputEvent.Output);
    assert.equal(readFileSync(join(fixture.results, 'networking.json'), 'utf8'), goJSON);
  } finally { rmSync(fixture.dir, { recursive: true, force: true }); }
});
