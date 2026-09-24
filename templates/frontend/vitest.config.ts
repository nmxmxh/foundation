import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import wyw from '@wyw-in-js/vite'
import path from 'path'

const serial = process.env.FOUNDATION_VITEST_SERIAL !== '0'
const maxWorkers = Number.parseInt(process.env.FOUNDATION_VITEST_WORKERS ?? '0', 10)

export default defineConfig({
  plugins: [
    react() as never,
    wyw({
      // Vite adds query strings during development. Transform application and linked kit sources.
      include: [/ui-minimal[\\/](ts[\\/])?src[\\/].*\.[jt]sx?(?:\?.*)?$/, /[\\/]src[\\/].*\.[jt]sx?(?:\?.*)?$/],
      transformLibraries: true,
      prefixer: false,
    }) as never,
  ],
  optimizeDeps: { exclude: ['@ovasabi/ui-minimal'] },
  resolve: {
    preserveSymlinks: true,
    alias: {
      '@': path.resolve(__dirname, './src'),
      '@generated': path.resolve(__dirname, './src/types/protos'),
      react: path.resolve(__dirname, './node_modules/react'),
      'react-dom': path.resolve(__dirname, './node_modules/react-dom'),
      zustand: path.resolve(__dirname, './node_modules/zustand'),
    },
    dedupe: ['react', 'react-dom', '@linaria/react'],
  },
  test: {
    globals: true,
    environment: 'jsdom',
    fileParallelism: !serial,
    ...(Number.isFinite(maxWorkers) && maxWorkers > 0 ? { maxWorkers } : {}),
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.{test,spec}.{js,mjs,cjs,ts,mts,cts,jsx,tsx}'],
    coverage: {
      reporter: ['text', 'json', 'html'],
      include: ['src/**/*.{ts,tsx}'],
      exclude: [
        'src/**/*.test.{ts,tsx}',
        'src/**/*.spec.{ts,tsx}',
        'src/test/**',
        'src/main.tsx',
        'src/vite-env.d.ts',
      ],
    },
  },
})
