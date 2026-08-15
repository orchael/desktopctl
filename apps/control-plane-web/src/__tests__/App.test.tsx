import { render, screen, waitFor } from '@testing-library/react';
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
    vi.fn((url: string) => {
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
