import { readFileSync, appendFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

export const allSuites = ['caller_identity', 'cks', 'networking', 'object_storage', 'inference', 'container_registry'];

export function selectSuites(pr, expectedHead, files, subjects) {
  if (!/^[a-f0-9]{40}$/.test(expectedHead) || pr?.state !== 'open' || pr?.head?.sha !== expectedHead) {
    throw new Error('Acceptance selection requires an open PR at the requested head SHA.');
  }
  if (typeof pr.title !== 'string' || !Array.isArray(pr.labels) || pr.labels.some(label => typeof label?.name !== 'string')) {
    throw new Error('PR metadata is missing its title or labels.');
  }

  const labels = new Set(pr.labels.map(label => label.name));
  const scopes = new Set([pr.title, ...subjects].flatMap(subject => {
    const match = /^[a-z][a-z0-9_-]*\(([^)]+)\)!?:/.exec(subject);
    return match ? [match[1]] : [];
  }));
  const coreChanged = files.some(file => /^(go\.(mod|sum)|main\.go|GNUmakefile|internal\/testutil\/.*|coreweave\/(client|retry|s3)(_test)?\.go)$/.test(file));
  const providerChanged = files.some(file => file.startsWith('internal/provider/'));
  return allSuites.filter(suite => coreChanged || providerChanged || labels.has('acceptance-test:all') ||
    labels.has(`acceptance-test:${suite}`) || scopes.has(suite) || files.some(file => file.startsWith(`coreweave/${suite}/`))).sort();
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const [prPath, expectedHead, filesPath, subjectsPath] = process.argv.slice(2);
  const lines = path => readFileSync(path, 'utf8').split('\n').filter(Boolean);
  // Validate and select before emitting anything consumed by the QA matrix.
  const suites = selectSuites(JSON.parse(readFileSync(prPath, 'utf8')), expectedHead, lines(filesPath), lines(subjectsPath));
  appendFileSync(process.env.GITHUB_OUTPUT, `suites=${JSON.stringify(suites)}\n`);
  appendFileSync(process.env.GITHUB_STEP_SUMMARY, `Selected acceptance suites for head \`${expectedHead}\`: ${suites.length ? suites.map(suite => `\`${suite}\``).join(', ') : 'none (no applicable suites)'}\n`);
}
