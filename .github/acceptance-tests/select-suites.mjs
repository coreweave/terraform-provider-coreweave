import { readFileSync, appendFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

export const allSuites = ['caller_identity', 'cks', 'networking', 'object_storage', 'inference', 'container_registry'];

export function selectSuites(pr, expectedHead, repository) {
  if (!/^[a-f0-9]{40}$/.test(expectedHead) || pr?.state !== 'open' || pr?.head?.sha !== expectedHead) {
    throw new Error('Acceptance selection requires an open PR at the requested head SHA.');
  }
  if (typeof repository !== 'string' || !repository || typeof pr.head.ref !== 'string' ||
      typeof pr.head.repo?.full_name !== 'string' || !pr.head.repo.full_name ||
      typeof pr.base?.ref !== 'string' || typeof pr.base.repo?.full_name !== 'string' || !pr.base.repo.full_name) {
    throw new Error('PR metadata is missing its source or target repository and branch.');
  }

  if (pr.head.repo.full_name !== repository || pr.base.repo.full_name !== repository) {
    throw new Error('Acceptance requires a same-repository PR. A maintainer must bring reviewed fork changes onto a repository branch.');
  }

  return [...allSuites].sort();
}

export function isReleaseCandidate(pr, repository) {
  return pr?.head?.ref === 'release-please--branches--main' && pr?.base?.ref === 'main' &&
    pr.head.repo?.full_name === repository && pr.base.repo?.full_name === repository;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const [prPath, expectedHead, repository] = process.argv.slice(2);
  const pr = JSON.parse(readFileSync(prPath, 'utf8'));
  const suites = selectSuites(pr, expectedHead, repository);
  appendFileSync(process.env.GITHUB_OUTPUT, `suites=${JSON.stringify(suites)}\nis-release=${isReleaseCandidate(pr, repository)}\n`);
  appendFileSync(process.env.GITHUB_STEP_SUMMARY, `Acceptance for head \`${expectedHead}\`: all suites require environment approval.\n`);
}
