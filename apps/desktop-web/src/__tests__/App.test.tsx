import { render, screen, waitFor } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import App from '../App';
import type { DesktopInfo } from '../types';

const mockInfo: DesktopInfo = {
  desktop_id: 'd-test',
  hostname: 'd-test.desktops.orchael.dev',
  github_owner: 'orchael',
  environment: 'dev',
  bridge_port: 9445,
  repos: ['orchael/desktopctl'],
  services: [],
  novnc_url: 'https://d-test.desktops.orchael.dev/novnc',
  desktop_web_version: '0.2.4'
};

describe('App', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn());
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('renders the header', async () => {
    vi.mocked(fetch).mockResolvedValueOnce({
      ok: true,
      json: async () => mockInfo
    } as Response);
    render(<App />);
    expect(screen.getByText('AI Desktop')).toBeInTheDocument();
    expect(
      screen.getByText('Local desktop status dashboard')
    ).toBeInTheDocument();
  });

  it('shows loading state while fetching', () => {
    vi.mocked(fetch).mockReturnValueOnce(new Promise(() => {}));
    render(<App />);
    expect(screen.getByText('Loading desktop info…')).toBeInTheDocument();
  });

  it('renders desktop info after successful fetch', async () => {
    vi.mocked(fetch).mockResolvedValueOnce({
      ok: true,
      json: async () => mockInfo
    } as Response);
    render(<App />);
    await waitFor(() => {
      expect(screen.getByText('d-test')).toBeInTheDocument();
    });
  });

  it('shows error banner when fetch fails', async () => {
    vi.mocked(fetch).mockRejectedValueOnce(new Error('Network error'));
    render(<App />);
    await waitFor(() => {
      expect(screen.getByText(/network error/i)).toBeInTheDocument();
    });
  });
});
