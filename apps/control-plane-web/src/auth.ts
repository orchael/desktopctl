import { PrismaAdapter } from '@auth/prisma-adapter';
import NextAuth from 'next-auth';
import Google from 'next-auth/providers/google';
import { organizationNameFromEmail } from '@/lib/organization-name';
import { prisma } from '@/lib/prisma';

export const { handlers, auth, signIn, signOut } = NextAuth({
  adapter: PrismaAdapter(prisma),
  providers: [
    Google({
      clientId: process.env.GOOGLE_CLIENT_ID,
      clientSecret: process.env.GOOGLE_CLIENT_SECRET
    })
  ],
  pages: { signIn: '/signin' },
  session: { strategy: 'database' },
  callbacks: {
    session({ session, user }) {
      session.user.id = user.id;
      return session;
    }
  },
  events: {
    async createUser({ user }) {
      if (!user.email) throw new Error('Google did not provide an email address');
      await prisma.$executeRaw`SELECT bootstrap_user_organization(${user.id}, ${organizationNameFromEmail(user.email)}, ${user.email})`;
    },
    async signIn({ user }) {
      if (user.id && user.email) {
        await prisma.$executeRaw`SELECT accept_user_invitations(${user.id}, ${user.email})`;
      }
    }
  }
});
