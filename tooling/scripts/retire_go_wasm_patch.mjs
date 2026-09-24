#!/usr/bin/env node
/** Retire recognized Go browser shims without changing Rust WASM modules. */
import { createHash } from 'node:crypto';
import { existsSync, lstatSync, readFileSync, readdirSync, rmdirSync, unlinkSync, writeFileSync } from 'node:fs';
import { join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const digest = (bytes) => createHash('sha256').update(bytes).digest('hex');
const shimDigest = '7e38642aab06a5341f6c240eb9d7626c8597afd393d82c4e565dba0d6681bc51';
const recipeDigests = new Map([
  ['build-wasm', '8e36a6cbc95d51bf45632d521509d8850e22d064e62be0c057309196c68866c5'],
  ['build-wasm-dev', '2a18fc3a1b2f6c134a502045cbf8f42bedb459be5adf9683b73eb70c6af0478d'],
]);
const retiredFiles = new Map([
  ['README.md', '033e1796f264546f31ada59495295f2dbc7f84773b290e020f6830cc060221e5'],
  ['go.mod', '10efa223a0365f4784474bc7fc987cfccb49a7bd660fb314c0b29cc171d6d620'],
  ['Makefile', '3b965009857d11af6d373d246d2b69220ee900af28cc139323b3154963e882ad'],
  ['internal/bridge/bridge.go', '7c2e103f3f09a8433c14b9d79b215c979dd124b312c7fba0cedb9bb824f0c0d6'],
  ['internal/compress/compress.go', 'd22a2dd3d46ffeec31dd733e76942cbfaac2ce6b6a56fc12f85c39c9d68dd75a'],
]);
const legacyConsumer = /\b(?:sendWasmMessage|wasmReady|onWasmMessage|__WASM_GLOBAL_METADATA|emitWasmCompatMessage)\b|wasm_exec\.js|[/'"]main\.wasm/;
const ciStep = "      - name: Compile WASM runtime shim\n        if: hashFiles('wasm/main.go') != ''\n        run: make build-wasm\n";

function read(file) {
  const info = lstatSync(file);
  if (!info.isFile() || info.size > 32 * 1024 * 1024) throw new Error(`review unsupported file: ${file}`);
  return readFileSync(file);
}

function walk(directory, limit = 4096, depth = 0, files = []) {
  if (!existsSync(directory)) return files;
  if (depth > 16 || lstatSync(directory).isSymbolicLink()) throw new Error(`review directory: ${directory}`);
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    if (files.length >= limit) throw new Error(`file scan exceeds ${limit} entries`);
    const path = join(directory, entry.name);
    if (entry.isDirectory()) walk(path, limit, depth + 1, files);
    else files.push(path);
  }
  return files;
}

function goArtifact(bytes) {
  try {
    return WebAssembly.Module.imports(new WebAssembly.Module(bytes)).some(({ module }) => module === 'gojs' || module === 'go');
  } catch { return false; }
}

function goSupport(bytes) {
  const text = bytes.toString();
  return text.includes('The Go Authors') && text.includes('globalThis.Go = class');
}

function recognizedShim(bytes) {
  const normalized = bytes.toString()
    .replace(/(Set\("wasmVersion", js.ValueOf\(")[^"]*("\)\))/, '$1VERSION$2')
    .replace(/\[.*? wasm\]/, '[PROJECT wasm]');
  return digest(normalized) === shimDigest;
}

function retireMakefile(code) {
  for (const [name, expected] of recipeDigests) {
    const pattern = new RegExp(`^${name}:[\\s\\S]*?(?=^[\\w-]+:|(?![\\s\\S]))`, 'm');
    const found = code.match(pattern)?.[0];
    if (!found) continue;
    if (digest(found) !== expected) throw new Error(`review custom Makefile target: ${name}`);
    code = code.replace(found, '');
  }
  code = code.replace(/^(?:\.PHONY|build-runtime|verify):.*$/gm,
    (line) => line.replace(/\s+build-wasm(?:-dev)?(?=\s|$)/g, '').trimEnd());
  for (const name of ['main.wasm', 'main.wasm.br', 'wasm_exec.js']) {
    code = code.replaceAll(`"$(WASM_PUBLIC_DIR)/${name}" `, '');
  }
  if (/\bbuild-wasm(?:-dev)?\b|wasm_exec\.js|main\.wasm/.test(code)) throw new Error('review remaining Makefile Go WASM references');
  return code;
}

function retireChecks(path, code) {
  if (path.endsWith('/wasm_manifest.mjs')) return code.replace('  ["main.wasm", "go-compat"],\n', '');
  if (path.endsWith('/scaffold_managed_patches.sh')) return code.replace('    "./wasm"\n', '');
  if (path.endsWith('/project_scaffold_check.sh')) return code
    .replace(/^.*check_exists "wasm entry".*\n/gm, '')
    .replace(/^.*check_file_contains "wasm runtime-transport shim".*\n/gm, '')
    .replace('wasm optimizer enables Go non-trapping float-to-int', 'wasm optimizer enables non-trapping float-to-int');
  if (path.endsWith('/go_static_analysis_check.sh')) return code.replace(
    /\nwasm_compile\(\) \{[\s\S]*?(?=echo "Go static analysis check passed")/, '\n');
  return code.replace('  "$target/wasm" \\\n', '').replace('  "$target/templates/wasm"; do', '  "$target/templates/frontend/src"; do');
}

export function planRetirement(root) {
  const changes = [];
  const add = (path, after = null) => {
    const before = read(join(root, path));
    if (after !== null && before.toString() === after) return;
    changes.push({ path, beforeHash: digest(before), after });
  };
  const sources = walk(join(root, 'frontend/src')).filter((path) => /\.[jt]sx?$/.test(path));
  for (const path of [...sources, join(root, 'frontend/index.html'), join(root, 'frontend/package.json')]) {
    if (existsSync(path) && legacyConsumer.test(read(path).toString())) throw new Error(`migrate legacy consumer: ${relative(root, path)}`);
  }
  for (const file of walk(join(root, 'wasm'), 128)) {
    const path = relative(join(root, 'wasm'), file).replaceAll('\\', '/');
    const bytes = read(file);
    const recognized = path === 'main.go' ? recognizedShim(bytes)
      : path === '.DS_Store' || digest(bytes) === retiredFiles.get(path)
        || (path.startsWith('build/') && (path.endsWith('.wasm') ? goArtifact(bytes) : path.endsWith('/wasm_exec.js') && goSupport(bytes)));
    if (!recognized) throw new Error(`review custom legacy source: wasm/${path}`);
    add(`wasm/${path}`);
  }
  for (const directory of ['frontend/public', 'frontend/dist']) {
    const main = join(root, directory, 'main.wasm');
    if (existsSync(main)) {
      if (!goArtifact(read(main))) throw new Error(`review non-Go artifact: ${directory}/main.wasm`);
      add(`${directory}/main.wasm`);
      for (const suffix of ['.br', '.gz', '.opt']) {
        if (existsSync(main + suffix)) add(`${directory}/main.wasm${suffix}`);
      }
    }
    const support = join(root, directory, 'wasm_exec.js');
    if (existsSync(support)) {
      if (!goSupport(read(support))) throw new Error(`review custom Go support: ${directory}/wasm_exec.js`);
      add(`${directory}/wasm_exec.js`);
    }
    const manifest = `${directory}/runtime/wasm-manifest.json`;
    if (existsSync(join(root, manifest))) {
      const original = read(join(root, manifest)).toString();
      const data = JSON.parse(original);
      const artifacts = data.artifacts.filter((artifact) => artifact.role !== 'go-compat');
      if (artifacts.length !== data.artifacts.length) add(manifest, `${JSON.stringify({ ...data, artifacts }, null, 2)}\n`);
    }
  }
  const edits = [
    ['Makefile', retireMakefile],
    ['go.work', (code) => code.replace(/^\s*\.\/wasm\s*\n/gm, '')],
    ['.github/workflows/ci.yml', (code) => {
      const next = code.replace(ciStep, '');
      if (/build-wasm|wasm\/main\.go/.test(next)) throw new Error('review custom Go WASM CI step');
      return next;
    }],
  ];
  for (const name of ['wasm_manifest.mjs', 'scaffold_managed_patches.sh', 'project_scaffold_check.sh', 'go_static_analysis_check.sh', 'dynamic_payload_practices_check.sh']) {
    const path = `scripts/checks/${name}`;
    edits.push([path, (code) => retireChecks(path, code)]);
  }
  for (const [path, transform] of edits) {
    if (existsSync(join(root, path))) add(path, transform(read(join(root, path)).toString()));
  }
  return changes;
}

function removeEmpty(directory, depth = 0) {
  if (!existsSync(directory) || depth > 16) return;
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    if (entry.isDirectory()) removeEmpty(join(directory, entry.name), depth + 1);
  }
  if (readdirSync(directory).length === 0) rmdirSync(directory);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const root = process.argv[2];
    if (!root || !existsSync(root)) process.exit(0);
    const changes = planRetirement(root);
    if (process.argv.includes('--dry-run')) {
      console.log(JSON.stringify(changes.map(({ path, beforeHash, after }) => ({ path, beforeHash, action: after === null ? 'remove' : 'update' })), null, 2));
    } else {
      for (const { path, beforeHash, after } of changes) {
        const file = join(root, path);
        if (digest(read(file)) !== beforeHash) throw new Error(`file changed during retirement: ${path}`);
        if (after === null) unlinkSync(file);
        else writeFileSync(file, after);
        console.log(`patched ${path} ${after === null ? 'removed retired Go WASM file' : 'retired Go WASM wiring'}`);
      }
      removeEmpty(join(root, 'wasm'));
    }
  } catch (error) {
    console.log(`manual ${error.message}`);
    process.exitCode = 1;
  }
}
