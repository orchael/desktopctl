import { afterEach, expect, it, vi } from 'vitest';
import { GET, POST } from '@/app/api/fleet/[...path]/route';

afterEach(() => {
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
});

const context = { params: Promise.resolve({ path: ['desktops'] }) };

it('fails closed without a private API token', async () => {
  vi.stubEnv('CONTROL_PLANE_API_TOKEN', '');
  const fetchMock = vi.fn();
  vi.stubGlobal('fetch', fetchMock);
  const result = await GET(new Request('https://localhost/api/fleet/desktops'), context);
  expect(result.status).toBe(503);
  expect(fetchMock).not.toHaveBeenCalled();
});

it('forwards fleet requests with the private token and no organization header', async () => {
  vi.stubEnv('CONTROL_PLANE_API_TOKEN', 'private-token');
  vi.stubEnv('CONTROL_PLANE_API_URL', 'http://api:8080');
  const fetchMock = vi
    .fn()
    .mockResolvedValue(
      new Response('[]', { status: 200, headers: { 'Content-Type': 'application/json' } })
    );
  vi.stubGlobal('fetch', fetchMock);
  const result = await GET(new Request('https://localhost/api/fleet/desktops?all=true'), context);
  expect(result.status).toBe(200);
  const [url, init] = fetchMock.mock.calls[0] as [URL, RequestInit];
  expect(url.toString()).toBe('http://api:8080/api/desktops?all=true');
  const headers = init.headers as Headers;
  expect(headers.get('Authorization')).toBe('Bearer private-token');
  expect(headers.has('X-Organization-ID')).toBe(false);
});

it('forwards a lifecycle POST', async () => {
  vi.stubEnv('CONTROL_PLANE_API_TOKEN', 'private-token');
  const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 202 }));
  vi.stubGlobal('fetch', fetchMock);
  const result = await POST(
    new Request('https://localhost/api/fleet/desktops', {
      method: 'POST',
      body: '{}',
      headers: { Origin: 'https://localhost' }
    }),
    context
  );
  expect(result.status).toBe(202);
  expect(fetchMock.mock.calls[0][1].method).toBe('POST');
});

it('rejects cross-site lifecycle requests', async () => {
  vi.stubEnv('CONTROL_PLANE_API_TOKEN', 'private-token');
  const fetchMock = vi.fn();
  vi.stubGlobal('fetch', fetchMock);
  const result = await POST(
    new Request('http://localhost/api/fleet/desktops', {
      method: 'POST',
      headers: { Origin: 'https://attacker.example' }
    }),
    context
  );
  expect(result.status).toBe(403);
  expect(fetchMock).not.toHaveBeenCalled();
});

it('accepts the configured private HTTPS origin', async () => {
  vi.stubEnv('CONTROL_PLANE_API_TOKEN', 'private-token');
  vi.stubEnv('CONTROL_PLANE_PUBLIC_ORIGIN', 'https://app.example.ts.net');
  const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 202 }));
  vi.stubGlobal('fetch', fetchMock);
  const result = await POST(
    new Request('https://app.example.ts.net/api/fleet/desktops', {
      method: 'POST',
      headers: { Origin: 'https://app.example.ts.net' }
    }),
    context
  );
  expect(result.status).toBe(202);
});

it('accepts the forwarded loopback host when Next uses its internal URL', async () => {
  vi.stubEnv('CONTROL_PLANE_API_TOKEN', 'private-token');
  const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 202 }));
  vi.stubGlobal('fetch', fetchMock);
  const result = await POST(
    new Request('http://localhost:3000/api/fleet/desktops', {
      method: 'POST',
      headers: { Host: '127.0.0.1:3000', Origin: 'http://127.0.0.1:3000' }
    }),
    context
  );
  expect(result.status).toBe(202);
});
