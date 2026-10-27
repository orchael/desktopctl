import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { Dashboard } from '@/components/dashboard';

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.unstubAllGlobals();
});

it('runs a lifecycle action from the selected desktop card', async () => {
  let stopped = false;
  const fetchMock = vi.fn().mockImplementation((input: string, options?: RequestInit) => {
    if (input === '/api/fleet/desktops' && options?.method !== 'POST') {
      return Promise.resolve(
        Response.json([
          {
            desktop_id: 'd-first',
            desktop_name: 'First',
            lifecycle_state: stopped ? 'stopped' : 'ready',
            github_owner: 'orchael',
            region: 'us-east-2',
            updated_at: '2026-10-27T00:00:00Z'
          }
        ])
      );
    }
    if (input.endsWith('/stop')) {
      stopped = true;
      return Promise.resolve(
        Response.json(
          { id: 'job-1', action: 'stop', desktop_id: 'd-first', state: 'queued' },
          { status: 202 }
        )
      );
    }
    return Promise.resolve(
      Response.json({ id: 'job-1', action: 'stop', desktop_id: 'd-first', state: 'succeeded' })
    );
  });
  vi.stubGlobal('fetch', fetchMock);
  render(<Dashboard />);
  const card = (await screen.findByText('First')).closest('.detail')!;
  fireEvent.click(within(card).getByRole('button', { name: 'Stop' }));
  await waitFor(() => expect(within(card).getByText('stopped')).toBeInTheDocument());
  expect(fetchMock).toHaveBeenCalledWith(
    '/api/fleet/desktops/d-first/stop',
    expect.objectContaining({ method: 'POST' })
  );
  expect(localStorage.getItem('desktopctl-companion-job')).toBeNull();
});

it('selects a fleet row in the summary card and links to the full details page', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn().mockResolvedValue(
      Response.json([
        {
          desktop_id: 'd-first',
          desktop_name: 'First',
          lifecycle_state: 'ready',
          github_owner: 'orchael',
          region: 'us-east-2',
          updated_at: '2026-10-27T00:00:00Z',
          novnc_url: 'https://first.example.com:8443/novnc/vnc.html'
        },
        {
          desktop_id: 'd-second',
          desktop_name: 'Second',
          lifecycle_state: 'stopped',
          github_owner: 'orchael',
          region: 'us-east-2',
          updated_at: '2026-10-27T00:00:00Z',
          novnc_url: 'https://second.example.com:8443/novnc/vnc.html'
        }
      ])
    )
  );
  render(<Dashboard />);
  const card = await screen.findByText('First');
  expect(card.closest('.detail')).toBeInTheDocument();
  fireEvent.click(screen.getByText('d-second ↗').closest('tr')!);
  expect(screen.getByText('Second').closest('.detail')).toBeInTheDocument();
  const selectedCard = screen.getByText('Second').closest('.detail')!;
  expect(
    within(selectedCard).getByRole('link', { name: 'View all details for d-second' })
  ).toHaveAttribute('href', '/desktops/d-second');
  expect(within(selectedCard).getByRole('link', { name: /open desktop/i })).toHaveAttribute(
    'href',
    '/api/fleet/desktops/d-second/access'
  );
});
