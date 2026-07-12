import { defineConfig } from 'vitest/config';

const serial = process.env.FOUNDATION_VITEST_SERIAL !== '0';
const maxWorkers = Number.parseInt(process.env.FOUNDATION_VITEST_WORKERS ?? '0', 10);

export default defineConfig({
  test: {
    environment: 'node',
    fileParallelism: !serial,
    ...(Number.isFinite(maxWorkers) && maxWorkers > 0 ? { maxWorkers } : {}),
    include: ['src/**/*.test.ts'],
    coverage: {
      provider: 'v8',
      include: ['src/**/*.ts'],
      exclude: ['src/**/*.test.ts', 'src/**/*.bench.ts', 'src/generated/**'],
      thresholds: {
        lines: 90,
        statements: 90,
        functions: 90,
        branches: 80,
      },
    },
  },
});
