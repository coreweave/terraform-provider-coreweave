import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import { selectedSuites, verifySuiteJobs, validateRun } from './validation.mjs';

const head = 'a'.repeat(40);
const merge = 'b'.repeat(40);
const everySuite = ['caller_identity', 'cks', 'container_registry', 'inference', 'networking', 'object_storage'];
const hosted = ['build', 'lint', 'generate', 'snapshot-build', 'test', 'pr-title-format', 'detect-changes'];
const context = { eventName: 'pull_request', repo: { owner: 'public', repo: 'fixture' }, runId: 100,
  sha: merge, payload: { pull_request: { number: 7, head: { sha: head } } } };
const needs = suites => ({ ...Object.fromEntries(hosted.map(id => [id, { result: 'success' }])),
  'detect-changes': { result: 'success', outputs: { suites: JSON.stringify(suites) } },
  'acceptance-tests': { result: suites.length ? 'success' : 'skipped' } });
const job = (suite, phase, overrides = {}) => ({ run_id: 100, run_attempt: 1,
  name: `acceptance-tests (${suite}) / ${phase}`, status: 'completed', conclusion: 'success',
  steps: [{ name: phase === 'sweep' ? `Sweep ${suite} resources` : `Validate ${suite} acceptance tests`,
    status: 'completed', conclusion: 'success' }], ...overrides });
const jobs = suites => suites.flatMap(suite => [job(suite, 'sweep'), job(suite, 'test')]);
const run = () => ({ id: 100, run_attempt: 1, head_sha: head, event: 'pull_request',
  path: '.github/workflows/test.yml', repository: { full_name: 'public/fixture' },
  status: 'in_progress', conclusion: null,
  referenced_workflows: [{ path: `public/fixture/.github/workflows/acceptance-test.yaml@${merge}`, sha: merge }] });

function api(overrides = {}) {
  const calls = [];
  let prReads = 0;
  let runReads = 0;
  const github = {
    rest: {
      pulls: { get: async parameters => {
        calls.push(['pr', parameters]);
        if (overrides.prError) throw overrides.prError;
        const pr = overrides.prs ? overrides.prs[prReads++] :
          Object.hasOwn(overrides, 'pr') ? overrides.pr : { state: 'open', head: { sha: head } };
        return { data: pr };
      } },
      actions: {
        getWorkflowRun: async parameters => {
          calls.push(['run', parameters]);
          if (overrides.runError) throw overrides.runError;
          return { data: overrides.runs ? overrides.runs[runReads++] :
            Object.hasOwn(overrides, 'run') ? overrides.run : run() };
        },
        listJobsForWorkflowRun: () => {},
      },
    },
    paginate: async (route, parameters) => {
      assert.equal(route, github.rest.actions.listJobsForWorkflowRun);
      calls.push(['jobs', parameters]);
      if (overrides.jobsError) throw overrides.jobsError;
      return overrides.jobs ?? jobs(everySuite);
    },
  };
  return { github, calls };
}

test('the aggregate reports hosted checks with explicitly nonapplicable acceptance', async () => {
  const { github, calls } = api();
  const result = await validateRun(github, context, needs([]), 1, false);
  assert.deepEqual(result, { head, suites: [], rows: [], acceptance: 'not applicable' });
  assert.equal(calls.filter(([name]) => name === 'jobs').length, 0);
  assert.equal(calls.filter(([name]) => name === 'pr').length, 2);
  assert.equal(calls.filter(([name]) => name === 'run').length, 2);
});

test('all six selected suites require successful sweep and test commands', async () => {
  const { github, calls } = api();
  const result = await validateRun(github, context, needs(everySuite), 1, false);
  assert.deepEqual(result.rows, everySuite.map(suite => [suite, 'passed', 'passed']));
  assert.equal(result.acceptance, 'passed');
  assert.deepEqual(calls.find(([name]) => name === 'jobs')[1],
    { owner: 'public', repo: 'fixture', run_id: 100, filter: 'all', per_page: 100 });
});

test('failed, canceled, skipped, absent or unfinished hosted checks cannot pass', () => {
  for (const id of hosted) {
    for (const result of ['failure', 'cancelled', 'skipped', 'in_progress', undefined]) {
      const state = needs([]);
      state[id] = { result };
      assert.throws(() => selectedSuites(state), `${id}: ${result}`);
    }
  }
});

test('selection and matrix conclusions must agree and contain unique known suites', () => {
  for (const selection of ['invalid JSON', '{}', '["unknown"]', '["cks","cks"]', 'null']) {
    const state = needs(['cks']);
    state['detect-changes'].outputs.suites = selection;
    assert.throws(() => selectedSuites(state));
  }
  for (const selection of [[], ['cks']]) {
    for (const result of ['failure', 'cancelled', undefined, selection.length ? 'skipped' : 'success']) {
      const state = needs(selection);
      state['acceptance-tests'].result = result;
      assert.throws(() => selectedSuites(state));
    }
  }
});

test('each selected phase needs actual successful command execution, not a skipped job or step', () => {
  for (const suite of everySuite) {
    for (const phase of ['sweep', 'test']) {
      const complete = jobs(everySuite);
      const target = complete.findIndex(item => item.name === job(suite, phase).name);
      assert.throws(() => verifySuiteJobs(complete.filter((_, index) => index !== target), everySuite, 100, 1));
      for (const conclusion of ['failure', 'cancelled', 'skipped', null]) {
        const invalid = [...complete];
        invalid[target] = job(suite, phase, { conclusion });
        assert.throws(() => verifySuiteJobs(invalid, everySuite, 100, 1));
        invalid[target] = job(suite, phase, { steps: [{ ...complete[target].steps[0], conclusion }] });
        assert.throws(() => verifySuiteJobs(invalid, everySuite, 100, 1));
      }
      for (const overrides of [{ steps: [] }, { status: 'in_progress' }, { run_id: 99 },
        { run_attempt: 2 }, { run_attempt: undefined }]) {
        const invalid = [...complete];
        invalid[target] = job(suite, phase, overrides);
        assert.throws(() => verifySuiteJobs(invalid, everySuite, 100, 1));
      }
    }
  }
});

test('partial reruns retain successful prior phases but never replace a newer failure with an older pass', () => {
  const original = jobs(['cks']);
  const updated = job('cks', 'test', { run_attempt: 2 });
  assert.deepEqual(verifySuiteJobs([...original, updated], ['cks'], 100, 2), [['cks', 'passed', 'passed']]);
  for (const conclusion of ['failure', 'skipped', 'cancelled']) {
    assert.throws(() => verifySuiteJobs([...original, { ...updated, conclusion }], ['cks'], 100, 2));
  }
  assert.throws(() => verifySuiteJobs([...original, updated, updated], ['cks'], 100, 2));
});

test('stale or closed revisions fail both before and after reading job evidence', async () => {
  const current = { state: 'open', head: { sha: head } };
  for (const invalid of [{ state: 'closed', head: { sha: head } }, { state: 'open', head: { sha: merge } }, {}, null]) {
    await assert.rejects(validateRun(api({ pr: invalid }).github, context, needs(everySuite), 1, false));
    await assert.rejects(validateRun(api({ prs: [current, invalid] }).github, context, needs(everySuite), 1, false));
  }
});

test('run identity, attempt, merge provenance and cancellation are checked', async () => {
  for (const overrides of [{ id: 99 }, { run_attempt: 2 }, { head_sha: merge }, { event: 'push' },
    { path: '.github/workflows/other.yml' }, { repository: {} }, { status: 'completed', conclusion: 'cancelled' },
    { referenced_workflows: [] }]) {
    await assert.rejects(validateRun(api({ run: { ...run(), ...overrides } }).github, context, needs(everySuite), 1, false));
  }
  await assert.rejects(validateRun(api().github, context, needs([]), 1, true));
  await assert.rejects(validateRun(api().github, { ...context, eventName: 'push' }, needs([]), 1, false));
  await assert.rejects(validateRun(api({ runs: [run(), { ...run(), conclusion: 'cancelled' }] }).github,
    context, needs(everySuite), 1, false));
});

test('API uncertainty fails closed at every read boundary', async () => {
  for (const field of ['prError', 'runError', 'jobsError']) {
    const error = new Error('API unavailable');
    await assert.rejects(validateRun(api({ [field]: error }).github, context, needs(everySuite), 1, false), actual => actual === error);
  }
});

test('the stable aggregate depends on every check, runs after failures and only has read permissions', () => {
  const workflow = readFileSync(new URL('../workflows/test.yml', import.meta.url), 'utf8');
  const aggregate = workflow.split('\n  validation:\n')[1];
  assert.ok(aggregate);
  assert.match(aggregate, /^    name: PR validation$/m);
  assert.match(aggregate, /^    if: \$\{\{ always\(\) \}\}$/m);
  const prerequisites = /^    needs: \[([^\]]+)\]$/m.exec(aggregate)[1].split(',').map(id => id.trim()).sort();
  assert.deepEqual(prerequisites, [...hosted, 'acceptance-tests'].sort());
  assert.match(aggregate, /^      actions: read$/m);
  assert.match(aggregate, /^      pull-requests: read$/m);
  assert.match(aggregate, /^          persist-credentials: false$/m);
  assert.doesNotMatch(aggregate, /secrets\.|: write|continue-on-error:/);
});
