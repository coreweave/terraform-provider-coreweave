import assert from 'node:assert/strict';
import { existsSync, readFileSync, realpathSync } from 'node:fs';
import { delimiter, dirname, join } from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath, pathToFileURL } from 'node:url';

// Use the same installed package as the validator and writer.
const binary = process.env.PATH.split(delimiter).map(path => join(path, 'renovate-config-validator')).find(existsSync);
assert.ok(binary, 'Run through the workflow-pinned Renovate package.');
const root = dirname(dirname(realpathSync(binary)));
const require = createRequire(join(root, 'package.json'));
assert.equal(require('./package.json').version, '44.51.2');
const load = path => import(pathToFileURL(join(root, 'dist', `${path}.js`)));
const JSON5 = require('json5');
const { getConfig } = await load('config/defaults');
const { resolveConfigPresets } = await load('config/presets/index');
const { mergeChildConfig } = await load('config/utils');
const { GlobalConfig } = await load('config/global');
const { applyPackageRules } = await load('util/package-rules/index');
const { filterVersions } = await load('workers/repository/process/lookup/filter');
const { filterInternalChecks } = await load('workers/repository/process/lookup/filter-checks');
const versioning = await load('modules/versioning/index');
const { branchifyUpgrades } = await load('workers/repository/updates/branchify');
const repository = fileURLToPath(new URL('../../../', import.meta.url));
GlobalConfig.set({ localDir: repository, dryRun: 'full' });
const { init } = await load('logger/index');
await init();
const resolved = await resolveConfigPresets(JSON5.parse(readFileSync(join(repository, '.github/renovate.json5'), 'utf8')));
const config = mergeChildConfig(getConfig(), resolved.config);
Object.assign(config, { baseBranch: 'main', repoIsOnboarded: true, errors: [], warnings: [],
  semanticCommits: 'enabled' });

assert.equal(config.pruneStaleBranches, false);
assert.equal(config.dependencyDashboardApproval, false);
assert.equal(config.prConcurrentLimit, 3);
assert.equal(config.prHourlyLimit, 2);
assert.notEqual(config.prCreation, 'approval');

const { extractPackageFile: extractGo } = await load('modules/manager/gomod/extract');
const { extractPackageFile: extractMise } = await load('modules/manager/mise/extract');
const go = ['go.mod', 'tools/go.mod'].map(packageFile => ({ manager: 'gomod', packageFile,
  ...extractGo(readFileSync(join(repository, packageFile), 'utf8')).deps.find(dep => dep.depType === 'golang') }));
const mise = await extractMise(readFileSync(join(repository, '.config/mise.toml'), 'utf8'), '.config/mise.toml');
go.push({ manager: 'mise', packageFile: '.config/mise.toml', ...mise.deps.find(dep => dep.depName === 'go') });
const linter = { manager: 'mise', packageFile: '.config/mise.toml', ...mise.deps.find(dep => dep.depName === 'golangci-lint') };
const resolve = async (dep, extra = {}) => applyPackageRules({ ...config, ...dep, packageName: dep.packageName ?? dep.depName, ...extra }, 'fixture');
const extracted = extractGo(readFileSync(join(repository, 'go.mod'), 'utf8')).deps;
const indirectAPI = extracted.find(dep => dep.depName === 'google.golang.org/genproto/googleapis/api');
assert.equal(indirectAPI.enabled, false);
for (const dep of extracted.filter(dep => dep.depName.startsWith('google.golang.org/genproto/googleapis/'))) {
  assert.equal((await resolve({manager: 'gomod', packageFile: 'go.mod', ...dep})).enabled, true);
}
const releases = (...versions) => versions.map(version => ({ version }));
for (const dep of go) {
  const effective = await resolve(dep);
  assert.equal(effective.datasource, 'golang-version');
  assert.equal(effective.packageName ?? effective.depName, 'go');
  assert.equal(effective.minimumReleaseAge, '7 days');
  assert.deepEqual(filterVersions(effective, '1.26.6', undefined,
    releases('1.26.7', '1.27.0', '1.27.2'), versioning.get(effective.versioning ?? 'semver')).map(item => item.version), ['1.26.7']);
}
const old = new Date(Date.now() - 10 * 86_400_000).toISOString();
const young = new Date(Date.now() - 86_400_000).toISOString();
for (const dep of [go[0], linter]) {
  const effective = await resolve(dep);
  for (const next of dep === linter ? ['2.9.1', '2.10.0'] : ['1.26.7']) {
    for (const [releaseTimestamp, pending] of [[old, false], [young, true]]) {
      const result = await filterInternalChecks({ ...effective, currentVersion: dep.currentValue },
        versioning.get(effective.versioning ?? 'semver'), 'fixture', [{ version: next, releaseTimestamp }]);
      assert.equal(result.pendingChecks, pending);
    }
  }
}
console.log('PASS: extracted Go aliases reject unsupported minors; Go and linter release age is enforced');

const module = depName => ({ manager: 'gomod', packageFile: 'go.mod', depName, datasource: 'go', currentValue: 'v1.0.0' });
const cases = [
  ...go.map(dep => [dep, 'go-patches']), [linter, 'golangci-lint-maintenance'],
  ...['terraform-plugin-framework', 'terraform-plugin-framework-validators', 'terraform-plugin-framework-timeouts',
    'terraform-plugin-framework-jsontypes', 'terraform-plugin-framework-nettypes', 'terraform-plugin-go', 'terraform-plugin-testing',
    'terraform-plugin-log', 'terraform-plugin-sdk/v2'].map(name => [module(`github.com/hashicorp/${name}`), 'terraform-plugin-libraries']),
  [module('github.com/zclconf/go-cty'), 'terraform-plugin-libraries'],
  ...['cks', 'networking', 'cwobject', 'inference', 'container-registry-api', 'sandbox', 'workload-federation'].flatMap(family =>
    ['connectrpc/go/v2', 'protocolbuffers/go'].map(generator => [module(`buf.build/gen/go/coreweave/${family}/${generator}`), `coreweave-${family}-clients`])),
  ...['github.com/aws/aws-sdk-go-v2', 'github.com/aws/aws-sdk-go-v2/service/s3', 'github.com/aws/aws-sdk-go-v2/credentials',
    'github.com/aws/smithy-go'].map(name => [module(name), 'aws-sdk']),
  ...['terraform-plugin-docs', 'copywrite'].map(name => [module(`github.com/hashicorp/${name}`), 'documentation-tools']),
  [module('github.com/stretchr/testify'), 'go-dependencies'],
  [{manager: 'mise', packageFile: '.config/mise.toml', ...mise.deps.find(dep => dep.depName === 'terraform')}, 'development-tools'],
  [{manager: 'github-actions', packageFile: '.github/workflows/acceptance-test.yaml', depName: 'ghcr.io/coreweave/github-actions-images/github-base-runner', datasource: 'docker', currentValue: '1.21.0'}, 'ci-containers'],
  [{manager: 'github-actions', packageFile: '.github/workflows/test.yml', depName: 'actions/checkout', datasource: 'github-tags', currentValue: 'v1.0.0'}, 'github-actions'],
  [{manager: 'github-actions', packageFile: '.github/actions/setup/action.yml', depName: 'jdx/mise-action', datasource: 'github-tags', currentValue: 'v1.0.0'}, 'mise-runtime'],
  [{manager: 'regex', packageFile: '.github/actions/setup/action.yml', depName: 'jdx/mise', datasource: 'github-releases', currentValue: 'v1.0.0'}, 'mise-runtime'],
];
for (const [dep, expected] of cases) {
  const effective = await resolve(dep, {updateType: 'patch'});
  assert.equal(effective.groupSlug, expected, dep.depName);
  assert.equal(effective.dependencyDashboardApproval, false);
}
for (const dep of [module('cloud.google.com/go/longrunning'), cases.at(-2)[0]]) {
  const effective = await resolve(dep, {updateType: 'major'});
  assert.equal(effective.groupName, null);
  assert.equal(effective.dependencyDashboardApproval, false);
}
for (const dep of [module('github.com/aws/smithy-go'), module('github.com/stretchr/testify')]) {
  const security = await resolve(dep, {updateType: 'patch', isVulnerabilityAlert: true, ...config.vulnerabilityAlerts});
  assert.equal(security.groupName, null);
  assert.equal(security.groupSlug, null);
  assert.equal(security.dependencyDashboardApproval, false);
  assert.equal(security.minimumReleaseAge, '0 days');
}
console.log('PASS: specific compatibility groups override broad rules; majors and security stay separate');

const packageFiles = {};
for (const [dep] of cases) {
  const file = (packageFiles[dep.manager] ??= []).find(file => file.packageFile === dep.packageFile);
  const parts = dep.currentValue.replace(/^v/, '').split('.').map(Number);
  parts[2] += 1;
  const newValue = `${dep.currentValue.startsWith('v') ? 'v' : ''}${parts.join('.')}`;
  const entry = { ...dep, packageName: dep.packageName ?? dep.depName, updates: [{ updateType: 'patch', newVersion: newValue,
    newValue, newMajor: parts[0], newMinor: parts[1] }] };
  if (file) file.deps.push(entry);
  else packageFiles[dep.manager].push({packageFile: dep.packageFile, deps: [entry]});
}
const planned = await branchifyUpgrades(config, packageFiles);
const goBranch = planned.branches.find(branch => branch.groupSlug === 'go-patches');
assert.equal(goBranch.upgrades.length, 3);
assert.ok(!goBranch.upgrades.some(dep => dep.depName.includes('lint')));
for (const family of ['cks', 'networking', 'cwobject', 'inference', 'container-registry-api', 'sandbox', 'workload-federation']) {
  assert.equal(planned.branches.find(branch => branch.groupSlug === `coreweave-${family}-clients`).upgrades.length, 2);
}
assert.equal(planned.branches.find(branch => branch.groupSlug === 'aws-sdk').upgrades.length, 4);

for (const branch of planned.branches) {
  assert.equal(branch.dependencyDashboardApproval, false);
}
console.log(`PASS: ${planned.branches.length} routine groups are planned without dashboard approval`);

const securityFiles = {gomod: [{packageFile: 'go.mod', deps: ['github.com/aws/smithy-go', 'github.com/stretchr/testify'].map(depName => ({
  ...module(depName), packageName: depName, isVulnerabilityAlert: true, force: {...config.vulnerabilityAlerts},
  updates: [{updateType: 'patch', newVersion: 'v1.0.1', newValue: 'v1.0.1', newMajor: 1, newMinor: 0}],
}))}]};
const securityBranches = (await branchifyUpgrades(config, securityFiles)).branches;
assert.equal(securityBranches.length, 2);
const {setCount, isLimitReached} = await load('workers/global/limits');
for (const [concurrent, hourly, reached] of [[0, 0, false], [2, 1, false], [3, 0, true], [0, 2, true]]) {
  setCount('ConcurrentPRs', concurrent);
  setCount('HourlyPRs', hourly);
  for (const branch of planned.branches) {
    assert.equal(isLimitReached('ConcurrentPRs', branch), reached);
  }
}
setCount('ConcurrentPRs', 24);
setCount('HourlyPRs', 24);
for (const branch of securityBranches) {
  assert.equal(branch.isVulnerabilityAlert, true);
  assert.equal(branch.dependencyDashboardApproval, false);
  assert.ok(branch.groupName == null);
  const {checkMinimumReleaseAge} = await load('util/minimum-release-age');
  assert.equal(checkMinimumReleaseAge(branch, young).isPending, false);
  assert.equal(isLimitReached('ConcurrentPRs', branch), false);
}
console.log('PASS: routine creation budgets are enforced; security updates bypass groups, age and budgets');
