import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { resolve } from 'path';

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': resolve(import.meta.dirname, 'src')
    }
  },
  build: {
    outDir: 'dist',
    sourcemap: true
  },
  server: {
    host: '127.0.0.1',
    port: 5174,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true
      },
      '/readyz': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true
      }
    }
  }
});
