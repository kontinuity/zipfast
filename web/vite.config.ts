import react from '@vitejs/plugin-react';
import path from 'path';
import { defineConfig } from 'vite';

// Zipfast client build.
//
// Produces a single static SPA in ../dist (i.e. web/dist), which the Go server
// serves from web/dist (or you host it on a CDN). Unlike upstream Zipline we do
// NOT build the SSR entries here: OpenGraph/embed meta for /view and /view/url is
// rendered server-side in Go (internal/server/embed.go), so the SPA only needs
// the single client entry.
export default defineConfig({
  plugins: [react()],
  root: './src/client',
  build: {
    outDir: '../../dist',
    emptyOutDir: true,
    target: 'esnext',
    rollupOptions: {
      input: path.resolve(__dirname, 'src/client/index.html'),
    },
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    port: 5173,
    proxy: {
      // Dev convenience: proxy API + file-serving routes to the Go server on :3000.
      '/api': 'http://localhost:3000',
      '/raw': 'http://localhost:3000',
      '/u': 'http://localhost:3000',
      '/go': 'http://localhost:3000',
      '/view': 'http://localhost:3000',
    },
  },
});
