import { auth } from '@/auth';
import { getActiveOrganization } from '@/lib/organizations';

async function proxyFleet(request: Request, context: { params: Promise<{ path: string[] }> }) {
  const session = await auth();
  if (!session?.user?.id) return Response.json({ error: 'Unauthorized' }, { status: 401 });
  const active = await getActiveOrganization(session.user.id);
  if (!active) return Response.json({ error: 'Organization not found' }, { status: 404 });
  const { path } = await context.params;
  const base = process.env.CONTROL_PLANE_API_URL ?? 'http://127.0.0.1:8080';
  const upstream = new URL(`/api/${path.join('/')}`, base);
  upstream.search = new URL(request.url).search;
  const headers = new Headers();
  headers.set('Accept', 'application/json');
  headers.set('X-Organization-ID', active.organizationId);
  if (process.env.CONTROL_PLANE_API_TOKEN)
    headers.set('Authorization', `Bearer ${process.env.CONTROL_PLANE_API_TOKEN}`);
  const response = await fetch(upstream, {
    method: request.method,
    headers,
    body: ['GET', 'HEAD'].includes(request.method) ? undefined : await request.arrayBuffer(),
    cache: 'no-store',
    signal: AbortSignal.timeout(30_000)
  });
  return new Response(response.body, {
    status: response.status,
    headers: { 'Content-Type': response.headers.get('Content-Type') ?? 'application/json' }
  });
}

export const GET = proxyFleet;
export const POST = proxyFleet;
