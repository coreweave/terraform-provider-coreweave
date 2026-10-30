import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import { summarizeRegressions } from './regressions.mjs';

const event = (Action, Package, Test, extra = {}) => ({ Action, Package, Test, ...extra });
const attr = (pkg, name, ticket) => event('attr', pkg, name, { Key: 'known_regression', Value: ticket });
const json = events => events.map(e => JSON.stringify(e)).join('\n');

test('attributes and outcomes join by package and test despite interleaved output', () => {
  const name = 'TestKnownRegressions/CKSAPI-753_replace';
  const summary = summarizeRegressions(json([
    attr('cks', name, 'CKSAPI-753'), attr('other', name, 'CKSAPI-753'),
    event('pass', 'other', name), event('output', 'cks', name, { Output: 'diagnostic' }),
    event('fail', 'cks', name), event('fail', 'cks'),
  ]));
  assert.match(summary, /CKSAPI-753.*cks.*FAIL/);
  assert.match(summary, /CKSAPI-753.*other.*PASS.*fixed: graduate this test out of TestKnownRegressions/);
});

test('no matching tests and missing terminal results stay explicit', () => {
  assert.match(summarizeRegressions(json([event('pass', 'empty')])), /No known_regression attributes/);
  assert.match(summarizeRegressions(json([attr('cks', 'case', 'CKSAPI-753')])), /UNKNOWN/);
  assert.match(summarizeRegressions(json([attr('cks', 'case', 'CKSAPI-753'), event('skip', 'cks', 'case')])), /SKIP/);
});

test('only known_regression attributes are summarized, malformed JSON fails visibly', () => {
  assert.match(summarizeRegressions(json([event('attr', 'cks', 'case', { Key: 'other', Value: 'value' })])), /No known_regression attributes/);
  assert.throws(() => summarizeRegressions('{broken'), /JSON/);
});

test('all marked tickets are retained and markdown cannot break the result table', () => {
  const summary = summarizeRegressions(json([
    attr('a|b', '<case>', 'CKSAPI-753'), attr('a|b', '<case>', 'CLDAPI-1174'),
    event('fail', 'a|b', '<case>'),
  ]));
  assert.match(summary, /CKSAPI-753/);
  assert.match(summary, /CLDAPI-1174/);
  assert.match(summary, /a&#124;b/);
  assert.match(summary, /&lt;case&gt;/);
});

const workflow = readFileSync(new URL('../workflows/acceptance-test.yaml', import.meta.url), 'utf8');
const sections = workflow.split(/^  (?=[a-z][a-z-]*:\n)/m);
const job = id => sections.find(section => section.startsWith(`${id}:\n`)) || '';

test('one suite lock covers the sweep and both independent test jobs', () => {
  assert.match(workflow, /group: acceptance-test-\$\{\{ inputs.suite \}\}\n  queue: max\n  cancel-in-progress: false/);
  for (const id of ['test', 'regressions']) {
    assert.match(job(id), /^    needs: \[sweep\]$/m);
    assert.doesNotMatch(job(id), /testacc-sweep/);
  }
  assert.match(job('sweep'), /make testacc-sweep/);
  assert.equal((workflow.match(/make testacc-sweep/g) || []).length, 1);
  assert.match(job('regressions'), /make testacc-regressions/);
});

test('only regressions tolerate job failure and report the real result', () => {
  assert.match(job('regressions'), /^    continue-on-error: true$/m);
  for (const id of ['test', 'sweep']) assert.doesNotMatch(job(id), /continue-on-error:/);
  assert.match(job('regressions'), /checks: write/);
  assert.match(job('regressions'), /if: \$\{\{ always\(\) \}\}/);
  assert.match(job('regressions'), /steps\.regressions\.outcome/);
  assert.match(job('regressions'), /Known regressions \(\$\{process.env.SUITE\}\)/);
  assert.match(job('regressions'), /github\.rest\.checks\.create/);
  assert.match(job('regressions'), /head_sha: context\.payload\.pull_request\.head\.sha/);
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
    assert.equal(calls[0].name, 'Known regressions (cks)');
    assert.equal(calls[0].head_sha, context.payload.pull_request.head.sha);
    assert.equal(calls[0].conclusion, conclusion, `${outcome}/${status}`);
    assert.equal(calls[0].status, 'completed');
  }
});

test('CKS regressions run one at a time while normal acceptance keeps its parallelism', () => {
  assert.match(job('test'), /inputs\.suite == 'cks' && 'TEST_ACC_PARALLEL=3 TEST_ACC_TIMEOUT=60m'/);
  assert.match(job('regressions'), /inputs\.suite == 'cks' && 'TEST_ACC_PARALLEL=1 TEST_ACC_TIMEOUT=60m'/);
});
