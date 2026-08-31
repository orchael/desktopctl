import { auth } from '@/auth';
import { getActiveOrganization, withOrganization } from '@/lib/organizations';
import { z } from 'zod';

const schema = z.object({ role: z.enum(['OWNER', 'MEMBER']) });

export async function PATCH(request: Request, context: { params: Promise<{ userId: string }> }) {
  const session = await auth();
  if (!session?.user?.id) return Response.json({ error: 'Unauthorized' }, { status: 401 });
  const parsed = schema.safeParse(await request.json());
  if (!parsed.success) return Response.json({ error: 'Invalid role' }, { status: 400 });
  const active = await getActiveOrganization(session.user.id);
  if (!active) return Response.json({ error: 'Organization not found' }, { status: 404 });
  const { userId } = await context.params;
  try {
    const membership = await withOrganization(session.user.id, active.organizationId, (tx) =>
      tx.membership.update({
        where: { organizationId_userId: { organizationId: active.organizationId, userId } },
        data: { role: parsed.data.role }
      })
    );
    return Response.json(membership);
  } catch (error) {
    return Response.json(
      {
        error:
          error instanceof Error && error.message.includes('always have an owner')
            ? 'The final owner cannot be demoted.'
            : 'Only owners can manage members.'
      },
      { status: 403 }
    );
  }
}

export async function DELETE(_request: Request, context: { params: Promise<{ userId: string }> }) {
  const session = await auth();
  if (!session?.user?.id) return Response.json({ error: 'Unauthorized' }, { status: 401 });
  const active = await getActiveOrganization(session.user.id);
  if (!active) return Response.json({ error: 'Organization not found' }, { status: 404 });
  const { userId } = await context.params;
  try {
    await withOrganization(session.user.id, active.organizationId, (tx) =>
      tx.membership.delete({
        where: { organizationId_userId: { organizationId: active.organizationId, userId } }
      })
    );
    return new Response(null, { status: 204 });
  } catch (error) {
    return Response.json(
      {
        error:
          error instanceof Error && error.message.includes('always have an owner')
            ? 'The final owner cannot be removed.'
            : 'Only owners can manage members.'
      },
      { status: 403 }
    );
  }
}
