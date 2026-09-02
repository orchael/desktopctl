import { cookies } from 'next/headers';
import type { Prisma } from '@prisma/client';
import { prisma } from '@/lib/prisma';
import { organizationNameFromEmail } from '@/lib/organization-name';

export const activeOrganizationCookie = 'ai-desktops-organization';

export async function listOrganizations(userId: string) {
  return prisma.$transaction(async (tx) => {
    await tx.$executeRaw`SELECT set_config('app.current_user_id', ${userId}, true)`;
    return tx.membership.findMany({
      where: { userId },
      select: {
        organizationId: true,
        role: true,
        organization: { select: { name: true } }
      },
      orderBy: { createdAt: 'asc' }
    });
  });
}

type Membership = Awaited<ReturnType<typeof listOrganizations>>[number];

export async function getActiveOrganization(userId: string, memberships?: Membership[]) {
  const available = memberships ?? (await listOrganizations(userId));
  const requested = (await cookies()).get(activeOrganizationCookie)?.value;
  return available.find((item) => item.organizationId === requested) ?? available[0] ?? null;
}

export async function ensureUserOrganization(userId: string, email: string) {
  await prisma.$executeRaw`SELECT bootstrap_user_organization(${userId}, ${organizationNameFromEmail(email)}, ${email})`;
}

export async function withOrganization<T>(
  userId: string,
  organizationId: string,
  work: (tx: Prisma.TransactionClient) => Promise<T>
) {
  return prisma.$transaction(async (tx) => {
    await tx.$executeRaw`SELECT set_config('app.current_user_id', ${userId}, true)`;
    await tx.$executeRaw`SELECT set_config('app.current_organization_id', ${organizationId}, true)`;
    return work(tx);
  });
}
