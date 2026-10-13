import assert from 'node:assert/strict';
import test from 'node:test';
import { existsSync, readFileSync } from 'node:fs';

const workflow = readFileSync(new URL('../workflows/test.yml', import.meta.url), 'utf8');
const sections = workflow.split(/^  (?=[a-z][a-z-]*:\n)/m);
const job = id => {
  const section = sections.find(part => part.startsWith(`${id}:\n`));
  assert.ok(section, `${id} job exists`);
  return section;
};

test('QA depends on selection, compilation, lint and module/unit checks', () => {
  const qa = job('acceptance-tests');
  const dependencies = /^    needs: \[([^\]]+)\]$/m.exec(qa);
  assert.ok(dependencies, 'QA must declare its prerequisites');
  assert.deepEqual(dependencies[1].split(',').map(id => id.trim()).sort(), ['build', 'lint', 'select-acceptance', 'test']);
  assert.match(qa, /^    if: needs\.select-acceptance\.outputs\.suites != '\[\]'$/m);
  assert.doesNotMatch(qa, /always\(|continue-on-error:/);
  assert.match(qa, /^    uses: \.\/\.github\/workflows\/acceptance-test\.yaml$/m);
});

test('the gate jobs execute the required commands without ignoring failures', () => {
  assert.match(job('build'), /^      native: true$/m);
  const build = readFileSync(new URL('../workflows/build.yml', import.meta.url), 'utf8');
  assert.match(build, /run: go build -v \.\/\.\.\./);
  assert.match(job('lint'), /run: make lint/);
  assert.match(job('test'), /run: go mod tidy -diff/);
  assert.match(job('test'), /run: go test -v -cover \.\/\.\.\./);
  for (const id of ['build', 'select-acceptance', 'lint', 'test']) {
    assert.doesNotMatch(job(id), /continue-on-error:|always\(/);
  }
});

test('one PR entry point covers documentation without a second acceptance trigger', () => {
  assert.match(workflow, /^  pull_request:\n    types: \[opened, synchronize, reopened\]$/m);
  assert.doesNotMatch(workflow, /paths-ignore:/);
  assert.equal(existsSync(new URL('../workflows/acceptance-tests.yaml', import.meta.url)), false);
});

test('release-only success requires the full QA matrix and normal validation without permitted skips', () => {
  const marker = job('release-acceptance');
  assert.match(marker, /^    name: Release acceptance$/m);
  assert.match(marker, /^    needs: \[select-acceptance, validation, acceptance-tests\]$/m);
  assert.match(marker, /^    if: needs\.select-acceptance\.outputs\.suites != '\[\]'$/m);
  assert.match(marker, /uses: re-actors\/alls-green@/);
  assert.doesNotMatch(marker, /allowed-skips:|continue-on-error:|always\(/);
});
