import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  use: {
    baseURL: 'http://127.0.0.1:3001',
    headless: true
  },
  webServer: {
    command: 'node server-dist/index.js',
    url: 'http://127.0.0.1:3001',
    timeout: 15_000,
    reuseExistingServer: false,
    env: {
      MOCK: 'true',
      PORT: '3001'
    }
  }
});
