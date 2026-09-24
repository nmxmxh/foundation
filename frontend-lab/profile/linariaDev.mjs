import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import wyw from '@wyw-in-js/vite';
import { chromium } from 'playwright';

const lab = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const viteRoot = process.env.LINARIA_DEV_VITE_ROOT ?? join(lab, 'node_modules/vite');
const { createServer } = await import(pathToFileURL(join(viteRoot, 'dist/node/index.js')).href);
await mkdir(join(lab, '.vitest'), { recursive: true });
const temporary = await mkdtemp(join(lab, '.vitest/linaria-dev-'));
const browser = await chromium.launch({ headless: true });
const results = [];
const source = (color) => `
import React from 'react';
import { createRoot } from 'react-dom/client';
import { styled } from '@linaria/react';
import { MinimalButton } from '@ovasabi/ui-minimal';
import { minimalVars } from '@ovasabi/ui-minimal/tokens';
const Panel = styled.div\`color: ${color}; padding: \${minimalVars.space.md};\`;
const root = import.meta.hot?.data.root ?? createRoot(document.getElementById('root'));
if (import.meta.hot) {
  import.meta.hot.data.root = root;
  import.meta.hot.accept();
}
root.render(
  React.createElement(Panel, {id: 'probe'}, React.createElement(MinimalButton, null, 'Linaria works'))
);
`;

async function runCase(name, querySafe, excludeKit) {
  const root = join(temporary, name);
  await mkdir(join(root, 'node_modules/@ovasabi'), { recursive: true });
  await mkdir(join(root, 'src'));
  await symlink(resolve(lab, '../ui-minimal/ts'), join(root, 'node_modules/@ovasabi/ui-minimal'));
  await writeFile(join(root, 'package.json'), JSON.stringify({ type: 'module' }));
  await writeFile(join(root, 'index.html'), '<div id="root"></div><script type="module" src="/src/main.ts?v=fixture"></script>');
  await writeFile(join(root, 'src/main.ts'), source('rgb(12, 34, 56)'));
  const errors = [];
  const include = querySafe
    ? [/ui-minimal[\\/](ts[\\/])?src[\\/].*\.[jt]sx?(?:\?.*)?$/, /[\\/]src[\\/].*\.[jt]sx?(?:\?.*)?$/]
    : [/ui-minimal[\\/](ts[\\/])?src[\\/].*\.[jt]sx?$/, /[\\/]src[\\/].*\.[jt]sx?$/];
  const server = await createServer({
    root, configFile: false, logLevel: 'error', plugins: [wyw({ include, transformLibraries: true, prefixer: false })],
    resolve: { preserveSymlinks: true, dedupe: ['react', 'react-dom', '@linaria/react'] },
    optimizeDeps: {
      exclude: excludeKit ? ['@ovasabi/ui-minimal'] : [],
      include: ['react', 'react-dom', 'react-dom/client', 'react/jsx-dev-runtime', 'react/jsx-runtime'],
    },
    server: { host: '127.0.0.1', port: 0, fs: { allow: [resolve(lab, '..')] } },
  });
  const page = await browser.newPage();
  page.on('pageerror', (error) => errors.push(error.message));
  try {
    await server.listen();
    await page.goto(server.resolvedUrls.local[0], { waitUntil: 'networkidle', timeout: 30000 });
    if (querySafe && excludeKit) {
      assert.deepEqual(errors, []);
      assert.equal(await page.locator('#probe').evaluate((element) => getComputedStyle(element).color), 'rgb(12, 34, 56)');
      assert.equal(await page.getByRole('button', { name: 'Linaria works' }).count(), 1);
      await page.evaluate(() => { window.__linariaHmrSentinel = 'retained'; });
      await writeFile(join(root, 'src/main.ts'), source('rgb(65, 43, 21)'));
      await page.waitForFunction(() => getComputedStyle(document.querySelector('#probe')).color === 'rgb(65, 43, 21)', undefined, { timeout: 10000 });
      assert.equal(await page.evaluate(() => window.__linariaHmrSentinel), 'retained', 'CSS updates must preserve the loaded page');
      assert.deepEqual(errors, []);
      results.push({ name, result: 'passed', coldRender: true, hmr: true, pageReload: false });
    } else {
      assert.ok(errors.some((message) => message.includes('tag in runtime')),
        `expected the old configuration to fail; observed ${JSON.stringify(errors)}`);
      results.push({ name, result: 'expected failure', errors });
    }
  } finally {
    await page.close();
    await server.close();
  }
}

try {
  await runCase('original', false, false);
  await runCase('exclude-only', false, true);
  await runCase('query-only', true, false);
  await runCase('corrected', true, true);
  console.log(JSON.stringify({ vite: JSON.parse(await readFile(join(viteRoot, 'package.json'))).version, results }, null, 2));
} finally {
  await browser.close();
  await rm(temporary, { recursive: true, force: true });
}
