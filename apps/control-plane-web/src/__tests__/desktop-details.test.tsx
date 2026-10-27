import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { DesktopDetails } from '@/components/desktop-details';

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.unstubAllGlobals();
});

it('shows the complete desktop record and opens its noVNC URL', async () => {
  const url = 'https://d-demo001.desktops.example.com:8443/novnc/vnc.html';
  const fetchMock = vi.fn().mockResolvedValue(
    Response.json({
      desktop_id: 'd-demo001',
      lifecycle_state: 'ready',
      github_owner: 'orchael',
      region: 'us-east-2',
      updated_at: '2026-10-27T00:00:00Z',
      novnc_url: url,
      ssh_target: 'ubuntu@d-demo001.desktops.example.com',
      ami_id: 'ami-1234',
      repos: ['orchael/desktopctl']
    })
  );
  vi.stubGlobal('fetch', fetchMock);
  render(<DesktopDetails id="d-demo001" />);

  expect(await screen.findByRole('link', { name: /open desktop/i })).toHaveAttribute('href', url);
  expect(screen.getByText('ubuntu@d-demo001.desktops.example.com')).toBeInTheDocument();
  expect(screen.getByText('ami-1234')).toBeInTheDocument();
  expect(screen.getByText('orchael/desktopctl')).toBeInTheDocument();
  expect(fetchMock).toHaveBeenCalledWith('/api/fleet/desktops/d-demo001', { cache: 'no-store' });
});

it('runs a lifecycle action and refreshes the detail page', async () => {
  let reads = 0;
  const fetchMock = vi.fn().mockImplementation((input: string, options?: RequestInit) => {
    if (input === '/api/fleet/desktops/d-demo001' && options?.method !== 'POST') {
      reads++;
      return Promise.resolve(
        Response.json({
          desktop_id: 'd-demo001',
          lifecycle_state: reads === 1 ? 'ready' : 'stopped',
          github_owner: 'orchael',
          region: 'us-east-2',
          updated_at: '2026-10-27T00:00:00Z'
        })
      );
    }
    if (input.endsWith('/stop')) {
      return Promise.resolve(
        Response.json(
          { id: 'job-1', action: 'stop', desktop_id: 'd-demo001', state: 'queued' },
          { status: 202 }
        )
      );
    }
    return Promise.resolve(
      Response.json({ id: 'job-1', action: 'stop', desktop_id: 'd-demo001', state: 'succeeded' })
    );
  });
  vi.stubGlobal('fetch', fetchMock);
  render(<DesktopDetails id="d-demo001" />);
  fireEvent.click(await screen.findByRole('button', { name: 'Stop' }));
  await waitFor(() =>
    expect(document.querySelector('.detailPageHead p')).toHaveTextContent('d-demo001 · stopped')
  );
  expect(fetchMock).toHaveBeenCalledWith(
    '/api/fleet/desktops/d-demo001/stop',
    expect.objectContaining({ method: 'POST' })
  );
  expect(localStorage.getItem('desktopctl-companion-job')).toBeNull();
});

it('shows a fetch error and does not open an unsafe browser URL', async () => {
  const fetchMock = vi
    .fn()
    .mockResolvedValueOnce(Response.json({ error: 'Desktop not found' }, { status: 404 }));
  vi.stubGlobal('fetch', fetchMock);
  render(<DesktopDetails id="missing" />);
  expect(await screen.findByRole('alert')).toHaveTextContent('Desktop not found');
  expect(screen.queryByRole('link', { name: /open desktop/i })).not.toBeInTheDocument();
});
