import { execSync } from 'child_process';
import fs from 'fs';
import os from 'os';
import path from 'path';
import { fileURLToPath } from 'url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

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
    { name: 'docker', active: true, version: '27.0.0' },
    { name: 'bridgectl', active: true, version: '1.0.1' },
    { name: 'novnc-desktop', active: true, version: '20260525-005909' }
  ],
  novnc_url: 'https://localhost:8443/novnc/vnc.html',
  desktop_web_version: '0.0.0-mock'
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
    if (name === 'bridgectl') {
      // bridgectl runs as a user-level systemd unit under the ubuntu user.
      // XDG_RUNTIME_DIR must be set explicitly because ai-desktops-web runs as
      // a system service (not a user session), so the env var is absent.
      execSync('systemctl --user is-active bridgectl', {
        stdio: 'pipe',
        timeout: 5000,
        env: { ...process.env, XDG_RUNTIME_DIR: '/run/user/1000' }
      });
    } else {
      execSync(`systemctl is-active ${name}`, { stdio: 'pipe', timeout: 5000 });
    }
    return true;
  } catch {
    return false;
  }
}

const NOVNC_VERSION_FILE =
  process.env.NOVNC_VERSION_FILE ?? '/opt/ai-desktops/novnc-desktop-version';

function serviceVersion(name: string): string | undefined {
  try {
    switch (name) {
      case 'docker': {
        const out = execSync('docker --version', {
          stdio: 'pipe',
          timeout: 5000
        }).toString();
        // "Docker version 27.3.1, build ce12230"
        const m = out.match(/Docker version ([^\s,]+)/);
        return m?.[1];
      }
      case 'bridgectl': {
        const out = execSync('bridgectl --version', {
          stdio: 'pipe',
          timeout: 5000
        }).toString();
        const m = out.match(/(\d+\.\d+\.\d+[^\s]*)/);
        return m?.[1];
      }
      case 'novnc-desktop': {
        const v = fs.readFileSync(NOVNC_VERSION_FILE, 'utf8').trim();
        return v || undefined;
      }
      default:
        return undefined;
    }
  } catch {
    return undefined;
  }
}

function desktopWebVersion(): string {
  try {
    const pkgPath = path.join(__dirname, '..', 'package.json');
    const pkg = JSON.parse(fs.readFileSync(pkgPath, 'utf8')) as {
      version: string;
    };
    return pkg.version ?? 'unknown';
  } catch {
    return 'unknown';
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

  const serviceNames = ['docker', 'bridgectl', 'novnc-desktop'];
  const services = serviceNames.map((name) => ({
    name,
    active: serviceActive(name),
    version: serviceVersion(name)
  }));

  return {
    desktop_id: desktopId,
    hostname,
    github_owner: githubOwner,
    environment,
    bridge_port: bridgePort,
    workspace,
    repos: scanRepos(workspace),
    services,
    novnc_url: novncUrl,
    desktop_web_version: desktopWebVersion()
  };
}
