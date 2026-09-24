import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import wyw from '@wyw-in-js/vite'
import { prerenderShell } from '@ovasabi/frontend-kit/vite'
import path from 'path'

// https://vite.dev/config/
export default defineConfig({
  plugins: [
    react(),
    wyw({
      // Vite adds query strings during development. Transform application and linked kit sources.
      include: [/ui-minimal[\\/](ts[\\/])?src[\\/].*\.[jt]sx?(?:\?.*)?$/, /[\\/]src[\\/].*\.[jt]sx?(?:\?.*)?$/],
      transformLibraries: true,
      prefixer: false,
    }),
    // Paint before JavaScript: the built index.html carries the first screen's
    // markup and the CSS that markup can use (criticalCss: 'subset'), so the
    // first paint needs neither script nor a stylesheet round trip; main.tsx
    // hydrates it. Keep long lists to their first screen with useFirstScreen
    // (@ovasabi/frontend-kit). Add a route for every page whose first screen
    // renders without signed-in data. Lab: FCP 1,198 → 585 ms on a mid phone
    // (Foundation research doc §15.5).
    prerenderShell({ entry: 'src/entry-server.tsx', routes: ['/'], criticalCss: 'subset' }),
  ],
  // The dependency optimizer cannot extract Linaria styles. Keep kit sources in the plugin pipeline.
  optimizeDeps: { exclude: ['@ovasabi/ui-minimal'] },
  resolve: {
    preserveSymlinks: true,
    alias: {
      '@': path.resolve(__dirname, './src'),
      '@generated': path.resolve(__dirname, './src/types/protos'),
      react: path.resolve(__dirname, './node_modules/react'),
      'react-dom': path.resolve(__dirname, './node_modules/react-dom'),
    },
    dedupe: ['react', 'react-dom', '@linaria/react', 'zustand'],
  },
  server: {
    port: 5173,
    headers: {
      'Cross-Origin-Opener-Policy': 'same-origin',
      'Cross-Origin-Embedder-Policy': 'require-corp',
      'Cross-Origin-Resource-Policy': 'same-origin',
      'Origin-Agent-Cluster': '?1',
    },
    proxy: {
      // Backend origin the dev server proxies to. Override with VITE_PROXY_TARGET
      // (e.g. an alternate local port) without editing this file.
      '/api': {
        target: process.env.VITE_PROXY_TARGET || 'http://localhost:8080',
        changeOrigin: true,
      },
      '/v1': {
        target: process.env.VITE_PROXY_TARGET || 'http://localhost:8080',
        changeOrigin: true,
        // Projection delta streams upgrade to WebSocket on /v1/projections/…
        ws: true,
      },
      '/ws': {
        target: (process.env.VITE_PROXY_TARGET || 'http://localhost:8080').replace(/^http/, 'ws'),
        ws: true,
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: true,
    rollupOptions: {
      output: {
        manualChunks: {
          vendor: ['react', 'react-dom', 'react-router-dom'],
          ui: ['@linaria/react'],
        },
      },
    },
  },
})
