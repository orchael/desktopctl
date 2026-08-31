import { auth } from '@/auth';
import { getActiveOrganization, listOrganizations, withOrganization } from '@/lib/organizations';
import { z } from 'zod';

const renameSchema = z.object({ name: z.string().trim().min(2).max(80) });

export async function GET() {
  const session = await auth();
  if (!session?.user?.id) return Response.json({ error: 'Unauthorized' }, { status: 401 });
  const memberships = await listOrganizations(session.user.id);
  return Response.json(
    memberships.map((membership) => ({
      id: membership.organizationId,
      name: membership.organization.name,
      role: membership.role
    }))
  );
}

export async function PATCH(request: Request) {
  const session = await auth();
  if (!session?.user?.id) return Response.json({ error: 'Unauthorized' }, { status: 401 });
  const parsed = renameSchema.safeParse(await request.json());
  if (!parsed.success)
    return Response.json({ error: 'Organization name must be 2–80 characters.' }, { status: 400 });
  const active = await getActiveOrganization(session.user.id);
  if (!active) return Response.json({ error: 'Organization not found' }, { status: 404 });
  try {
    const organization = await withOrganization(session.user.id, active.organizationId, (tx) =>
      tx.organization.update({
        where: { id: active.organizationId },
        data: { name: parsed.data.name },
        select: { id: true, name: true }
      })
    );
    return Response.json(organization);
  } catch {
    return Response.json({ error: 'Only organization owners can rename it.' }, { status: 403 });
  }
}
