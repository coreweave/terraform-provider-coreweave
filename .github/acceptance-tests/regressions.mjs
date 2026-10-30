import { appendFileSync, readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

function escapeCell(value) {
  return String(value).replaceAll('&', '&amp;').replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;').replaceAll('|', '&#124;').replaceAll('\n', ' ');
}

export function summarizeRegressions(json) {
  const results = new Map();
  for (const line of json.split('\n').filter(line => line.trim())) {
    const event = JSON.parse(line);
    if (!event.Test) continue;
    const key = JSON.stringify([event.Package, event.Test]);
    const result = results.get(key) || { package: event.Package, test: event.Test, tickets: new Set(), outcome: 'UNKNOWN' };
    if (event.Action === 'attr' && event.Key === 'known_regression') result.tickets.add(event.Value);
    if (['pass', 'fail', 'skip'].includes(event.Action)) result.outcome = event.Action.toUpperCase();
    results.set(key, result);
  }
  const rows = [...results.values()].flatMap(result => [...result.tickets].map(ticket =>
    `| ${[ticket, result.package, result.test, result.outcome,
      result.outcome === 'PASS' ? 'fixed: graduate this test out of TestKnownRegressions' : ''].map(escapeCell).join(' | ')} |`));
  return ['## Known regressions', '', ...(rows.length ? [
    '| Ticket | Package | Test | Result | Next step |', '| --- | --- | --- | --- | --- |', ...rows,
  ] : ['No known_regression attributes were recorded. A suite without TestKnownRegressions is expected to pass with no tests to run.']), ''].join('\n');
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const summary = summarizeRegressions(readFileSync(process.argv[2], 'utf8'));
  appendFileSync(process.env.GITHUB_STEP_SUMMARY, summary);
}
