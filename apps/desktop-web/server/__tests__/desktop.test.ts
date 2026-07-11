import { describe, it, expect, vi, beforeEach } from 'vitest';

// Reset module registry before each test so env var changes take effect.
beforeEach(() => {
  vi.resetModules();
  delete process.env.MOCK;
  delete process.env.DESKTOP_ENV_FILE;
  delete process.env.NOVNC_HTTPS_PORT;
});

describe('buildDesktopInfo', () => {
  it('returns mock data when MOCK=true', async () => {
    process.env.MOCK = 'true';
    const { buildDesktopInfo } = await import('../desktop.js');
    const info = buildDesktopInfo() as Record<string, unknown>;
    expect(info.desktop_id).toBe('mock-desktop');
    expect(info.hostname).toBe('localhost');
    expect(Array.isArray(info.services)).toBe(true);
  });

  it('mock data includes novnc-desktop service with version', async () => {
    process.env.MOCK = 'true';
    const { buildDesktopInfo } = await import('../desktop.js');
    const info = buildDesktopInfo() as Record<string, unknown>;
    const services = info.services as Array<Record<string, unknown>>;
    const novnc = services.find((s) => s.name === 'novnc-desktop');
    expect(novnc).toBeDefined();
    expect(novnc?.version).toMatch(/^\d{8}-\d{6}$/);
  });

  it('returns desktop info with correct shape when MOCK=true', async () => {
    process.env.MOCK = 'true';
    const { buildDesktopInfo } = await import('../desktop.js');
    const info = buildDesktopInfo() as Record<string, unknown>;
    expect(info).toHaveProperty('desktop_id');
    expect(info).toHaveProperty('hostname');
    expect(info).toHaveProperty('github_owner');
    expect(info).toHaveProperty('environment');
    expect(info).toHaveProperty('bridge_port');
    expect(info).toHaveProperty('workspace');
    expect(info).toHaveProperty('repos');
    expect(info).toHaveProperty('services');
    expect(info).toHaveProperty('novnc_url');
  });

  it('strips port suffix from requestHost', async () => {
    process.env.DESKTOP_ENV_FILE = '/tmp/nonexistent-desktop.env';
    const { buildDesktopInfo } = await import('../desktop.js');
    const info = buildDesktopInfo('example.com:5173') as Record<
      string,
      unknown
    >;
    expect(info.hostname).toBe('example.com');
  });
});

describe('buildDesktopInfo real mode', () => {
  it('uses a non-existent env file gracefully', async () => {
    process.env.DESKTOP_ENV_FILE = '/tmp/nonexistent-desktop.env';
    const { buildDesktopInfo } = await import('../desktop.js');
    const info = buildDesktopInfo('testhost.example.com') as Record<
      string,
      unknown
    >;
    expect(info.desktop_id).toBe('unknown');
    expect(info.hostname).toBe('testhost.example.com');
    expect(info.github_owner).toBe('');
  });

  it('parses a real env file', async () => {
    const tmpFile = `/tmp/test-desktop-${Date.now()}.env`;
    const { writeFileSync, unlinkSync } = await import('fs');
    writeFileSync(
      tmpFile,
      [
        'DESKTOP_ID="test-id-123"',
        'GITHUB_OWNER="testorg"',
        'WORKSPACE="/tmp/workspace"',
        'BRIDGE_PORT="9999"',
        'ENVIRONMENT="prod"'
      ].join('\n')
    );
    process.env.DESKTOP_ENV_FILE = tmpFile;
    try {
      const { buildDesktopInfo } = await import('../desktop.js');
      const info = buildDesktopInfo('myhost.com') as Record<string, unknown>;
      expect(info.desktop_id).toBe('test-id-123');
      expect(info.github_owner).toBe('testorg');
      expect(info.bridge_port).toBe(9999);
      expect(info.environment).toBe('prod');
      expect(info.novnc_url).toBe('https://myhost.com:8443/');
    } finally {
      unlinkSync(tmpFile);
    }
  });
});
