import { isIP } from 'node:net';

async function proxyFleet(request: Request, context: { params: Promise<{ path: string[] }> }) {
  const publicOrigin = process.env.CONTROL_PLANE_PUBLIC_ORIGIN || undefined;
  const bindHost = process.env.CONTROL_PLANE_BIND_HOST || '127.0.0.1';
  const host = request.headers.get('host')?.replace(/:\d+$/, '') ?? new URL(request.url).hostname;
  const allowedHosts = ['127.0.0.1', 'localhost', '[::1]', bindHost];
  if (publicOrigin) allowedHosts.push(new URL(publicOrigin).hostname);
  if (!allowedHosts.includes(host) && !(bindHost === '0.0.0.0' && isIP(host) === 4))
    return Response.json({ error: 'Invalid host' }, { status: 403 });
  const requestURL = new URL(request.url);
  const expectedOrigin =
    publicOrigin ?? `${requestURL.protocol}//${request.headers.get('host') ?? requestURL.host}`;
  if (request.method === 'POST' && request.headers.get('origin') !== expectedOrigin)
    return Response.json({ error: 'Invalid origin' }, { status: 403 });
  const { path } = await context.params;
  const base = process.env.CONTROL_PLANE_API_URL ?? 'http://127.0.0.1:8080';
  const upstream = new URL(`/api/${path.join('/')}`, base);
  upstream.search = new URL(request.url).search;
  const headers = new Headers();
  headers.set('Accept', 'application/json');
  const token = process.env.CONTROL_PLANE_API_TOKEN;
  if (!token) return Response.json({ error: 'Fleet API is not configured' }, { status: 503 });
  headers.set('Authorization', `Bearer ${token}`);
  const response = await fetch(upstream, {
    method: request.method,
    headers,
    body: ['GET', 'HEAD'].includes(request.method) ? undefined : await request.arrayBuffer(),
    cache: 'no-store',
    redirect: 'manual',
    signal: AbortSignal.timeout(30_000)
  });
  if (response.status === 303) {
    const location = response.headers.get('Location');
    let parsed: URL;
    try {
      parsed = new URL(location ?? '');
    } catch {
      return Response.json({ error: 'Invalid desktop access link' }, { status: 502 });
    }
    if (!location || parsed.protocol !== 'https:')
      return Response.json({ error: 'Invalid desktop access link' }, { status: 502 });
    return new Response(null, {
      status: 303,
      headers: { Location: location, 'Cache-Control': 'no-store', 'Referrer-Policy': 'no-referrer' }
    });
  }
  return new Response(response.body, {
    status: response.status,
    headers: { 'Content-Type': response.headers.get('Content-Type') ?? 'application/json' }
  });
}

export const GET = proxyFleet;
export const POST = proxyFleet;
