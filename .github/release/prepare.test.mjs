import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { execFileSync, spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const script = fileURLToPath(new URL('./prepare.sh', import.meta.url));
function fixture(t) {
  const dir = mkdtempSync(join(tmpdir(), 'release-prepare-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const env = { ...process.env, GIT_CONFIG_GLOBAL: '/dev/null', GIT_CONFIG_NOSYSTEM: '1' };
  const git = (...args) => execFileSync('git', args, { cwd: dir, env, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim();
  git('init', '-q', '-b', 'main', '--template=');
  git('config', 'user.name', 'Release fixture');
  git('config', 'user.email', 'fixture@example.com');
  git('config', 'commit.gpgsign', 'false');
  git('config', 'tag.gpgsign', 'false');
  writeFileSync(join(dir, '.release-please-manifest.json'), JSON.stringify({ '.': '1.2.3' }));
  writeFileSync(join(dir, 'CHANGELOG.md'), '## [1.2.3]\n\nCurrent notes\n\n## [1.2.2]\n\nPrevious notes\n');
  git('add', '.'); git('commit', '-qm', 'chore: release fixture');
  const commit = git('rev-parse', 'HEAD');
  git('init', '--bare', '-q', 'remote.git');
  git('remote', 'add', 'origin', join(dir, 'remote.git'));
  mkdirSync(join(dir, 'bin'));
  writeFileSync(join(dir, 'bin', 'gh'), `#!/bin/sh
case "$MOCK_RELEASE" in
  missing) echo 'gh: Not Found (HTTP 404)' >&2; exit 1 ;;
  error) echo 'gh: Server error (HTTP 500)' >&2; exit 1 ;;
  draft) echo '{"draft":true}' ;;
  published) echo '{"draft":false}' ;;
  malformed) echo '{}' ;;
esac
`, { mode: 0o755 });
  const run = status => {
    const output = join(dir, 'output'); writeFileSync(output, '');
    const result = spawnSync('bash', [script], { cwd: dir, encoding: 'utf8', env: { ...env,
      PATH: `${join(dir, 'bin')}:${env.PATH}`, RELEASE_COMMIT: commit, RUNNER_TEMP: dir, GITHUB_OUTPUT: output,
      GITHUB_REPOSITORY: 'public/fixture', MOCK_RELEASE: status } });
    return { ...result, output: readFileSync(output, 'utf8') };
  };
  return { dir, git, commit, run };
}

test('a new release tags the validated commit and extracts only its notes', t => {
  const f = fixture(t);
  const result = f.run('missing');
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.output, 'published=false\ntag=v1.2.3\n');
  assert.equal(f.git('rev-parse', 'v1.2.3^{commit}'), f.commit);
  assert.match(f.git('ls-remote', 'origin', 'refs/tags/v1.2.3'), new RegExp(`^${f.commit}\\s`));
  assert.match(readFileSync(join(f.dir, 'release-notes.md'), 'utf8'), /Current notes/);
  assert.doesNotMatch(readFileSync(join(f.dir, 'release-notes.md'), 'utf8'), /Previous notes/);
});

test('draft recovery rebuilds while completed publication skips rebuilding', t => {
  const f = fixture(t);
  assert.equal(f.run('draft').output, 'published=false\ntag=v1.2.3\n');
  const result = f.run('published');
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.output, 'published=true\ntag=v1.2.3\n');
});

test('an existing tag on a different commit cannot be moved', t => {
  const f = fixture(t);
  f.git('commit', '--allow-empty', '-qm', 'chore: unrelated fixture');
  const other = f.git('rev-parse', 'HEAD');
  f.git('tag', 'v1.2.3', other);
  const result = f.run('missing');
  assert.notEqual(result.status, 0);
  assert.equal(result.output, '');
  assert.equal(f.git('rev-parse', 'v1.2.3^{commit}'), other);
  assert.equal(f.git('ls-remote', 'origin', 'refs/tags/v1.2.3'), '');
});

test('lookup failures and malformed release metadata do not authorize publication', t => {
  const f = fixture(t);
  for (const status of ['error', 'malformed']) {
    const result = f.run(status);
    assert.notEqual(result.status, 0);
    assert.equal(result.output, '');
  }
});

test('invalid versions and missing notes fail preparation', t => {
  const f = fixture(t);
  writeFileSync(join(f.dir, '.release-please-manifest.json'), JSON.stringify({ '.': 'invalid' }));
  assert.notEqual(f.run('missing').status, 0);
  writeFileSync(join(f.dir, '.release-please-manifest.json'), JSON.stringify({ '.': '1.2.3' }));
  writeFileSync(join(f.dir, 'CHANGELOG.md'), '## [1.2.2]\nOther notes\n');
  assert.notEqual(f.run('missing').status, 0);
});

// The publication step follows successful GoReleaser completion; interrupted drafts can be rebuilt.
test('publication and tagged labeling follow successful artifact creation', () => {
  const workflow = readFileSync(new URL('../workflows/release.yml', import.meta.url), 'utf8');
  assert.ok(workflow.indexOf('- name: Publish completed release') > workflow.indexOf('- name: Run GoReleaser'));
  assert.ok(workflow.indexOf('- name: Mark release PR as tagged') > workflow.indexOf('- name: Publish completed release'));
  assert.match(workflow, /if: steps\.prepare\.outputs\.published != 'true'/);
  assert.doesNotMatch(workflow, /continue-on-error:|always\(/);
  const config = readFileSync(new URL('../../.goreleaser.yml', import.meta.url), 'utf8');
  assert.match(config, /target_commitish: '\{\{ \.FullCommit \}\}'/);
  assert.match(config, /draft: true\n  replace_existing_draft: true/);
});
