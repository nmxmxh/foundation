#!/usr/bin/env node

import fs from 'node:fs'

const [, , templatePath, targetPath] = process.argv

if (!templatePath || !targetPath) {
  console.error('usage: frontend_manifest_sync.mjs <template-package.json> <target-package.json>')
  process.exit(1)
}

const readJSON = (filePath) => JSON.parse(fs.readFileSync(filePath, 'utf8'))

const writeJSON = (filePath, value) => {
  fs.writeFileSync(filePath, `${JSON.stringify(value, null, 2)}\n`)
}

const target = readJSON(targetPath)
const template = readJSON(templatePath)

const requiredScripts = ['preview', 'test', 'test:watch']
const requiredDependencies = [
  '@ovasabi/runtime-transport',
  '@ovasabi/frontend-kit',
  '@ovasabi/ui-minimal',
  // ui-minimal's styles are Linaria (Foundation research doc 14.8).
  '@linaria/core',
  '@linaria/react',
  'react-router-dom',
  'zustand',
]
const requiredNativeDependencies = [
  '@ovasabi/runtime-native',
]
const requiredDevDependencies = [
  '@testing-library/jest-dom',
  '@testing-library/react',
  '@testing-library/user-event',
  // Build-time extraction for ui-minimal's Linaria styles.
  '@wyw-in-js/vite',
  '@babel/preset-typescript',
  '@babel/preset-react',
  'jsdom',
  'ts-proto',
  'vitest',
]
const pinnedDependencyVersions = new Set([
  '@ovasabi/runtime-transport',
  '@ovasabi/frontend-kit',
  '@ovasabi/ui-minimal',
  '@ovasabi/runtime-native',
])
// vitest is pinned to the template range because an app left on an older major
// makes npm resolve vitest@latest through optional peers (@vitejs/devtools-vitest
// peers vitest@*), and the mixed-major peer set crashes arborist (npm 11:
// "Cannot read properties of null (reading 'edgesOut')").
const pinnedDevDependencyVersions = new Set(['ts-proto', 'vitest'])
// Plugins that peer on an exact vitest version: never added, but when present
// they must move in lockstep with the pinned vitest range.
const lockstepDevDependencies = ['@vitest/coverage-v8']

let changed = false

target.scripts ??= {}
target.dependencies ??= {}
target.devDependencies ??= {}

for (const key of requiredScripts) {
  const value = template.scripts?.[key]
  if (value && target.scripts[key] !== value) {
    target.scripts[key] = value
    changed = true
  }
}

for (const key of requiredDependencies) {
  const value = template.dependencies?.[key]
  if (value && (!target.dependencies[key] || (pinnedDependencyVersions.has(key) && target.dependencies[key] !== value))) {
    target.dependencies[key] = value
    changed = true
  }
}

if (process.env.WITH_NATIVE === 'true') {
  for (const key of requiredNativeDependencies) {
    const value = key === '@ovasabi/runtime-native'
      ? 'file:../foundation/runtime-native/ts'
      : template.dependencies?.[key]
    if (value && (!target.dependencies[key] || (pinnedDependencyVersions.has(key) && target.dependencies[key] !== value))) {
      target.dependencies[key] = value
      changed = true
    }
  }
}

for (const key of requiredDevDependencies) {
  const value = template.devDependencies?.[key]
  if (value && (!target.devDependencies[key] || (pinnedDevDependencyVersions.has(key) && target.devDependencies[key] !== value))) {
    target.devDependencies[key] = value
    changed = true
  }
}

for (const key of lockstepDevDependencies) {
  const value = template.devDependencies?.[key]
  if (value && target.devDependencies[key] && target.devDependencies[key] !== value) {
    target.devDependencies[key] = value
    changed = true
  }
}

if (changed) {
  writeJSON(targetPath, target)
}
