import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';

// Exercise the actual github-script body rather than a copy of the guard.
const workflow = readFileSync(new URL('../workflows/acceptance-test.yaml', import.meta.url), 'utf8');
const block = /^          script: \|\n((?:            .*\n)+)/m.exec(workflow);
assert.ok(block, 'the reusable acceptance workflow must contain the freshness script');
const AsyncFunction = Object.getPrototypeOf(async function () {}).constructor;
const guard = new AsyncFunction('github', 'context', 'core', block[1].replace(/^ {12}/gm, ''));
const head = 'a'.repeat(40);
const newerHead = 'b'.repeat(40);
const context = (overrides = {}) => ({ eventName: 'pull_request', repo: { owner: 'public', repo: 'fixture' },
  payload: { pull_request: { number: 7, head: { sha: head } } }, ...overrides });

test('the caller explicitly grants the reusable QA jobs their read permissions', () => {
  const caller = readFileSync(new URL('../workflows/acceptance-tests.yaml', import.meta.url), 'utf8');
  const job = caller.split('\n  run-tests:\n')[1];
  assert.ok(job, 'the acceptance caller job exists');
  const permissions = /^    permissions:\n((?:      .*\n)+)/m.exec(job);
  assert.ok(permissions, 'the caller must set its permission ceiling explicitly');
  const actual = Object.fromEntries(permissions[1].trim().split('\n').map(line => line.trim().split(': ')));
  assert.deepEqual(actual, { contents: 'read', 'pull-requests': 'read', packages: 'read' });
});

test('both QA jobs guard checkout and the resource command', () => {
  const sections = workflow.split(/^  (?=[a-z][a-z-]*:\n)/m);
  const isGuard = step => /^(?:&current-pr\n|\*current-pr\n)/.test(step);
  for (const id of ['sweep', 'test']) {
    const section = sections.find(part => part.startsWith(`${id}:\n`));
    assert.ok(section, `${id} job exists`);
    const steps = section.split('    steps:\n')[1].split(/^      - /m).slice(1);
    assert.ok(isGuard(steps[0]), `${id}: guard before checkout`);
    assert.match(steps[1], /^uses: actions\/checkout@/);
    const command = steps.findIndex(step => step.includes(id === 'sweep' ? 'make testacc-sweep' : 'make testacc SUITES='));
    assert.ok(command > 0 && isGuard(steps[command - 1]), `${id}: guard immediately before QA command`);
  }
});

async function check(ctx, current, error) {
  const calls = [];
  const messages = [];
  const github = { rest: { pulls: { get: async parameters => {
    calls.push(parameters);
    if (error) throw error;
    return { data: current };
  } } } };
  await guard(github, ctx, { info: message => messages.push(message) });
  return { calls, messages };
}

test('the requested current open PR passes the actual workflow guard', async () => {
  const result = await check(context(), { state: 'open', head: { sha: head } });
  assert.deepEqual(result.calls, [{ owner: 'public', repo: 'fixture', pull_number: 7 }]);
  assert.deepEqual(result.messages, [`Verified current PR head ${head}.`]);
});

test('closed, stale or malformed API metadata rejects the phase', async () => {
  for (const current of [{ state: 'closed', head: { sha: head } }, { state: 'open', head: { sha: newerHead } },
    { state: 'open' }, {}, null]) {
    await assert.rejects(check(context(), current));
  }
});

test('API failures cannot approve queued work', async () => {
  for (const message of ['unauthorized', 'rate limited', 'network failure']) {
    const error = new Error(message);
    await assert.rejects(check(context(), null, error), actual => actual === error);
  }
});

test('unsupported events or invalid requested revisions cannot pass', async () => {
  for (const ctx of [context({ eventName: 'push' }), context({ payload: {} }),
    context({ payload: { pull_request: { number: 0, head: { sha: head } } } }),
    context({ payload: { pull_request: { number: 7, head: { sha: 'invalid' } } } })]) {
    await assert.rejects(check(ctx, { state: 'open', head: { sha: head } }));
  }
});

test('only the newer of two queued revisions can pass once the head changes', async () => {
  const current = { state: 'open', head: { sha: newerHead } };
  await assert.rejects(check(context(), current));
  const newer = context({ payload: { pull_request: { number: 7, head: { sha: newerHead } } } });
  await check(newer, current);
});

test('a revision that passes before sweeping is rejected before testing if the head changes', async () => {
  await check(context(), { state: 'open', head: { sha: head } });
  await assert.rejects(check(context(), { state: 'open', head: { sha: newerHead } }));
});
