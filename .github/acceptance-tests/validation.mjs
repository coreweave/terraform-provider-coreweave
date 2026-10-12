import { allSuites } from './select-suites.mjs';

const hostedJobs = ['build', 'lint', 'generate', 'snapshot-build', 'test', 'pr-title-format', 'detect-changes'];

export function selectedSuites(needs) {
  for (const id of hostedJobs) {
    if (needs?.[id]?.result !== 'success') {
      throw new Error(`PR validation requires ${id} to succeed.`);
    }
  }
  const suites = JSON.parse(needs['detect-changes'].outputs?.suites);
  if (!Array.isArray(suites) || suites.some(suite => !allSuites.includes(suite)) || new Set(suites).size !== suites.length) {
    throw new Error('PR validation requires a valid, unique acceptance suite selection.');
  }
  const expected = suites.length ? 'success' : 'skipped';
  if (needs['acceptance-tests']?.result !== expected) {
    throw new Error(`Acceptance must be ${expected} for this suite selection.`);
  }
  return suites;
}

export function verifySuiteJobs(jobs, suites, runId, attempt) {
  if (!Array.isArray(jobs) || !Number.isSafeInteger(runId) || runId < 1 ||
      !Number.isSafeInteger(attempt) || attempt < 1) {
    throw new Error('Acceptance job evidence is unavailable.');
  }
  return suites.map(suite => {
    for (const phase of ['sweep', 'test']) {
      const name = `acceptance-tests (${suite}) / ${phase}`;
      const candidates = jobs.filter(job => job.name === name);
      if (!candidates.length || candidates.some(job => job.run_id !== runId ||
          !Number.isSafeInteger(job.run_attempt) || job.run_attempt < 1 || job.run_attempt > attempt)) {
        throw new Error(`Missing or invalid job evidence for ${name}.`);
      }
      // Partial reruns retain earlier successful jobs, but a newer execution of
      // the same phase must never be replaced by an older passing result.
      const latestAttempt = Math.max(...candidates.map(job => job.run_attempt));
      const latest = candidates.filter(job => job.run_attempt === latestAttempt);
      if (latest.length !== 1 || latest[0].status !== 'completed' || latest[0].conclusion !== 'success') {
        throw new Error(`Acceptance phase ${name} did not succeed.`);
      }
      const stepName = phase === 'sweep' ? `Sweep ${suite} resources` : `Validate ${suite} acceptance tests`;
      const steps = latest[0].steps?.filter(step => step.name === stepName);
      if (!Array.isArray(steps) || steps.length !== 1 || steps[0].status !== 'completed' || steps[0].conclusion !== 'success') {
        throw new Error(`Acceptance command ${stepName} did not execute successfully.`);
      }
    }
    return [suite, 'passed', 'passed'];
  });
}

export async function validateRun(github, context, needs, attempt, cancelled) {
  const requested = context.payload?.pull_request;
  if (cancelled || context.eventName !== 'pull_request' || !Number.isSafeInteger(requested?.number) ||
      requested.number < 1 || !/^[a-f0-9]{40}$/.test(requested?.head?.sha) ||
      !/^[a-f0-9]{40}$/.test(context.sha) || !Number.isSafeInteger(context.runId) || context.runId < 1 ||
      !Number.isSafeInteger(attempt) || attempt < 1) {
    throw new Error('PR validation requires an uncanceled pull-request run with valid revision metadata.');
  }
  const suites = selectedSuites(needs);
  const parameters = { ...context.repo, run_id: context.runId };
  const checkPR = async () => {
    const { data: pr } = await github.rest.pulls.get({ ...context.repo, pull_number: requested.number });
    if (pr?.state !== 'open' || pr?.head?.sha !== requested.head.sha) {
      throw new Error('PR validation stopped: the requested revision is closed or outdated.');
    }
  };
  const checkRun = async () => {
    const { data: run } = await github.rest.actions.getWorkflowRun(parameters);
    if (run?.id !== context.runId || run.run_attempt !== attempt || run.head_sha !== requested.head.sha ||
        run.event !== 'pull_request' || run.path !== '.github/workflows/test.yml' ||
        run.repository?.full_name !== `${context.repo.owner}/${context.repo.repo}` ||
        run.status !== 'in_progress' || run.conclusion !== null) {
      throw new Error('PR validation requires matching current workflow-run evidence.');
    }
    if (suites.length && !run.referenced_workflows?.some(workflow =>
      workflow.sha === context.sha && workflow.path ===
        `${context.repo.owner}/${context.repo.repo}/.github/workflows/acceptance-test.yaml@${context.sha}`)) {
      throw new Error('Acceptance evidence must come from the tested merge revision.');
    }
  };
  await checkPR();
  await checkRun();
  let rows = [];
  if (suites.length) {
    const jobs = await github.paginate(github.rest.actions.listJobsForWorkflowRun, {
      ...parameters, filter: 'all', per_page: 100,
    });
    rows = verifySuiteJobs(jobs, suites, context.runId, attempt);
  }
  await checkPR();
  await checkRun();
  return { head: requested.head.sha, suites, rows, acceptance: suites.length ? 'passed' : 'not applicable' };
}
