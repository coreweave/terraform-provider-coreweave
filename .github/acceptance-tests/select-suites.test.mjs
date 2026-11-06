import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtempSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { selectSuites } from './select-suites.mjs';

const head = 'a'.repeat(40);
const repository = 'public/fixture';
const expectedSuites = ['caller_identity', 'cks', 'container_registry', 'inference', 'networking', 'object_storage', 'observability'];
const pr = (overrides = {}) => ({ state: 'open', head: { sha: head, ref: 'renovate/dependencies', repo: { full_name: repository } },
  base: { ref: 'main', repo: { full_name: repository } }, ...overrides });
const release = () => pr({ head: { ...pr().head, ref: 'release-please--branches--main' } });
const select = metadata => selectSuites(metadata, head, repository);

test('ordinary and Renovate PRs skip QA even when labels or titles request it', () => {
  for (const ref of ['renovate/dependencies', 'fix/provider', 'docs/update']) {
    assert.deepEqual(select(pr({ head: { ...pr().head, ref }, title: 'fix(cks): change provider',
      labels: [{ name: 'acceptance-test:all' }] })), []);
  }
});

test('release PRs always select all suites independent of changed files, title or labels', () => {
  assert.deepEqual(select(release()), expectedSuites);
  assert.deepEqual(select({ ...release(), title: 'chore: release', labels: [] }), expectedSuites);
});

test('forks, other target branches and similarly named branches do not request release QA', () => {
  assert.deepEqual(select({ ...release(), head: { ...release().head, repo: { full_name: 'fork/fixture' } } }), []);
  assert.deepEqual(select({ ...release(), base: { ...release().base, ref: 'other' } }), []);
  assert.deepEqual(select({ ...release(), base: { ...release().base, repo: { full_name: 'other/repo' } } }), []);
  assert.deepEqual(select({ ...release(), head: { ...release().head, ref: 'release-please--branches--main-extra' } }), []);
});

test('closed, stale and incomplete metadata fail instead of silently skipping QA', () => {
  for (const metadata of [pr({ state: 'closed' }), pr({ head: { ...pr().head, sha: 'b'.repeat(40) } }),
    pr({ head: null }), pr({ base: null }), pr({ head: { sha: head } }), null]) {
    assert.throws(() => select(metadata));
  }
  assert.throws(() => selectSuites(pr(), '', repository));
  assert.throws(() => selectSuites(pr(), head, ''));
});

test('the CLI emits outputs only after successful selection', t => {
  const dir = mkdtempSync(join(tmpdir(), 'acceptance-selection-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const metadata = join(dir, 'pr.json');
  const output = join(dir, 'output');
  const summary = join(dir, 'summary');
  const run = () => spawnSync(process.execPath, [fileURLToPath(new URL('./select-suites.mjs', import.meta.url)), metadata, head, repository], {
    env: { ...process.env, GITHUB_OUTPUT: output, GITHUB_STEP_SUMMARY: summary }, encoding: 'utf8',
  });
  for (const value of [JSON.stringify(pr({ state: 'closed' })), '{invalid']) {
    writeFileSync(output, ''); writeFileSync(summary, ''); writeFileSync(metadata, value);
    assert.notEqual(run().status, 0);
    assert.equal(readFileSync(output, 'utf8'), '');
    assert.equal(readFileSync(summary, 'utf8'), '');
  }
  for (const [metadataValue, suites] of [[pr(), []], [release(), expectedSuites]]) {
    writeFileSync(output, ''); writeFileSync(summary, ''); writeFileSync(metadata, JSON.stringify(metadataValue));
    assert.equal(run().status, 0);
    assert.equal(readFileSync(output, 'utf8'), `suites=${JSON.stringify(suites)}\n`);
    assert.match(readFileSync(summary, 'utf8'), new RegExp(head));
  }
});
