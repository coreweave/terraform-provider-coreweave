import assert from 'node:assert/strict';
import test from 'node:test';
import { existsSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';

test('the workflow title command accepts conventional titles and rejects invalid titles', t => {
  const grep = spawnSync('grep', ['-P', 'a'], { input: 'a', encoding: 'utf8' });
  if (grep.status !== 0) {
    t.skip('The workflow uses GNU grep with PCRE support, available on its Linux runner.');
    return;
  }
  const workflow = readFileSync(new URL('../workflows/test.yml', import.meta.url), 'utf8');
  const block = workflow.split('\n  pr-title-format:\n')[1]?.split(/^  (?=[a-z][a-z-]*:\n)/m)[0];
  assert.ok(block);
  const source = block.split('        run: |\n')[1];
  assert.ok(source);
  const script = source.split('\n').map(line => line.replace(/^ {10}/, '')).join('\n');
  const ghFixture = `gh() {
    [[ "$*" == 'api repos/public/fixture/pulls/7 --jq .title' ]] || return 2
    [[ "$FIXTURE_API_FAILURE" != true ]] || return 1
    printf '%s\\n' "$FIXTURE_PR_TITLE"
  }\n`;
  const check = (title, extra = {}) => spawnSync('bash', ['-e', '-c', ghFixture + script], {
    env: { ...process.env, LC_ALL: 'C.UTF-8', PR_TITLE: 'invalid original event title',
      FIXTURE_PR_TITLE: title, FIXTURE_API_FAILURE: 'false', GITHUB_REPOSITORY: 'public/fixture',
      PR_NUMBER: '7', ...extra }, encoding: 'utf8', timeout: 10_000,
  });
  for (const title of ['fix: update module', 'ci(validation): aggregate PR results',
    'feat(api)!: change configuration', 'docs: 📝 clarify setup']) {
    const result = check(title);
    assert.equal(result.status, 0, `${title}\n${result.stderr}`);
  }
  for (const title of ['', 'update module', 'fix: ', 'fix : missing separator']) {
    const result = check(title);
    assert.equal(result.status, 1, `${title}\n${result.stderr}`);
    assert.match(result.stdout, /PR title does not match conventional commits format/);
  }
  assert.equal(check('fix: corrected current title').status, 0, 'A corrected live title must supersede the original event title.');
  assert.equal(check('fix: valid title', { FIXTURE_API_FAILURE: 'true' }).status, 1, 'API failures must fail title validation.');
  const directory = mkdtempSync(join(tmpdir(), 'pr-title-'));
  try {
    const sentinel = join(directory, 'unexpected-command');
    const result = check(`fix: literal $(touch ${sentinel})`);
    assert.equal(result.status, 0, result.stderr);
    assert.equal(existsSync(sentinel), false, 'The PR title must be treated as data.');
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});
