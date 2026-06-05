import { render, screen } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import DesktopStatus from '../components/DesktopStatus';
import type { DesktopInfo } from '../types';

const baseInfo: DesktopInfo = {
  desktop_id: 'd-001',
  hostname: 'd-001.desktops.orchael.dev',
  github_owner: 'orchael',
  environment: 'dev',
  bridge_port: 9445,
  repos: ['orchael/ai-desktops'],
  services: [
    { name: 'docker', active: true },
    { name: 'novnc', active: false }
  ],
  novnc_url: ''
};

describe('DesktopStatus', () => {
  it('renders desktop identity fields', () => {
    render(<DesktopStatus info={baseInfo} />);
    expect(screen.getByText('d-001')).toBeInTheDocument();
    expect(screen.getByText('d-001.desktops.orchael.dev')).toBeInTheDocument();
    expect(screen.getByText('orchael')).toBeInTheDocument();
    expect(screen.getByText('dev')).toBeInTheDocument();
  });

  it('renders repository tags', () => {
    render(<DesktopStatus info={baseInfo} />);
    expect(screen.getByText('orchael/ai-desktops')).toBeInTheDocument();
  });

  it('shows "No repositories configured" when repos is empty', () => {
    render(<DesktopStatus info={{ ...baseInfo, repos: [] }} />);
    expect(screen.getByText('No repositories configured.')).toBeInTheDocument();
  });

  it('renders services with correct active/inactive status', () => {
    render(<DesktopStatus info={baseInfo} />);
    expect(screen.getByText('docker')).toBeInTheDocument();
    expect(screen.getByText('novnc')).toBeInTheDocument();
    const activeLabels = screen.getAllByText('active');
    const inactiveLabels = screen.getAllByText('inactive');
    expect(activeLabels).toHaveLength(1);
    expect(inactiveLabels).toHaveLength(1);
  });
});
