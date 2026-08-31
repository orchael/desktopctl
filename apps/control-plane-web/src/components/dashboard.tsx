'use client';

import { useCallback, useEffect, useMemo, useState, useTransition } from 'react';

type Desktop = {
  desktop_id: string;
  lifecycle_state: string;
  github_owner: string;
  region: string;
  updated_at: string;
  hostname: string;
  instance_id: string;
  instance_type?: string;
  market_type?: string;
  stack_name: string;
  readiness: string;
  novnc_url?: string;
};
type Organization = { id: string; name: string; role: 'OWNER' | 'MEMBER' };
type Member = {
  role: 'OWNER' | 'MEMBER';
  user: { id: string; name: string | null; email: string | null; image: string | null };
};

export function Dashboard({
  user,
  organizations,
  activeOrganizationId,
  initialMembers
}: {
  user: { id: string; name?: string | null; email?: string | null; image?: string | null };
  organizations: Organization[];
  activeOrganizationId: string;
  initialMembers: Member[];
}) {
  const [desktops, setDesktops] = useState<Desktop[]>([]);
  const [selectedId, setSelectedId] = useState<string>();
  const [members, setMembers] = useState(initialMembers);
  const [view, setView] = useState<'fleet' | 'organization'>('fleet');
  const [error, setError] = useState('');
  const [busy, startTransition] = useTransition();
  const active = organizations.find((organization) => organization.id === activeOrganizationId)!;
  const selected = desktops.find((desktop) => desktop.desktop_id === selectedId) ?? desktops[0];
  const metrics = useMemo(
    () =>
      desktops.reduce(
        (value, desktop) => {
          if (desktop.lifecycle_state === 'ready') value.running++;
          else if (desktop.lifecycle_state === 'stopped') value.stopped++;
          else value.attention++;
          return value;
        },
        { running: 0, stopped: 0, attention: 0 }
      ),
    [desktops]
  );

  const load = useCallback(async () => {
    const response = await fetch('/api/fleet/desktops', { cache: 'no-store' });
    if (!response.ok) throw new Error((await response.json()).error ?? 'Could not load desktops');
    const value = (await response.json()) as Desktop[];
    setDesktops(value);
    setSelectedId((current) => current ?? value[0]?.desktop_id);
  }, []);

  useEffect(() => {
    load().catch((reason: Error) => setError(reason.message));
  }, [load]);

  function mutate(path: string, init?: RequestInit, after?: () => void) {
    setError('');
    startTransition(async () => {
      const response = await fetch(path, init);
      if (!response.ok) {
        setError((await response.json()).error ?? 'Request failed');
        return;
      }
      after?.();
    });
  }

  function desktopAction(action: string) {
    if (!selected) return;
    mutate(
      `/api/fleet/desktops/${selected.desktop_id}/${action}`,
      { method: 'POST' },
      () => void load()
    );
  }
  function switchOrganization(id: string) {
    mutate(
      '/api/organizations/active',
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ organizationId: id })
      },
      () => location.reload()
    );
  }
  function refreshMembers() {
    fetch('/api/organizations/members')
      .then((response) => response.json())
      .then(setMembers);
  }

  return (
    <main className="appShell">
      <header className="topbar">
        <div className="brand">
          <strong>ai-desktops</strong>
          <span>Control Plane</span>
        </div>
        <label className="orgPicker">
          <span>Organization</span>
          <select
            value={activeOrganizationId}
            onChange={(event) => switchOrganization(event.target.value)}
            disabled={busy}
          >
            {organizations.map((organization) => (
              <option key={organization.id} value={organization.id}>
                {organization.name}
              </option>
            ))}
          </select>
        </label>
        <div className="user">
          <Avatar user={user} />
          <span>{user.name ?? user.email}</span>
        </div>
      </header>
      <aside className="rail">
        <button className={view === 'fleet' ? 'active' : ''} onClick={() => setView('fleet')}>
          <Icon name="grid" />
          Fleet
        </button>
        <button
          className={view === 'organization' ? 'active' : ''}
          onClick={() => setView('organization')}
        >
          <Icon name="users" />
          Organization
        </button>
        <a className="signout" href="/signout">
          <Icon name="exit" />
          Sign out
        </a>
      </aside>
      <section className="content">
        {error ? (
          <div className="alert" role="alert">
            {error}
            <button onClick={() => setError('')}>Dismiss</button>
          </div>
        ) : null}
        {view === 'fleet' ? (
          <>
            <div className="pageTitle">
              <div>
                <h1>Fleet</h1>
                <p>{active.name} desktops</p>
              </div>
              <button className="secondary" onClick={() => void load()} disabled={busy}>
                ↻ Refresh
              </button>
            </div>
            <div className="metrics">
              <Metric label="Running" value={metrics.running} tone="green" />
              <Metric label="Stopped" value={metrics.stopped} tone="slate" />
              <Metric label="Attention" value={metrics.attention} tone="orange" />
            </div>
            <div className="fleetLayout">
              <div className="tableWrap">
                <table>
                  <thead>
                    <tr>
                      <th>Desktop ID</th>
                      <th>State</th>
                      <th>GitHub owner</th>
                      <th>Region</th>
                      <th>Updated</th>
                    </tr>
                  </thead>
                  <tbody>
                    {desktops.map((desktop) => (
                      <tr
                        key={desktop.desktop_id}
                        className={desktop.desktop_id === selected?.desktop_id ? 'selected' : ''}
                        onClick={() => setSelectedId(desktop.desktop_id)}
                      >
                        <td className="mono">{desktop.desktop_id}</td>
                        <td>
                          <Status state={desktop.lifecycle_state} />
                        </td>
                        <td>{desktop.github_owner}</td>
                        <td>{desktop.region}</td>
                        <td>{formatDate(desktop.updated_at)}</td>
                      </tr>
                    ))}
                    {desktops.length === 0 ? (
                      <tr>
                        <td colSpan={5} className="empty">
                          No desktops belong to this organization.
                        </td>
                      </tr>
                    ) : null}
                  </tbody>
                </table>
              </div>
              <DesktopDetail desktop={selected} busy={busy} action={desktopAction} />
            </div>
          </>
        ) : (
          <OrganizationPanel
            active={active}
            members={members}
            currentUserId={user.id}
            busy={busy}
            mutate={mutate}
            refreshMembers={refreshMembers}
          />
        )}
      </section>
    </main>
  );
}

function OrganizationPanel({
  active,
  members,
  currentUserId,
  busy,
  mutate,
  refreshMembers
}: {
  active: Organization;
  members: Member[];
  currentUserId: string;
  busy: boolean;
  mutate: (path: string, init?: RequestInit, after?: () => void) => void;
  refreshMembers: () => void;
}) {
  const owner = active.role === 'OWNER';
  return (
    <div className="settings">
      <div className="pageTitle">
        <div>
          <h1>Organization</h1>
          <p>Identity and access for {active.name}</p>
        </div>
        <span className="role">{active.role}</span>
      </div>
      <section>
        <h2>Organization name</h2>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            const data = new FormData(event.currentTarget);
            mutate(
              '/api/organizations',
              {
                method: 'PATCH',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ name: data.get('name') })
              },
              () => location.reload()
            );
          }}
        >
          <input name="name" defaultValue={active.name} disabled={!owner || busy} />
          <button disabled={!owner || busy}>Save name</button>
        </form>
      </section>
      <section>
        <h2>Invite member</h2>
        <p>Invited users join after signing in with the same Google email.</p>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            const form = event.currentTarget;
            const data = new FormData(form);
            mutate(
              '/api/organizations/invitations',
              {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ email: data.get('email') })
              },
              () => {
                form.reset();
                refreshMembers();
              }
            );
          }}
        >
          <input
            type="email"
            name="email"
            placeholder="name@company.com"
            disabled={!owner || busy}
          />
          <button disabled={!owner || busy}>Invite member</button>
        </form>
      </section>
      <section>
        <h2>
          Members <span>{members.length}</span>
        </h2>
        <div className="memberList">
          {members.map((member) => (
            <div className="member" key={member.user.id}>
              <Avatar user={member.user} />
              <div>
                <strong>
                  {member.user.name ?? 'Unnamed user'}
                  {member.user.id === currentUserId ? ' (you)' : ''}
                </strong>
                <span>{member.user.email}</span>
              </div>
              <select
                aria-label={`Role for ${member.user.email}`}
                value={member.role}
                disabled={!owner || busy}
                onChange={(event) =>
                  mutate(
                    `/api/organizations/members/${member.user.id}`,
                    {
                      method: 'PATCH',
                      headers: { 'Content-Type': 'application/json' },
                      body: JSON.stringify({ role: event.target.value })
                    },
                    refreshMembers
                  )
                }
              >
                <option>OWNER</option>
                <option>MEMBER</option>
              </select>
              <button
                className="dangerLink"
                disabled={!owner || busy}
                onClick={() =>
                  mutate(
                    `/api/organizations/members/${member.user.id}`,
                    { method: 'DELETE' },
                    refreshMembers
                  )
                }
              >
                Remove
              </button>
            </div>
          ))}
        </div>
        <small>The final owner cannot be removed or demoted.</small>
      </section>
    </div>
  );
}

function DesktopDetail({
  desktop,
  busy,
  action
}: {
  desktop?: Desktop;
  busy: boolean;
  action: (name: string) => void;
}) {
  if (!desktop)
    return (
      <aside className="detail">
        <p className="empty">Select a desktop to inspect it.</p>
      </aside>
    );
  return (
    <aside className="detail">
      <div className="detailHead">
        <div>
          <Status state={desktop.lifecycle_state} />
          <h2>{desktop.desktop_id}</h2>
        </div>
      </div>
      <dl>
        {[
          ['Hostname', desktop.hostname],
          ['Instance', desktop.instance_id],
          ['Type', desktop.instance_type ?? 'unknown'],
          ['Market', desktop.market_type ?? 'on-demand'],
          ['Stack', desktop.stack_name],
          ['Readiness', desktop.readiness || 'unknown']
        ].map(([label, value]) => (
          <div key={label}>
            <dt>{label}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>
      <div className="actions">
        <button onClick={() => action('refresh')} disabled={busy}>
          Refresh state
        </button>
        <button
          className="primary"
          onClick={() => action('start')}
          disabled={busy || desktop.lifecycle_state === 'ready'}
        >
          Start
        </button>
        <button
          onClick={() => action('stop')}
          disabled={busy || desktop.lifecycle_state === 'stopped'}
        >
          Stop
        </button>
        {desktop.novnc_url ? (
          <a href={desktop.novnc_url} target="_blank" rel="noreferrer">
            Open desktop ↗
          </a>
        ) : null}
      </div>
    </aside>
  );
}
function Metric({ label, value, tone }: { label: string; value: number; tone: string }) {
  return (
    <div className="metric">
      <span>
        <i className={tone} />
        {label}
      </span>
      <strong>{value}</strong>
    </div>
  );
}
function Status({ state }: { state: string }) {
  const tone = state === 'ready' ? 'green' : state === 'stopped' ? 'slate' : 'orange';
  return (
    <span className="status">
      <i className={tone} />
      {state.replace('_', ' ')}
    </span>
  );
}
function Avatar({
  user
}: {
  user: { name?: string | null; email?: string | null; image?: string | null };
}) {
  return user.image ? (
    <img className="avatar" src={user.image} alt="" />
  ) : (
    <span className="avatar fallback">{(user.name ?? user.email ?? '?')[0]?.toUpperCase()}</span>
  );
}
function Icon({ name }: { name: string }) {
  return (
    <span className="navIcon" aria-hidden="true">
      {name === 'grid' ? '⊞' : name === 'users' ? '♙' : '↪'}
    </span>
  );
}
function formatDate(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? value
    : new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(date);
}
