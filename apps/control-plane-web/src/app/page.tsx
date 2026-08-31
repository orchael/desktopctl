import { redirect } from 'next/navigation';
import { requireUser } from '@/lib/session';
import { getActiveOrganization, listOrganizations, withOrganization } from '@/lib/organizations';
import { Dashboard } from '@/components/dashboard';

export default async function HomePage() {
  const user = await requireUser();
  const organizations = await listOrganizations(user.id);
  const active = await getActiveOrganization(user.id);
  if (!active) redirect('/signin');
  const members = await withOrganization(user.id, active.organizationId, (tx) =>
    tx.membership.findMany({
      where: { organizationId: active.organizationId },
      select: { role: true, user: { select: { id: true, name: true, email: true, image: true } } },
      orderBy: { createdAt: 'asc' }
    })
  );
  return (
    <Dashboard
      user={user}
      organizations={organizations.map((item) => ({
        id: item.organizationId,
        name: item.organization.name,
        role: item.role
      }))}
      activeOrganizationId={active.organizationId}
      initialMembers={members}
    />
  );
}
