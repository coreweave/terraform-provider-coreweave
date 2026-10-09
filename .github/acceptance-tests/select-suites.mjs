import { readFileSync, appendFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

export const allSuites = ['caller_identity', 'cks', 'networking', 'object_storage', 'inference', 'container_registry', 'observability'];

export function selectSuites(pr, expectedHead, repository) {
  if (!/^[a-f0-9]{40}$/.test(expectedHead) || pr?.state !== 'open' || pr?.head?.sha !== expectedHead) {
    throw new Error('Acceptance selection requires an open PR at the requested head SHA.');
  }
  if (typeof repository !== 'string' || !repository || typeof pr.head.ref !== 'string' ||
      typeof pr.head.repo?.full_name !== 'string' || !pr.head.repo.full_name ||
      typeof pr.base?.ref !== 'string' || typeof pr.base.repo?.full_name !== 'string' || !pr.base.repo.full_name) {
    throw new Error('PR metadata is missing its source or target repository and branch.');
  }

  // Release PRs validate all accumulated changes, including dependency updates.
  return pr.head.ref === 'release-please--branches--main' && pr.base.ref === 'main' &&
    pr.head.repo.full_name === repository && pr.base.repo.full_name === repository ? [...allSuites].sort() : [];
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const [prPath, expectedHead, repository] = process.argv.slice(2);
  const suites = selectSuites(JSON.parse(readFileSync(prPath, 'utf8')), expectedHead, repository);
  appendFileSync(process.env.GITHUB_OUTPUT, `suites=${JSON.stringify(suites)}\n`);
  appendFileSync(process.env.GITHUB_STEP_SUMMARY, `Acceptance for head \`${expectedHead}\`: ${suites.length ? 'all suites (release PR)' : 'skipped (ordinary PR)'}\n`);
}
