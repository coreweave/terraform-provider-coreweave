import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtempSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const workflow = readFileSync(new URL('../workflows/release.yml', import.meta.url), 'utf8');
const command = workflow.match(/- name: Publish completed release[\s\S]*?\n        run: (.+)/)[1];
const cwd = fileURLToPath(new URL('../../', import.meta.url));
const notes = '## [1.2.3]\n\n### Features\n\n* Current change\n';
const release = { tag_name: 'v1.2.3', draft: true, body: `${notes}\n` };

function run(t, overrides = {}) {
  const dir = mkdtempSync(join(tmpdir(), 'release-publish-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  writeFileSync(join(dir, 'release-notes.md'), overrides.notes ?? notes);
  writeFileSync(join(dir, 'response.json'), JSON.stringify(overrides.release ?? release));
  const calls = join(dir, 'calls');
  writeFileSync(calls, '');
  writeFileSync(join(dir, 'mock.mjs'), `
    import { appendFileSync, readFileSync } from 'node:fs';
    const args = process.argv.slice(2);
    appendFileSync(process.env.CALLS, JSON.stringify(args) + '\\n');
    if (args[0] === 'api' && args[1] === 'repos/public/fixture/releases/tags/v1.2.3') {
      if (process.env.LOOKUP_EXIT !== '0') process.exit(Number(process.env.LOOKUP_EXIT));
      console.log(readFileSync(process.env.RESPONSE, 'utf8'));
    } else if (JSON.stringify(args) === JSON.stringify(['release', 'edit', 'v1.2.3', '--repo', 'public/fixture', '--draft=false'])) {
      process.exit(Number(process.env.PUBLISH_EXIT));
    } else {
      console.error('Unexpected gh invocation', args); process.exit(99);
    }
  `);
  writeFileSync(join(dir, 'gh'), '#!/bin/sh\nexec "$NODE" "$MOCK" "$@"\n', { mode: 0o755 });
  const result = spawnSync('bash', ['-c', command], { cwd, encoding: 'utf8', env: { ...process.env,
    PATH: `${dir}:${process.env.PATH}`, NODE: process.execPath, MOCK: join(dir, 'mock.mjs'),
    RESPONSE: join(dir, 'response.json'), CALLS: calls, LOOKUP_EXIT: String(overrides.lookupExit ?? 0),
    PUBLISH_EXIT: String(overrides.publishExit ?? 0), RUNNER_TEMP: dir,
    GITHUB_REPOSITORY: 'public/fixture', RELEASE_TAG: 'v1.2.3' } });
  return { ...result, calls: readFileSync(calls, 'utf8').trim().split('\n').filter(Boolean).map(line => JSON.parse(line)) };
}

test('the workflow publishes a matching draft after reading its notes, allowing trailing newlines', t => {
  const result = run(t);
  assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(result.calls, [
    ['api', 'repos/public/fixture/releases/tags/v1.2.3'],
    ['release', 'edit', 'v1.2.3', '--repo', 'public/fixture', '--draft=false'],
  ]);
});

test('a blank draft description cannot be published', t => {
  const result = run(t, { release: { ...release, body: '\n' } });
  assert.notEqual(result.status, 0);
  assert.ok(result.calls.every(args => args[0] !== 'release'));
});

test('mismatched, malformed, unrelated and published releases cannot be published', t => {
  for (const metadata of [{ ...release, body: 'Other notes' }, { ...release, body: null },
    { ...release, body: 123 }, { ...release, draft: false }, { ...release, draft: 'true' },
    { ...release, tag_name: 'v1.2.2' }, {}]) {
    const result = run(t, { release: metadata });
    assert.notEqual(result.status, 0, JSON.stringify(metadata));
    assert.ok(result.calls.every(args => args[0] !== 'release'), JSON.stringify(metadata));
  }
});

test('empty expected notes and lookup failures cannot authorize publication', t => {
  for (const overrides of [{ notes: '' }, { notes: ' \n\t\n' }, { lookupExit: 1 }]) {
    const result = run(t, overrides);
    assert.notEqual(result.status, 0, JSON.stringify(overrides));
    assert.ok(result.calls.every(args => args[0] !== 'release'));
  }
});

test('publication errors fail the workflow step', t => {
  const result = run(t, { publishExit: 1 });
  assert.notEqual(result.status, 0);
  assert.equal(result.calls.at(-1)[0], 'release');
});
