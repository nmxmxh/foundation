#!/usr/bin/env node
/** Upgrade the known Foundation WASM target while preserving project targets. */
import { createHash } from 'node:crypto';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = process.argv[2];
if (!root) process.exit(0);
const file = join(root, 'Makefile');
if (!existsSync(file)) process.exit(0);
const foundation = join(dirname(fileURLToPath(import.meta.url)), '../..');
const code = readFileSync(file, 'utf8');
const target = /^build-rust-wasm:[\s\S]*?(?=^[\w-]+:|(?![\s\S]))/m;
const current = code.match(target)?.[0];
const desired = readFileSync(join(foundation, 'templates/Makefile'), 'utf8').match(target)?.[0];
if (!current || !desired) {
  console.log('manual Makefile missing build-rust-wasm target');
  process.exit(0);
}
if (current === desired) process.exit(0);
const digest = createHash('sha256').update(current).digest('hex');
// This digest identifies the complete Foundation target before browser ABI version 2.
const legacyDigest = '95fdcdd2ac3ca04710ee54e3fcd40a522cd6299fe3e53179073187ec21280e59';
if (digest !== legacyDigest) {
  console.log('manual Makefile custom build-rust-wasm target requires review');
  process.exit(0);
}
writeFileSync(file, code.replace(current, () => desired));
console.log('patched Makefile builds scalar and shared browser WASM artifacts by default');
