import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const cwd = mkdtempSync(join(tmpdir(), 'renovate-validator-'));
const fixtures = fileURLToPath(new URL('.', import.meta.url));

function validate(args, workingDirectory = cwd) {
  const result = spawnSync('renovate-config-validator', args, {
    cwd: workingDirectory,
    encoding: 'utf8',
    timeout: 60_000,
  });
  assert.ifError(result.error);
  assert.equal(result.signal, null, 'validator was terminated');
  assert.doesNotMatch(result.stdout + result.stderr, /RE2 not usable/, 'RE2 must be available');
  return result;
}

try {
  const version = validate(['--version']);
  assert.equal(version.status, 0);
  assert.equal(version.stdout.trim(), '44.51.2');

  const candidate = join(cwd, 'candidate');
  mkdirSync(candidate);
  copyFileSync(join(fixtures, 'valid.json5'), join(candidate, 'renovate.json5'));
  const marker = join(candidate, 'global-config-ran');
  writeFileSync(join(candidate, 'config.js'),
    `require('node:fs').writeFileSync(${JSON.stringify(marker)}, 'executed'); module.exports = {};\n`);
  const control = validate(['--strict', '--no-global', join(candidate, 'renovate.json5')], candidate);
  assert.equal(control.status, 0, `${control.stdout}${control.stderr}`);
  assert.equal(existsSync(marker), true, 'control must discover executable global config');
  rmSync(marker);
  const isolated = validate(['--strict', '--no-global', join(candidate, 'renovate.json5')]);
  assert.equal(isolated.status, 0, `${isolated.stdout}${isolated.stderr}`);
  assert.equal(existsSync(marker), false, 'candidate global config executed during isolated validation');
  assert.equal(existsSync(join(candidate, 'artifact-command-ran')), false, 'control executed artifact command');
  console.log('PASS: adjacent executable global config is not loaded');

  for (const [file, expectedStatus, diagnostic] of [
    ['valid.json5', 0, /Config validated successfully/],
    ['invalid.json5', 1, /minimumReleaseAge/],
    ['global-option.json5', 1, /global option reserved/],
    ['migration.json5', 1, /Config migration necessary/],
  ]) {
    const result = validate(['--strict', '--no-global', join(fixtures, file)]);
    assert.equal(result.status, expectedStatus, `${file}: ${result.stdout}${result.stderr}`);
    assert.match(result.stdout + result.stderr, diagnostic, file);
    assert.equal(existsSync(join(cwd, 'artifact-command-ran')), false, 'artifact command executed');
    console.log(`PASS: ${file}`);
  }
} finally {
  rmSync(cwd, { recursive: true, force: true });
}
