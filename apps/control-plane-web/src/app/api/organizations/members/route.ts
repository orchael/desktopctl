import { auth } from '@/auth';
import { getActiveOrganization, withOrganization } from '@/lib/organizations';

export async function GET() {
  const session = await auth();
  if (!session?.user?.id) return Response.json({ error: 'Unauthorized' }, { status: 401 });
  const active = await getActiveOrganization(session.user.id);
  if (!active) return Response.json({ error: 'Organization not found' }, { status: 404 });
  const members = await withOrganization(session.user.id, active.organizationId, (tx) =>
    tx.membership.findMany({
      where: { organizationId: active.organizationId },
      select: {
        role: true,
        createdAt: true,
        user: { select: { id: true, name: true, email: true, image: true } }
      },
      orderBy: { createdAt: 'asc' }
    })
  );
  return Response.json(members);
}
