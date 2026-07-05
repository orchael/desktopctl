import { describe, it, expect } from 'vitest';
import type { DesktopInfo, ServiceStatus } from '../types';

describe('DesktopInfo', () => {
  it('accepts a valid desktop info object', () => {
    const info: DesktopInfo = {
      desktop_id: 'd-001',
      hostname: 'd-001.desktops.orchael.dev',
      github_owner: 'orchael',
      environment: 'dev',
      bridge_port: 9445,
      repos: ['orchael/ai-desktops'],
      services: [],
      novnc_url: 'https://d-001.desktops.orchael.dev/novnc',
      desktop_web_version: '0.2.4'
    };
    expect(info.desktop_id).toBe('d-001');
    expect(info.hostname).toBe('d-001.desktops.orchael.dev');
    expect(info.github_owner).toBe('orchael');
    expect(info.environment).toBe('dev');
    expect(info.bridge_port).toBe(9445);
    expect(info.repos).toHaveLength(1);
    expect(info.repos[0]).toBe('orchael/ai-desktops');
    expect(info.services).toHaveLength(0);
    expect(info.novnc_url).toBe('https://d-001.desktops.orchael.dev/novnc');
  });

  it('handles multiple repos', () => {
    const info: DesktopInfo = {
      desktop_id: 'd-002',
      hostname: 'd-002.desktops.orchael.dev',
      github_owner: 'orchael',
      environment: 'prod',
      bridge_port: 9445,
      repos: ['orchael/app', 'orchael/shared-lib', 'orchael/infra'],
      services: [],
      novnc_url: 'https://d-002.desktops.orchael.com/novnc',
      desktop_web_version: '0.2.4'
    };
    expect(info.repos).toHaveLength(3);
  });
});

describe('ServiceStatus', () => {
  it('accepts an active service', () => {
    const service: ServiceStatus = { name: 'docker', active: true };
    expect(service.name).toBe('docker');
    expect(service.active).toBe(true);
  });

  it('accepts an inactive service', () => {
    const service: ServiceStatus = { name: 'novnc', active: false };
    expect(service.active).toBe(false);
  });
});
