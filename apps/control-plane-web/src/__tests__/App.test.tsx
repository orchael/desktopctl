import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import App from '../App';

const desktops = [
  {
    desktop_id: 'd-test',
    stack_name: 'desktop-d-test',
    github_owner: 'orchael',
    region: 'us-east-2',
    lifecycle_state: 'ready',
    instance_id: 'i-123',
    hostname: 'd-test.desktops.orchael.dev',
    novnc_url: 'https://d-test.desktops.orchael.dev:8443/novnc/vnc.html',
    ssh_target: 'ubuntu@d-test.desktops.orchael.dev',
    readiness: 'ready',
    instance_type: 'm7i.xlarge',
    market_type: 'spot',
    created_at: '2026-08-14T00:00:00Z',
    updated_at: '2026-08-14T00:00:00Z'
  }
];

beforeEach(() => {
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string, init?: RequestInit) => {
      if (url === '/readyz') {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              ok: true,
              environment: 'dev',
              region: 'us-east-2',
              fleet_table: 'fleet'
            })
          )
        );
      }
      if (url === '/api/desktops') {
        return Promise.resolve(new Response(JSON.stringify(desktops)));
      }
      if (url === '/api/desktops/d-test/refresh') {
        const headers = new Headers(init?.headers);
        if (headers.get('Authorization') !== 'Bearer test-token') {
          return Promise.resolve(
            new Response(JSON.stringify({ error: 'missing token' }), { status: 401 })
          );
        }
        return Promise.resolve(new Response(JSON.stringify(desktops[0])));
      }
      return Promise.resolve(new Response(JSON.stringify({ error: 'not found' }), { status: 404 }));
    })
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

test('renders fleet rows and selected detail', async () => {
  render(<App />);

  await waitFor(() => expect(screen.getAllByText('d-test')).toHaveLength(2));
  expect(screen.getByText('Control Plane')).toBeInTheDocument();
  expect(screen.getByText('d-test.desktops.orchael.dev')).toBeInTheDocument();
  expect(screen.getByText('Ready')).toBeInTheDocument();
});

test('sends bearer token for mutating actions', async () => {
  render(<App />);

  await waitFor(() => expect(screen.getAllByText('d-test')).toHaveLength(2));
  fireEvent.change(screen.getByLabelText('API token'), { target: { value: 'test-token' } });
  fireEvent.click(screen.getByRole('button', { name: 'Refresh state' }));

  await waitFor(() =>
    expect(fetch).toHaveBeenCalledWith('/api/desktops/d-test/refresh', expect.anything())
  );
  const refreshCall = vi
    .mocked(fetch)
    .mock.calls.find(([url]) => url === '/api/desktops/d-test/refresh');
  const headers = new Headers(refreshCall?.[1]?.headers);
  expect(headers.get('Authorization')).toBe('Bearer test-token');
});
