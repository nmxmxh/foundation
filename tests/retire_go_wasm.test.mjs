import { test } from 'node:test';
import assert from 'node:assert/strict';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';

const foundation = resolve(import.meta.dirname, '..');
const script = join(foundation, 'tooling/scripts/retire_go_wasm_patch.mjs');
const fixture = (t) => {
  const root = mkdtempSync(join(tmpdir(), 'retire-go-wasm-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const put = (name, value) => {
    mkdirSync(join(root, name, '..'), { recursive: true });
    writeFileSync(join(root, name), value);
  };
  put('wasm/main.go', readFileSync(join(foundation, 'tests/fixtures/retired_go_wasm.go.txt')));
  const legacy = readFileSync(join(foundation, 'tests/fixtures/retired_go_wasm.mk'), 'utf8');
  put('Makefile', `.PHONY: build-runtime build-wasm build-wasm-dev build-rust-wasm\nbuild-runtime: build-wasm build-rust-wasm\n${legacy}build-rust-wasm:\n\t@echo rust-kept\ncustom:\n\t@echo project-owned\n`);
  return { root, put };
};
const run = (root, ...args) => spawnSync(process.execPath, [script, root, ...args], { encoding: 'utf8', timeout: 10000 });

test('retires the shim, build wiring, and Go artifacts while preserving Rust', (t) => {
  const { root, put } = fixture(t);
  const goWasm = Buffer.from([0,97,115,109,1,0,0,0,1,4,1,96,0,0,2,10,1,4,103,111,106,115,1,120,0,0]);
  put('frontend/public/main.wasm', goWasm);
  put('frontend/public/main.wasm.br', 'compressed artifact');
  put('frontend/public/wasm_exec.js', '// The Go Authors\nglobalThis.Go = class {};');
  put('frontend/public/modules/compute.wasm', 'Rust artifact');
  put('frontend/public/runtime/wasm-manifest.json', JSON.stringify({ schemaVersion: 1, artifacts: [{role: 'go-compat'}, {role: 'rust-module', url: '/modules/compute.wasm'}] }));
  put('go.work', 'go 1.26.0\nuse (\n\t.\n\t./wasm\n\t./foundation/runtime-sdk/go\n)\n');
  const dryRun = run(root, '--dry-run');
  assert.equal(dryRun.status, 0, dryRun.stdout);
  assert.ok(existsSync(join(root, 'wasm/main.go')));
  const result = run(root);
  assert.equal(result.status, 0, result.stdout);
  assert.equal(existsSync(join(root, 'wasm')), false);
  assert.equal(existsSync(join(root, 'frontend/public/main.wasm')), false);
  assert.equal(existsSync(join(root, 'frontend/public/main.wasm.br')), false);
  assert.equal(readFileSync(join(root, 'frontend/public/modules/compute.wasm'), 'utf8'), 'Rust artifact');
  assert.match(readFileSync(join(root, 'Makefile'), 'utf8'), /echo project-owned/);
  assert.doesNotMatch(readFileSync(join(root, 'Makefile'), 'utf8'), /\bbuild-wasm\b/);
  assert.doesNotMatch(readFileSync(join(root, 'go.work'), 'utf8'), /\.\/wasm/);
  assert.equal(JSON.parse(readFileSync(join(root, 'frontend/public/runtime/wasm-manifest.json'))).artifacts.length, 1);
  assert.equal(run(root).stdout, '');
});

test('custom sources, recipes, and remaining consumers prevent partial retirement', (t) => {
  for (const [name, contents] of [
    ['wasm/custom.go', 'package main'],
    ['frontend/src/legacy.ts', 'window.sendWasmMessage(command);'],
    ['Makefile', 'build-wasm:\n\t@echo custom-build\n'],
    ['frontend/public/main.wasm', Buffer.from([0,97,115,109,1,0,0,0])],
  ]) {
    const { root, put } = fixture(t);
    put(name, contents);
    const before = readFileSync(join(root, 'wasm/main.go'));
    const result = run(root);
    assert.equal(result.status, 1);
    assert.match(result.stdout, /^manual /);
    assert.deepEqual(readFileSync(join(root, 'wasm/main.go')), before);
    assert.deepEqual(readFileSync(join(root, name)), Buffer.from(contents));
  }
});

test('new scaffolds never include the retired source or build targets', () => {
  assert.equal(existsSync(join(foundation, 'templates/wasm')), false);
  for (const name of ['templates/Makefile', 'templates/scaffold.manifest.tsv', 'templates/github/workflows/ci.yml']) {
    assert.doesNotMatch(readFileSync(join(foundation, name), 'utf8'), /\bbuild-wasm\b|templates\/wasm|wasm\/main\.go/);
  }
});

test('artifact manifests omit retired Go artifacts', (t) => {
  const { root, put } = fixture(t);
  put('frontend/public/main.wasm', 'retired Go artifact');
  put('frontend/public/kernel.wasm', 'kernel artifact');
  put('frontend/public/modules/compute.shared.wasm', 'Rust artifact');
  const publicDir = join(root, 'frontend/public');
  const result = spawnSync(process.execPath, [
    join(foundation, 'tooling/scripts/wasm_manifest.mjs'), '--public-dir', publicDir,
  ], { encoding: 'utf8', timeout: 10000 });
  assert.equal(result.status, 0, result.stderr);
  const manifest = JSON.parse(readFileSync(join(publicDir, 'runtime/wasm-manifest.json')));
  assert.deepEqual(manifest.artifacts.map(({ url }) => url), [
    '/kernel.wasm', '/modules/compute.shared.wasm',
  ]);
});
