import Link from 'next/link';
import { auth } from '@/auth';
import { Dashboard } from '@/components/dashboard';
import {
  ensureUserOrganization,
  getActiveOrganization,
  listOrganizations,
  withOrganization
} from '@/lib/organizations';

export default async function HomePage() {
  const session = await auth();
  if (!session?.user?.id) return <LandingPage />;

  let organizations = await listOrganizations(session.user.id);
  if (organizations.length === 0 && session.user.email) {
    await ensureUserOrganization(session.user.id, session.user.email);
    organizations = await listOrganizations(session.user.id);
  }

  const active = await getActiveOrganization(session.user.id, organizations);
  if (!active) return <MissingOrganization />;

  const members = await withOrganization(session.user.id, active.organizationId, (tx) =>
    tx.membership.findMany({
      where: { organizationId: active.organizationId },
      select: { role: true, user: { select: { id: true, name: true, email: true, image: true } } },
      orderBy: { createdAt: 'asc' }
    })
  );
  return (
    <Dashboard
      user={session.user}
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

function LandingPage() {
  return (
    <main className="landingPage">
      <header className="landingHeader">
        <Link className="landingBrand" href="/" aria-label="ai-desktops home">
          ai-desktops
        </Link>
        <Link className="landingSignIn landingSignInSmall" href="/signin">
          Sign in
        </Link>
      </header>

      <section className="landingHero">
        <div className="landingCopy">
          <h1>Your cloud workspace, ready when you are.</h1>
          <p>Persistent development desktops for focused work, managed in one place.</p>
          <Link className="landingSignIn" href="/signin">
            Sign in
          </Link>
        </div>
        <WorkspacePreview />
      </section>

      <section className="principles" aria-label="Product principles">
        <div>
          <DatabaseIcon />
          <span>Persistent by default</span>
        </div>
        <div>
          <ShieldIcon />
          <span>Private to your organization</span>
        </div>
        <div>
          <CodeIcon />
          <span>Built for real development work</span>
        </div>
      </section>
    </main>
  );
}

function WorkspacePreview() {
  return (
    <div className="workspacePreview" aria-hidden="true">
      <div className="workspaceBar">
        <span className="windowDots">
          <i />
          <i />
          <i />
        </span>
        <strong>web-app</strong>
        <span className="workspaceStatus">
          <i />
          Running
        </span>
      </div>
      <div className="workspaceBody">
        <aside className="workspaceList">
          <span>Desktops</span>
          <strong>
            web-app <i />
          </strong>
          <span>api-service</span>
          <span>data-pipeline</span>
          <span>ml-sandbox</span>
        </aside>
        <div className="workspaceCode">
          <div className="codeTab">server.ts</div>
          <pre>
            <code>{`import { createServer } from "http";

const server = createServer((req, res) => {
  if (req.url === "/health") {
    res.end(JSON.stringify({ status: "ok" }));
    return;
  }
});

server.listen(3000);`}</code>
          </pre>
          <div className="workspaceTerminal">
            <span>Terminal</span>
            <code>$ pnpm run dev</code>
            <code className="terminalSuccess">Server running on port 3000</code>
          </div>
        </div>
      </div>
    </div>
  );
}

function MissingOrganization() {
  return (
    <main className="organizationMissing">
      <div className="landingBrand">ai-desktops</div>
      <h1>Your account needs an organization.</h1>
      <p>We couldn’t create your workspace organization. Sign out and try again.</p>
      <Link className="landingSignIn" href="/signout">
        Sign out
      </Link>
    </main>
  );
}

function DatabaseIcon() {
  return (
    <svg viewBox="0 0 24 24">
      <ellipse cx="12" cy="5" rx="7" ry="3" />
      <path d="M5 5v7c0 1.7 3.1 3 7 3s7-1.3 7-3V5M5 12v7c0 1.7 3.1 3 7 3s7-1.3 7-3v-7" />
    </svg>
  );
}

function ShieldIcon() {
  return (
    <svg viewBox="0 0 24 24">
      <path d="M12 2 20 6v6c0 5-3.4 8.4-8 10-4.6-1.6-8-5-8-10V6l8-4Z" />
      <rect x="9" y="10" width="6" height="6" rx="1" />
      <path d="M10.5 10V8.5a1.5 1.5 0 0 1 3 0V10" />
    </svg>
  );
}

function CodeIcon() {
  return (
    <svg viewBox="0 0 24 24">
      <path d="m8 6-6 6 6 6M16 6l6 6-6 6M14 3l-4 18" />
    </svg>
  );
}
