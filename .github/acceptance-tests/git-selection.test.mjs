import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { selectSuites } from './select-suites.mjs';

const collector = fileURLToPath(new URL('./collect-changes.sh', import.meta.url));

function fixture(t, initialFiles) {
  const dir = mkdtempSync(join(tmpdir(), 'acceptance-git-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const env = { ...process.env, GIT_CONFIG_GLOBAL: '/dev/null', GIT_CONFIG_NOSYSTEM: '1' };
  const git = (...args) => execFileSync('git', args, { cwd: dir, env, encoding: 'utf8' }).trim();
  const write = (path, contents) => {
    mkdirSync(dirname(join(dir, path)), { recursive: true });
    writeFileSync(join(dir, path), contents);
  };
  const commit = subject => {
    git('add', '.');
    git('commit', '-qm', subject);
    return git('rev-parse', 'HEAD');
  };
  git('init', '-q', '-b', 'main', '--template=');
  git('config', 'user.email', 'fixture@example.com');
  git('config', 'user.name', 'Acceptance fixture');
  git('config', 'commit.gpgsign', 'false');
  for (const [path, contents] of Object.entries(initialFiles)) write(path, contents);
  const ancestor = commit('chore: fixture baseline');
  return { dir, env, git, write, commit, ancestor };
}

function collect(f, base, head, merge) {
  const output = join(f.dir, 'selection');
  mkdirSync(output);
  execFileSync('bash', [collector, output], {
    cwd: f.dir,
    env: { ...f.env, BASE_SHA: base, HEAD_SHA: head, GITHUB_SHA: merge },
  });
  const lines = name => readFileSync(join(output, name), 'utf8').split('\n').filter(Boolean);
  const files = lines('files.txt');
  const subjects = lines('subjects.txt');
  const suites = selectSuites({ state: 'open', head: { sha: head }, title: 'chore: fixture update', labels: [] }, head, files, subjects);
  return { files, subjects, suites };
}

for (const path of ['go.mod', 'coreweave/cks/resource.go']) {
  test(`an identical ${path} change already on base contributes no merge changes`, t => {
    const f = fixture(t, { [path]: 'original\n' });
    f.write(path, 'updated\n');
    const base = f.commit('chore: base update');
    f.git('checkout', '-qb', 'pr', f.ancestor);
    f.write(path, 'updated\n');
    const head = f.commit('chore: identical PR update');
    f.git('checkout', '-q', 'main');
    f.git('merge', '--no-ff', '-qm', 'merge fixture', head);
    const merge = f.git('rev-parse', 'HEAD');
    assert.equal(f.git('rev-parse', `${base}^{tree}`), f.git('rev-parse', `${merge}^{tree}`));
    assert.equal(f.git('diff', '--name-only', `${base}...${head}`), path);
    const result = collect(f, base, head, merge);
    assert.deepEqual(result.files, []);
    assert.deepEqual(result.suites, []);
  });
}

test('a cross-suite move selects the source and destination suites', t => {
  const f = fixture(t, { 'coreweave/cks/resource.go': 'package fixture\n'.repeat(10) });
  const base = f.ancestor;
  f.git('checkout', '-qb', 'pr');
  mkdirSync(join(f.dir, 'coreweave/networking'));
  f.git('mv', 'coreweave/cks/resource.go', 'coreweave/networking/resource.go');
  const head = f.commit('chore: move resource');
  f.git('checkout', '-q', 'main');
  f.git('merge', '--no-ff', '-qm', 'merge fixture', head);
  const merge = f.git('rev-parse', 'HEAD');
  assert.equal(f.git('diff', '--name-only', `${base}...${merge}`), 'coreweave/networking/resource.go');
  const result = collect(f, base, head, merge);
  assert.deepEqual(result.files, ['coreweave/cks/resource.go', 'coreweave/networking/resource.go']);
  assert.deepEqual(result.suites, ['cks', 'networking']);
});

test('base-only commit scopes are excluded while PR commit scopes select suites', t => {
  const f = fixture(t, { 'fixture.txt': 'original\n' });
  f.write('base-only.txt', 'unrelated base change\n');
  const base = f.commit('fix(cks): unrelated base change');
  f.git('checkout', '-qb', 'pr', f.ancestor);
  f.write('fixture.txt', 'PR change\n');
  const head = f.commit('test(networking): PR change');
  f.git('checkout', '-q', 'main');
  f.git('merge', '--no-ff', '-qm', 'merge fixture', head);
  const merge = f.git('rev-parse', 'HEAD');
  const result = collect(f, base, head, merge);
  assert.deepEqual(result.files, ['fixture.txt']);
  assert.deepEqual(result.subjects, ['test(networking): PR change']);
  assert.deepEqual(result.suites, ['networking']);
});
