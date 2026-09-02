import { auth } from '@/auth';
import { activeOrganizationCookie, listOrganizations } from '@/lib/organizations';
import { z } from 'zod';

const schema = z.object({ organizationId: z.string().uuid() });

export async function POST(request: Request) {
  const session = await auth();
  if (!session?.user?.id) return Response.json({ error: 'Unauthorized' }, { status: 401 });
  const parsed = schema.safeParse(await request.json());
  if (!parsed.success) return Response.json({ error: 'Invalid organization' }, { status: 400 });
  const memberships = await listOrganizations(session.user.id);
  if (!memberships.some((item) => item.organizationId === parsed.data.organizationId)) {
    return Response.json({ error: 'Organization not found' }, { status: 404 });
  }
  const response = new Response(null, { status: 204 });
  response.headers.append(
    'Set-Cookie',
    `${activeOrganizationCookie}=${parsed.data.organizationId}; Path=/; HttpOnly; SameSite=Lax; Max-Age=31536000${process.env.NODE_ENV === 'production' ? '; Secure' : ''}`
  );
  return response;
}
