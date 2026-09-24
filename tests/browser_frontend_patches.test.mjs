import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';

const foundation = resolve(import.meta.dirname, '..');
const template = readFileSync(join(foundation, 'templates/frontend/vite.config.ts'), 'utf8');
const stale = template.replaceAll(String.raw`(?:\?.*)?`, '').replace(/^  optimizeDeps:.*\n/m, '');
const patch = (name, root) => {
  const result = spawnSync(process.execPath, [join(foundation, 'tooling/scripts', name), root], { encoding: 'utf8', timeout: 10000 });
  assert.equal(result.status, 0, result.stderr);
  return result.stdout;
};
const fixture = (t) => {
  const root = mkdtempSync(join(tmpdir(), 'foundation-patch-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  return root;
};

test('Docker migration copies linked browser packages before installation and stays idempotent', (t) => {
  const root = fixture(t);
  mkdirSync(join(root, 'frontend'));
  writeFileSync(join(root, 'frontend/package.json'), JSON.stringify({ dependencies: {
    '@ovasabi/config-contracts': 'file:../foundation/config-contracts/ts',
    '@ovasabi/runtime-browser': 'file:../foundation/runtime-sdk/ts/browser-host',
  } }));
  writeFileSync(join(root, 'Dockerfile'), `FROM node:22 AS frontend-builder
COPY foundation/ui-minimal/ts/package.json ./foundation/ui-minimal/ts/
RUN cd frontend && npm ci
COPY foundation/ui-minimal/ts ./foundation/ui-minimal/ts
RUN cd frontend && npm run build
`);
  const invoke = () => {
    const result = spawnSync('bash', [join(foundation, 'tooling/scripts/scaffold_managed_patches.sh'), root, '--only', 'runtime_native_dockerfile'], { encoding: 'utf8', timeout: 10000 });
    assert.equal(result.status, 0, result.stderr);
  };
  invoke();
  const result = readFileSync(join(root, 'Dockerfile'), 'utf8');
  for (const name of ['config-contracts/ts', 'runtime-sdk/ts/browser-host']) {
    assert.ok(result.includes(`COPY foundation/${name} ./foundation/${name}`));
    assert.ok(result.indexOf(`COPY foundation/${name}/package.json`) < result.indexOf('npm ci'));
  }
  assert.ok(!result.includes('foundation/runtime-native/ts'));
  invoke();
  assert.equal(readFileSync(join(root, 'Dockerfile'), 'utf8'), result);
});

test('upgrades existing Vite and Vitest plugins and remains idempotent', (t) => {
  const root = fixture(t);
  for (const name of ['vite.config.ts', 'vitest.config.ts']) writeFileSync(join(root, name), stale);
  assert.equal(patch('frontend_linaria_patch.mjs', root).split('\n').filter(Boolean).length, 2);
  const result = readFileSync(join(root, 'vite.config.ts'), 'utf8');
  assert.ok(result.includes(String.raw`(?:\?.*)?$`));
  assert.ok(result.includes("exclude: ['@ovasabi/ui-minimal']"));
  assert.ok(result.includes("prerenderShell({ entry: 'src/entry-server.tsx'"));
  assert.equal(patch('frontend_linaria_patch.mjs', root), '');
  assert.equal(readFileSync(join(root, 'vite.config.ts'), 'utf8'), result);
});

test('installs a missing plugin and preserves custom optimizer dependencies', (t) => {
  const root = fixture(t);
  writeFileSync(join(root, 'vite.config.ts'), `import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';
export default defineConfig({ plugins: [react(), projectPlugin()], optimizeDeps: { include: ['react'], exclude: ['custom-esm'] }, define: { CUSTOM: 'kept' } });`);
  assert.match(patch('frontend_linaria_patch.mjs', root), /^patched/);
  const result = readFileSync(join(root, 'vite.config.ts'), 'utf8');
  assert.ok(result.includes("exclude: ['@ovasabi/ui-minimal', 'custom-esm']"));
  assert.ok(result.includes("include: ['react']"));
  assert.ok(result.includes('projectPlugin()'));
  assert.equal(patch('frontend_linaria_patch.mjs', root), '');
});

test('preserves both existing project fixes and configuration factories', (t) => {
  const root = fixture(t);
  for (const variant of [template, template.replaceAll('(?:\\?.*)?', '(\\?.*)?'), template.replace('defineConfig({', 'defineConfig(() => ({').replace(/\}\)\s*$/, '}))')]) {
    writeFileSync(join(root, 'vite.config.ts'), variant);
    assert.equal(patch('frontend_linaria_patch.mjs', root), '');
    assert.equal(readFileSync(join(root, 'vite.config.ts'), 'utf8'), variant);
  }
});

test('reports dynamic or conflicting configuration without partial edits', (t) => {
  const root = fixture(t);
  for (const configuration of [
    stale.replace('resolve: {', "optimizeDeps: externalConfig, resolve: {"),
    stale.replace('resolve: {', "optimizeDeps: { exclude: getExclusions() }, resolve: {"),
    stale.replace('resolve: {', "optimizeDeps: { include: ['@ovasabi/ui-minimal'] }, resolve: {"),
    stale.replace('resolve: {', "optimizeDeps, resolve: {"),
    stale.replace('resolve: {', "optimizeDeps() { return {}; }, resolve: {"),
    stale.replace('resolve: {', "optimizeDeps: {}, optimizeDeps: {}, resolve: {"),
    stale.replace('resolve: {', "[optimizerKey]: {}, resolve: {"),
    stale.replace('transformLibraries: true', 'transformLibraries: false'),
    "export default getProjectConfig();",
  ]) {
    writeFileSync(join(root, 'vite.config.ts'), configuration);
    assert.match(patch('frontend_linaria_patch.mjs', root), /^manual/);
    assert.equal(readFileSync(join(root, 'vite.config.ts'), 'utf8'), configuration);
  }
});

test('upgrades only the known Rust build target and preserves unrelated targets', (t) => {
  const root = fixture(t);
  const legacy = readFileSync(join(foundation, 'tests/fixtures/browser_wasm_legacy.mk'), 'utf8');
  writeFileSync(join(root, 'Makefile'), `custom:\n\t@echo project-owned\n\n${legacy}`);
  assert.match(patch('browser_wasm_build_patch.mjs', root), /^patched/);
  const result = readFileSync(join(root, 'Makefile'), 'utf8');
  assert.ok(result.includes('build_browser_wasm.sh'));
  assert.ok(result.startsWith('custom:\n\t@echo project-owned\n'));
  assert.ok(result.endsWith('wasm-manifest:\n\t@echo manifest\n'));
  assert.equal(patch('browser_wasm_build_patch.mjs', root), '');
  const custom = legacy.replace('echo "Building Rust WASM modules..."', 'echo "Custom build"');
  writeFileSync(join(root, 'Makefile'), custom);
  assert.match(patch('browser_wasm_build_patch.mjs', root), /^manual/);
  assert.equal(readFileSync(join(root, 'Makefile'), 'utf8'), custom);
});

test('targeted managed patches record their changes and avoid unrelated patches', (t) => {
  const root = fixture(t);
  mkdirSync(join(root, 'frontend'));
  writeFileSync(join(root, 'frontend/vite.config.ts'), stale);
  writeFileSync(join(root, '.env.example'), 'GO_VERSION=1.20\n');
  const result = spawnSync('bash', [join(foundation, 'tooling/scripts/scaffold_managed_patches.sh'), root, '--only', 'frontend_linaria'], { encoding: 'utf8', timeout: 10000 });
  assert.equal(result.status, 0, result.stderr);
  assert.ok(readFileSync(join(root, '.foundation-patches.tsv'), 'utf8').includes('Linaria'));
  assert.equal(readFileSync(join(root, '.env.example'), 'utf8'), 'GO_VERSION=1.20\n');
});
