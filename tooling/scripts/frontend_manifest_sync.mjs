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
  'framer-motion',
  'react-router-dom',
  'styled-components',
  'zustand',
]
const requiredDevDependencies = [
  '@testing-library/jest-dom',
  '@testing-library/react',
  '@testing-library/user-event',
  'jsdom',
  'ts-proto',
  'vitest',
]
const pinnedDependencyVersions = new Set([
  '@ovasabi/runtime-transport',
  '@ovasabi/frontend-kit',
  '@ovasabi/ui-minimal',
  'framer-motion',
])
const pinnedDevDependencyVersions = new Set(['ts-proto'])

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

for (const key of requiredDevDependencies) {
  const value = template.devDependencies?.[key]
  if (value && (!target.devDependencies[key] || (pinnedDevDependencyVersions.has(key) && target.devDependencies[key] !== value))) {
    target.devDependencies[key] = value
    changed = true
  }
}

if (changed) {
  writeJSON(targetPath, target)
}
