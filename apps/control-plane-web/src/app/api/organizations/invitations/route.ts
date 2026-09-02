import { auth } from '@/auth';
import { getActiveOrganization, withOrganization } from '@/lib/organizations';
import { z } from 'zod';

const schema = z.object({
  email: z
    .string()
    .email()
    .transform((value) => value.toLowerCase())
});

export async function POST(request: Request) {
  const session = await auth();
  if (!session?.user?.id) return Response.json({ error: 'Unauthorized' }, { status: 401 });
  const parsed = schema.safeParse(await request.json());
  if (!parsed.success)
    return Response.json({ error: 'Enter a valid email address.' }, { status: 400 });
  const active = await getActiveOrganization(session.user.id);
  if (!active) return Response.json({ error: 'Organization not found' }, { status: 404 });
  try {
    const invitation = await withOrganization(
      session.user.id,
      active.organizationId,
      async (tx) => {
        const existing = await tx.user.findUnique({
          where: { email: parsed.data.email },
          select: { id: true }
        });
        if (existing) {
          await tx.membership.upsert({
            where: {
              organizationId_userId: { organizationId: active.organizationId, userId: existing.id }
            },
            create: { organizationId: active.organizationId, userId: existing.id },
            update: {}
          });
          return { accepted: true };
        }
        return tx.invitation.upsert({
          where: {
            organizationId_email: {
              organizationId: active.organizationId,
              email: parsed.data.email
            }
          },
          create: {
            organizationId: active.organizationId,
            email: parsed.data.email,
            invitedById: session.user.id
          },
          update: { invitedById: session.user.id },
          select: { id: true, email: true, role: true }
        });
      }
    );
    return Response.json(invitation, { status: 201 });
  } catch {
    return Response.json(
      { error: 'Only organization owners can invite members.' },
      { status: 403 }
    );
  }
}
