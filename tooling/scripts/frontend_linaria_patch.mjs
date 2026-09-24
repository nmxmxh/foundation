#!/usr/bin/env node
/** Upgrade Linaria configuration without executing project configuration code. */
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { dirname, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = process.argv[2];
if (!root || !existsSync(root)) process.exit(0);
const foundation = join(dirname(fileURLToPath(import.meta.url)), '../..');
const require = createRequire(import.meta.url);
const report = (kind, file, message) => console.log(`${kind} ${relative(join(root, '..'), file)} ${message}`);
let ts;
try {
  ts = require(require.resolve('typescript', { paths: [root, join(foundation, 'frontend-kit/ts'), join(foundation, 'frontend-lab')] }));
} catch {
  report('manual', root, 'install frontend TypeScript dependencies, then repeat the Linaria patch');
  process.exit(0);
}

const plugin = String.raw`wyw({ include: [/ui-minimal[\\/](ts[\\/])?src[\\/].*\.[jt]sx?(?:\?.*)?$/, /[\\/]src[\\/].*\.[jt]sx?(?:\?.*)?$/], transformLibraries: true, prefixer: false })`;
const property = (object, name) => {
  const matches = object.properties.filter((entry) => entry.name?.text === name);
  if (matches.length > 1 || (matches.length === 1 && !ts.isPropertyAssignment(matches[0]))) {
    throw new Error(`${name} requires one explicit property assignment`);
  }
  return matches[0];
};
const unwrap = (node) => {
  for (let i = 0; node && i < 20; i += 1) {
    if (ts.isParenthesizedExpression(node) || ts.isAsExpression(node) || ts.isSatisfiesExpression(node)) node = node.expression;
    else return node;
  }
  throw new Error('configuration nesting exceeds the supported bound');
};
const literalObject = (node, name) => {
  node = unwrap(node);
  if (!node || !ts.isObjectLiteralExpression(node) || node.properties.some((entry) =>
    ts.isSpreadAssignment(entry) || (entry.name && ts.isComputedPropertyName(entry.name)))) {
    throw new Error(`${name} requires a literal object without spreads or computed properties`);
  }
  return node;
};
const parse = (file, code) => {
  const source = ts.createSourceFile(file, code, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  if (source.parseDiagnostics.length) throw new Error('configuration contains syntax errors');
  return source;
};

function updateOptimizer(config, source, edits) {
  const existing = property(config, 'optimizeDeps');
  if (!existing) {
    edits.push({ start: config.getStart(source) + 1, end: config.getStart(source) + 1,
      text: "\n  optimizeDeps: { exclude: ['@ovasabi/ui-minimal'] }," });
    return;
  }
  const options = literalObject(existing.initializer, 'optimizeDeps');
  const included = property(options, 'include')?.initializer;
  if (included && (!ts.isArrayLiteralExpression(included) || included.elements.some((entry) =>
    !ts.isStringLiteral(entry) || entry.text === '@ovasabi/ui-minimal' || entry.text.startsWith('@ovasabi/ui-minimal/')))) {
    throw new Error('review optimizeDeps.include before excluding ui-minimal');
  }
  const excluded = property(options, 'exclude')?.initializer;
  if (!excluded) {
    edits.push({ start: options.getStart(source) + 1, end: options.getStart(source) + 1,
      text: " exclude: ['@ovasabi/ui-minimal']," });
  } else if (!ts.isArrayLiteralExpression(excluded) || excluded.elements.some((entry) => !ts.isStringLiteral(entry))) {
    throw new Error('optimizeDeps.exclude requires a literal string array');
  } else if (!excluded.elements.some((entry) => entry.text === '@ovasabi/ui-minimal')) {
    edits.push({ start: excluded.getStart(source) + 1, end: excluded.getStart(source) + 1,
      text: "'@ovasabi/ui-minimal', " });
  }
}

function updateFilters(options, source, edits) {
  const include = property(options, 'include')?.initializer;
  if (!include || !ts.isArrayLiteralExpression(include)) throw new Error('Linaria include requires an explicit filter array');
  const filters = [];
  for (const entry of include.elements) {
    if (entry.kind !== ts.SyntaxKind.RegularExpressionLiteral) throw new Error('review custom Linaria glob filters');
    const before = entry.getText(source);
    const after = before.replace(String.raw`\.[jt]sx?$/`, String.raw`\.[jt]sx?(?:\?.*)?$/`);
    if (after !== before) edits.push({ start: entry.getStart(source), end: entry.end, text: after });
    const delimiter = after.lastIndexOf('/');
    filters.push(new RegExp(after.slice(1, delimiter), after.slice(delimiter + 1)));
  }
  for (const id of ['/project/src/App.tsx?v=123', '/project/node_modules/@ovasabi/ui-minimal/src/primitives.tsx?v=123']) {
    if (!filters.some((filter) => { filter.lastIndex = 0; return filter.test(id); })) {
      throw new Error('Linaria filters must cover application and ui-minimal sources with Vite query strings');
    }
  }
  const transform = property(options, 'transformLibraries');
  if (!transform) edits.push({ start: options.getStart(source) + 1, end: options.getStart(source) + 1, text: ' transformLibraries: true,' });
  else if (transform.initializer.kind !== ts.SyntaxKind.TrueKeyword) throw new Error('review disabled Linaria library transforms');
}

function patchConfig(file, original) {
  let code = original;
  if (!code.includes('@wyw-in-js/vite')) {
    const reactImport = /^import react from ['"]@vitejs\/plugin-react['"];?\s*$/m;
    const plugins = /plugins:\s*\[\s*react\(\)(\s+as\s+never)?/;
    if (!reactImport.test(code) || !plugins.test(code)) throw new Error('add the Linaria plugin to the custom configuration');
    code = code.replace(reactImport, (line) => `${line.trimEnd()}\nimport wyw from '@wyw-in-js/vite';`);
    code = code.replace(plugins, (match, cast = '') => `${match}, ${plugin}${cast}`);
  }
  const source = parse(file, code);
  const imported = source.statements.find((node) => ts.isImportDeclaration(node) && node.moduleSpecifier.text === '@wyw-in-js/vite');
  const name = imported?.importClause?.name?.text;
  const exported = source.statements.find(ts.isExportAssignment);
  const call = unwrap(exported?.expression);
  if (!call || !ts.isCallExpression(call)) throw new Error('expected an exported defineConfig call');
  let argument = unwrap(call.arguments[0]);
  if (argument && ts.isArrowFunction(argument)) argument = unwrap(argument.body);
  const config = literalObject(argument, 'Vite configuration');
  const plugins = property(config, 'plugins')?.initializer;
  if (!plugins || !ts.isArrayLiteralExpression(plugins)) throw new Error('plugins requires a literal array');
  const calls = plugins.elements.map(unwrap).filter((node) => ts.isCallExpression(node) && node.expression.getText(source) === name);
  if (calls.length !== 1) throw new Error('expected one Linaria plugin instance');
  const edits = [];
  updateFilters(literalObject(calls[0].arguments[0], 'Linaria options'), source, edits);
  updateOptimizer(config, source, edits);
  for (const edit of edits.sort((a, b) => b.start - a.start)) code = code.slice(0, edit.start) + edit.text + code.slice(edit.end);
  parse(file, code);
  return code;
}

for (const name of ['vite.config.ts', 'vitest.config.ts']) {
  const file = join(root, name);
  if (!existsSync(file)) continue;
  try {
    const original = readFileSync(file, 'utf8');
    const code = patchConfig(file, original);
    if (code === original) continue;
    writeFileSync(file, code);
    report('patched', file, 'keeps Linaria transforms active during development and dependency loading');
  } catch (error) {
    report('manual', file, error.message);
  }
}
