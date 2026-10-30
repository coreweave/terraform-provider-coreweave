import { appendFileSync, readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

function escapeCell(value) {
  return String(value).replaceAll('&', '&amp;').replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;').replaceAll('|', '&#124;').replaceAll('\n', ' ');
}

export function summarizeNonBlocking(json) {
  const results = new Map();
  for (const line of json.split('\n').filter(line => line.trim())) {
    const event = JSON.parse(line);
    if (!event.Test) continue;
    const key = JSON.stringify([event.Package, event.Test]);
    const result = results.get(key) || { package: event.Package, test: event.Test, nonblocking: false, outcome: 'UNKNOWN' };
    if (event.Action === 'attr' && event.Key === 'acceptance_mode' && event.Value === 'nonblocking') result.nonblocking = true;
    if (['pass', 'fail', 'skip'].includes(event.Action)) result.outcome = event.Action.toUpperCase();
    results.set(key, result);
  }
  const rows = [...results.values()].filter(result => result.nonblocking).map(result =>
    `| ${[result.package, result.test, result.outcome,
      result.outcome === 'PASS' ? 'Passing: review for inclusion in required acceptance tests.' : ''].map(escapeCell).join(' | ')} |`);
  return ['## Non-blocking acceptance', '', ...(rows.length ? [
    '| Package | Test | Result | Next step |', '| --- | --- | --- | --- |', ...rows,
  ] : ['No non-blocking acceptance tests were recorded. A suite without TestNonBlockingAcceptance is expected to pass with no tests to run.']), ''].join('\n');
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const summary = summarizeNonBlocking(readFileSync(process.argv[2], 'utf8'));
  appendFileSync(process.env.GITHUB_STEP_SUMMARY, summary);
}
