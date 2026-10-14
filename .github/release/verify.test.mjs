import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtempSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const head = 'a'.repeat(40);
const commit = 'b'.repeat(40);
const tree = 'c'.repeat(40);
const repo = 'public/fixture';
const pr = { merged: true, state: 'closed', merge_commit_sha: commit,
  head: { sha: head, ref: 'release-please--branches--main', repo: { full_name: repo } },
  base: { ref: 'main', repo: { full_name: repo } } };
const check = { name: 'Release acceptance', conclusion: 'success' };
const script = fileURLToPath(new URL('./verify.sh', import.meta.url));

function run(t, overrides = {}) {
  const dir = mkdtempSync(join(tmpdir(), 'release-gate-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const output = join(dir, 'output');
  writeFileSync(output, '');
  writeFileSync(join(dir, 'responses.json'), JSON.stringify({ pr, checks: [check], headTree: tree, mergeTree: tree, ...overrides }));
  writeFileSync(join(dir, 'mock.mjs'), `
    import { readFileSync } from 'node:fs';
    const data = JSON.parse(readFileSync(process.env.RESPONSES));
    const args = process.argv.slice(2);
    if (data.apiError) process.exit(1);
    if (args[0] === 'run' && args[1] === 'list') {
      if (!args.includes('test.yml') || !args.includes('${head}') || !args.includes('pull_request')) process.exit(99);
      console.log(data.runId ?? '123');
    } else if (args[0] === 'run' && args[1] === 'watch') {
      if (args[2] !== '123' || !args.includes('--exit-status')) process.exit(99);
      process.exit(data.watchExit ?? 0);
    } else if (args[0] === 'run' && args[1] === 'view') {
      console.log(JSON.stringify({ jobs: data.checks }));
    } else if (args[1] === 'repos/public/fixture/pulls/7') {
      console.log(JSON.stringify(data.pr));
    } else if (args[1] === 'repos/public/fixture/git/commits/${head}') {
      console.log(data.headTree);
    } else if (args[1] === 'repos/public/fixture/git/commits/${commit}') {
      console.log(data.mergeTree);
    } else {
      console.error('Unexpected gh invocation', args); process.exit(99);
    }
  `);
  writeFileSync(join(dir, 'gh'), '#!/bin/sh\nexec "$NODE" "$MOCK" "$@"\n', { mode: 0o755 });
  const result = spawnSync('bash', [script], { encoding: 'utf8', env: { ...process.env,
    PATH: `${dir}:${process.env.PATH}`, NODE: process.execPath, MOCK: join(dir, 'mock.mjs'), RESPONSES: join(dir, 'responses.json'),
    PR_NUMBER: overrides.number ?? '7', GITHUB_REPOSITORY: repo, GITHUB_OUTPUT: output } });
  return { ...result, output: readFileSync(output, 'utf8') };
}

test('a fully validated merged candidate exports the exact merge commit', t => {
  const result = run(t);
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.output, `commit=${commit}\n`);
});

test('missing, duplicate, unsuccessful or unrelated release checks block publication', t => {
  for (const checks of [[], [check, check], ...['failure', 'cancelled', 'skipped', 'neutral', null].map(conclusion => [{ ...check, conclusion }]),
    [{ ...check, name: 'PR validation' }]]) {
    const result = run(t, { checks });
    assert.notEqual(result.status, 0, JSON.stringify(checks));
    assert.equal(result.output, '');
  }
});

test('unmerged, forked, unrelated and malformed release PRs block publication', t => {
  for (const metadata of [{ ...pr, merged: false }, { ...pr, state: 'open' },
    { ...pr, head: { ...pr.head, ref: 'renovate/dependencies' } },
    { ...pr, head: { ...pr.head, repo: { full_name: 'fork/fixture' } } },
    { ...pr, base: { ...pr.base, ref: 'other' } },
    { ...pr, base: { ...pr.base, repo: { full_name: 'other/repo' } } },
    { ...pr, merge_commit_sha: 'bad' }, {}, null]) {
    const result = run(t, { pr: metadata });
    assert.notEqual(result.status, 0);
    assert.equal(result.output, '');
  }
});

test('API errors, pending/failed checks and untested merge changes cannot export a candidate', t => {
  for (const overrides of [{ apiError: true }, { watchExit: 1 }, { watchExit: 8 },
    { runId: '' }, { runId: 'null' }, { mergeTree: 'd'.repeat(40) }, { headTree: 'null', mergeTree: 'null' }, { number: '7; invalid' }]) {
    const result = run(t, overrides);
    assert.notEqual(result.status, 0);
    assert.equal(result.output, '');
  }
});

test('automatic and manual publication share the gate before candidate code and signing', () => {
  const workflow = readFileSync(new URL('../workflows/release.yml', import.meta.url), 'utf8');
  assert.match(workflow, /pull_request_target:\n    types: \[closed\]/);
  assert.match(workflow, /release-pr:/);
  assert.doesNotMatch(workflow, /git-tag:|workflow_call:/);
  assert.match(workflow, /ref: \$\{\{ github\.sha \}\}\n          persist-credentials: false/);
  assert.match(workflow, /actions: read/);
  assert.match(workflow, /actions: write/);
  assert.match(workflow, /gh workflow run release-please\.yaml/);
  assert.match(workflow, /goreleaser:\n    concurrency:[\s\S]*?needs: validation/);
  assert.match(workflow, /ref: \$\{\{ needs\.validation\.outputs\.commit \}\}/);
  const writer = readFileSync(new URL('../workflows/release-please.yaml', import.meta.url), 'utf8');
  assert.match(writer, /skip-github-release: true/);
  assert.match(writer, /token: \$\{\{ github\.token \}\}/);
  assert.doesNotMatch(writer, /uses: \.\/\.github\/workflows\/release\.yml/);
});

test('publication checks do not become prerequisites for the selected Tests run', t => {
  const result = run(t, { checks: [check, { name: 'validation', conclusion: null, event: 'pull_request_target' }] });
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.output, `commit=${commit}\n`);
});
