import { execSync } from 'child_process';
import fs from 'fs';
import os from 'os';
import path from 'path';

const ENV_FILE = process.env.DESKTOP_ENV_FILE ?? '/opt/ai-desktops/desktop.env';
const NOVNC_HTTPS_PORT = process.env.NOVNC_HTTPS_PORT ?? '8443';

const MOCK_DATA = {
  desktop_id: 'mock-desktop',
  hostname: 'localhost',
  github_owner: 'mock-owner',
  environment: 'dev',
  bridge_port: 9445,
  workspace: '/workspace',
  repos: ['mock-repo'],
  services: [
    { name: 'docker', active: true },
    { name: 'ai-agent-bridge', active: true },
    { name: 'novnc-desktop', active: true }
  ],
  novnc_url: 'https://localhost:8443/novnc/vnc.html'
};

function readEnvFile(filePath: string): Record<string, string> {
  const env: Record<string, string> = {};
  try {
    const content = fs.readFileSync(filePath, 'utf8');
    for (const line of content.split('\n')) {
      const trimmed = line.trim();
      if (!trimmed || trimmed.startsWith('#')) continue;
      const idx = trimmed.indexOf('=');
      if (idx === -1) continue;
      const key = trimmed.slice(0, idx).trim();
      const value = trimmed
        .slice(idx + 1)
        .trim()
        .replace(/^"(.*)"$/, '$1');
      env[key] = value;
    }
  } catch {
    // file may not exist outside AMI
  }
  return env;
}

function serviceActive(name: string): boolean {
  try {
    execSync(`systemctl is-active ${name}`, { stdio: 'pipe', timeout: 5000 });
    return true;
  } catch {
    return false;
  }
}

function scanRepos(workspace: string): string[] {
  if (!workspace) return [];
  try {
    if (!fs.statSync(workspace).isDirectory()) return [];
    return fs
      .readdirSync(workspace)
      .filter((entry) => {
        try {
          return fs.statSync(path.join(workspace, entry, '.git')).isDirectory();
        } catch {
          return false;
        }
      })
      .sort();
  } catch {
    return [];
  }
}

export function buildDesktopInfo(requestHost?: string): object {
  if (process.env.MOCK === 'true') return MOCK_DATA;

  const env = readEnvFile(ENV_FILE);

  const desktopId = env['DESKTOP_ID'] ?? 'unknown';
  const githubOwner = env['GITHUB_OWNER'] ?? '';
  const workspace = env['WORKSPACE'] ?? '/workspace';
  const environment = env['ENVIRONMENT'] ?? 'dev';
  const bridgePort = parseInt(env['BRIDGE_PORT'] ?? '9445', 10);

  const rawHost = requestHost ?? process.env.HOSTNAME ?? os.hostname();
  const hostname = rawHost.split(':')[0];
  const novncUrl = `https://${hostname}:${NOVNC_HTTPS_PORT}/novnc/vnc.html`;

  const services = [
    { name: 'docker', active: serviceActive('docker') },
    { name: 'ai-agent-bridge', active: serviceActive('ai-agent-bridge') },
    { name: 'novnc-desktop', active: serviceActive('novnc-desktop') }
  ];

  return {
    desktop_id: desktopId,
    hostname,
    github_owner: githubOwner,
    environment,
    bridge_port: bridgePort,
    workspace,
    repos: scanRepos(workspace),
    services,
    novnc_url: novncUrl
  };
}
