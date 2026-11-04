import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import { summarizeNonBlocking } from './nonblocking.mjs';

const event = (Action, Package, Test, extra = {}) => ({ Action, Package, Test, ...extra });
const attr = (pkg, name) => event('attr', pkg, name, { Key: 'acceptance_mode', Value: 'nonblocking' });
const json = events => events.map(e => JSON.stringify(e)).join('\n');

test('attributes and outcomes join by package and test despite interleaved output', () => {
  const name = 'TestNonBlockingAcceptance/cluster_lifecycle';
  const summary = summarizeNonBlocking(json([
    attr('cks', name), attr('other', name),
    event('pass', 'other', name), event('output', 'cks', name, { Output: 'diagnostic' }),
    event('fail', 'cks', name), event('fail', 'cks'),
  ]));
  assert.match(summary, /cks.*cluster_lifecycle.*FAIL/);
  assert.match(summary, /other.*cluster_lifecycle.*PASS.*Passing: review for inclusion in required acceptance tests\./);
  assert.doesNotMatch(summary, /Ticket/);
});

test('no matching tests and missing terminal results stay explicit', () => {
  assert.match(summarizeNonBlocking(json([event('pass', 'empty')])), /No non-blocking acceptance tests were recorded/);
  assert.match(summarizeNonBlocking(json([attr('cks', 'case')])), /UNKNOWN/);
  assert.match(summarizeNonBlocking(json([attr('cks', 'case'), event('skip', 'cks', 'case')])), /SKIP/);
});

test('only the nonblocking acceptance mode is summarized, malformed JSON fails visibly', () => {
  const ignored = json([
    event('attr', 'cks', 'other_key', { Key: 'other', Value: 'nonblocking' }),
    event('attr', 'cks', 'other_value', { Key: 'acceptance_mode', Value: 'required' }),
  ]);
  assert.match(summarizeNonBlocking(ignored), /No non-blocking acceptance tests were recorded/);
  assert.throws(() => summarizeNonBlocking('{broken'), /JSON/);
});

test('repeated markers produce one row and markdown cannot break the result table', () => {
  const summary = summarizeNonBlocking(json([
    attr('a|b', '<case>'), attr('a|b', '<case>'),
    event('fail', 'a|b', '<case>'),
  ]));
  assert.equal((summary.match(/a&#124;b/g) || []).length, 1);
  assert.match(summary, /&lt;case&gt;/);
});

const workflow = readFileSync(new URL('../workflows/acceptance-test.yaml', import.meta.url), 'utf8');
const sections = workflow.split(/^  (?=[a-z][a-z-]*:\n)/m);
const job = id => sections.find(section => section.startsWith(`${id}:\n`)) || '';

test('one suite lock covers the sweep and both independent test jobs', () => {
  assert.match(workflow, /group: acceptance-test-\$\{\{ inputs.suite \}\}\n  queue: max\n  cancel-in-progress: false/);
  for (const id of ['test', 'nonblocking']) {
    assert.match(job(id), /^    needs: \[sweep\]$/m);
    assert.doesNotMatch(job(id), /testacc-sweep/);
  }
  assert.match(job('sweep'), /make testacc-sweep/);
  assert.equal((workflow.match(/make testacc-sweep/g) || []).length, 1);
  assert.match(job('nonblocking'), /make testacc-nonblocking/);
});

test('only non-blocking acceptance tolerates job failure and reports the real result', () => {
  assert.match(job('nonblocking'), /^    continue-on-error: true$/m);
  for (const id of ['test', 'sweep']) assert.doesNotMatch(job(id), /continue-on-error:/);
  assert.match(job('nonblocking'), /checks: write/);
  assert.match(job('nonblocking'), /if: \$\{\{ always\(\) \}\}/);
  assert.match(job('nonblocking'), /steps\.nonblocking\.outcome/);
  assert.match(job('nonblocking'), /Non-blocking acceptance \(\$\{process.env.SUITE\}\)/);
  assert.match(job('nonblocking'), /github\.rest\.checks\.create/);
  assert.match(job('nonblocking'), /head_sha: context\.payload\.pull_request\.head\.sha/);
});

test('the actual check publisher preserves failures, cancellation, and skipped commands', async () => {
  const scripts = [...workflow.matchAll(/^          script: \|\n((?:            .*\n)+)/gm)];
  assert.equal(scripts.length, 2);
  const AsyncFunction = Object.getPrototypeOf(async function () {}).constructor;
  const publish = new AsyncFunction('github', 'context', 'process', scripts[1][1].replace(/^ {12}/gm, ''));
  for (const [outcome, status, conclusion] of [
    ['success', 'success', 'success'], ['failure', 'failure', 'failure'],
    ['skipped', 'failure', 'failure'], ['', 'failure', 'failure'],
    ['cancelled', 'cancelled', 'cancelled'], ['success', 'failure', 'failure'],
    ['success', 'cancelled', 'cancelled'],
  ]) {
    const calls = [];
    const context = { repo: { owner: 'public', repo: 'fixture' }, serverUrl: 'https://github.com',
      runId: 42, payload: { pull_request: { head: { sha: 'a'.repeat(40) } } } };
    await publish({ rest: { checks: { create: async params => calls.push(params) } } }, context,
      { env: { SUITE: 'cks', TEST_OUTCOME: outcome, JOB_STATUS: status } });
    assert.equal(calls.length, 1);
    assert.equal(calls[0].name, 'Non-blocking acceptance (cks)');
    assert.equal(calls[0].head_sha, context.payload.pull_request.head.sha);
    assert.equal(calls[0].conclusion, conclusion, `${outcome}/${status}`);
    assert.equal(calls[0].status, 'completed');
  }
});

test('CKS non-blocking acceptance tests run one at a time while normal acceptance keeps its parallelism', () => {
  assert.match(job('test'), /inputs\.suite == 'cks' && 'TEST_ACC_PARALLEL=3 TEST_ACC_TIMEOUT=60m'/);
  assert.match(job('nonblocking'), /inputs\.suite == 'cks' && 'TEST_ACC_PARALLEL=1 TEST_ACC_TIMEOUT=60m'/);
});

test('non-blocking acceptance installs Node before the command that may fail', () => {
  const nonblocking = job('nonblocking');
  const setup = nonblocking.indexOf('uses: actions/setup-node@');
  const command = nonblocking.indexOf('name: Run non-blocking acceptance for');
  assert.ok(setup >= 0 && setup < command, 'Node setup must finish before the intentional failure so the summary can run');
  assert.match(nonblocking, /node-version: '24\.21\.0'/);
});
