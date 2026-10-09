import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtempSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { allSuites, selectSuites } from './select-suites.mjs';

const head = 'a'.repeat(40);
const pr = (overrides = {}) => ({ state: 'open', head: { sha: head }, title: 'ci: update workflow', labels: [], ...overrides });
const select = (metadata = pr(), files = [], subjects = []) => selectSuites(metadata, head, files, subjects);
const everySuite = [...allSuites].sort();

test('root module and shared provider changes always require every suite despite narrow labels', () => {
  for (const file of ['go.mod', 'go.sum', 'main.go', 'GNUmakefile', 'internal/testutil/helpers.go', 'internal/provider/provider.go', 'coreweave/client.go', 'coreweave/retry_test.go', 'coreweave/s3.go']) {
    assert.deepEqual(select(pr({ labels: [{ name: 'acceptance-test:cks' }] }), [file]), everySuite, file);
  }
});

test('service paths, current labels, title and commit scopes combine without duplicates', () => {
  assert.deepEqual(select(pr({ title: 'fix(cks)!: adjust schema', labels: [{ name: 'acceptance-test:inference' }, { name: 'unrelated' }] }),
    ['coreweave/networking/resource.go', 'coreweave/cks/resource.go'], ['test(container_registry): update coverage']),
  ['cks', 'container_registry', 'inference', 'networking']);
  assert.deepEqual(select(pr({ labels: [{ name: 'acceptance-test:all' }] })), everySuite);
  for (const suite of allSuites) {
    assert.deepEqual(select(pr(), [`coreweave/${suite}/resource_test.go`]), [suite]);
    assert.deepEqual(select(pr({ labels: [{ name: `acceptance-test:${suite}` }] })), [suite]);
  }
});

test('a rerun can use added or removed labels and the edited live title', () => {
  assert.deepEqual(select(pr({ labels: [{ name: 'acceptance-test:object_storage' }] })), ['object_storage']);
  assert.deepEqual(select(pr({ title: 'test(networking): validate behavior' })), ['networking']);
  assert.deepEqual(select(pr()), []);
});

test('documentation, workflow changes and irrelevant labels select no QA suites', () => {
  assert.deepEqual(select(pr({ labels: [{ name: 'ready' }, { name: 'acceptance-test:unknown' }] }),
    ['README.md', 'docs/index.md', '.github/workflows/acceptance-tests.yaml', 'coreweave/cks-extra/resource.go']), []);
});

test('closed, stale and malformed metadata fail closed instead of emitting an empty selection', () => {
  for (const metadata of [pr({ state: 'closed' }), pr({ head: { sha: 'b'.repeat(40) } }), pr({ head: null }),
    pr({ title: null }), pr({ labels: null }), pr({ labels: [{}] }), null]) {
    assert.throws(() => select(metadata));
  }
  assert.throws(() => selectSuites(pr(), '', [], []));
});

test('the workflow command emits matrix and summary only after successful selection', () => {
  const dir = mkdtempSync(join(tmpdir(), 'acceptance-selection-'));
  try {
    const files = join(dir, 'files.txt');
    const subjects = join(dir, 'subjects.txt');
    const metadata = join(dir, 'pr.json');
    const output = join(dir, 'output');
    const summary = join(dir, 'summary');
    writeFileSync(files, 'go.sum\n');
    writeFileSync(subjects, 'deps: update dependencies\n');
    const run = () => spawnSync(process.execPath, [fileURLToPath(new URL('./select-suites.mjs', import.meta.url)), metadata, head, files, subjects], {
      env: { ...process.env, GITHUB_OUTPUT: output, GITHUB_STEP_SUMMARY: summary }, encoding: 'utf8',
    });
    for (const value of [JSON.stringify(pr({ state: 'closed' })), JSON.stringify(pr({ head: { sha: 'b'.repeat(40) } })), '{invalid']) {
      writeFileSync(output, '');
      writeFileSync(summary, '');
      writeFileSync(metadata, value);
      assert.notEqual(run().status, 0);
      assert.equal(readFileSync(output, 'utf8'), '');
      assert.equal(readFileSync(summary, 'utf8'), '');
    }
    writeFileSync(metadata, JSON.stringify(pr()));
    assert.equal(run().status, 0);
    assert.equal(readFileSync(output, 'utf8'), `suites=${JSON.stringify(everySuite)}\n`);
    assert.match(readFileSync(summary, 'utf8'), new RegExp(head));
    writeFileSync(files, 'README.md\n');
    writeFileSync(output, '');
    writeFileSync(summary, '');
    assert.equal(run().status, 0);
    assert.equal(readFileSync(output, 'utf8'), 'suites=[]\n');
    assert.match(readFileSync(summary, 'utf8'), /none \(no applicable suites\)/);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
