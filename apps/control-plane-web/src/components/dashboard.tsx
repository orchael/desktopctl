'use client';

import { useCallback, useEffect, useState } from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import type { Desktop } from '@/lib/desktop';
type Job = { id: string; action: string; desktop_id: string; state: string; error?: string };
const pendingJobKey = 'desktopctl-companion-job';

export function Dashboard() {
  const router = useRouter();
  const [desktops, setDesktops] = useState<Desktop[]>([]);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [job, setJob] = useState<Job>();
  const [showCreate, setShowCreate] = useState(false);
  const metrics = desktops.reduce(
    (value, desktop) => {
      if (desktop.lifecycle_state === 'ready') value.running++;
      else if (desktop.lifecycle_state === 'stopped') value.stopped++;
      else value.attention++;
      return value;
    },
    { running: 0, stopped: 0, attention: 0 }
  );

  const load = useCallback(async () => {
    const response = await fetch('/api/fleet/desktops', { cache: 'no-store' });
    const value = (await response.json()) as Desktop[] | { error?: string };
    if (!response.ok)
      throw new Error(('error' in value && value.error) || 'Could not load desktops');
    const next = value as Desktop[];
    setDesktops(next);
  }, []);

  useEffect(() => {
    load().catch((reason: Error) => setError(reason.message));
  }, [load]);

  const waitForJob = useCallback(
    async (id: string) => {
      setBusy(true);
      try {
        for (;;) {
          const response = await fetch(`/api/fleet/jobs/${encodeURIComponent(id)}`, {
            cache: 'no-store'
          });
          if (!response.ok)
            throw new Error((await response.json()).error ?? 'Could not read operation');
          const current = (await response.json()) as Job;
          setJob(current);
          if (current.state === 'succeeded') {
            localStorage.removeItem(pendingJobKey);
            await load();
            return;
          }
          if (current.state === 'failed') {
            localStorage.removeItem(pendingJobKey);
            throw new Error(current.error ?? 'Operation failed');
          }
          await new Promise((resolve) => setTimeout(resolve, 2000));
        }
      } catch (reason) {
        localStorage.removeItem(pendingJobKey);
        setError(reason instanceof Error ? reason.message : 'Operation failed');
      } finally {
        setBusy(false);
      }
    },
    [load]
  );

  useEffect(() => {
    const pending = localStorage.getItem(pendingJobKey);
    if (pending) void waitForJob(pending);
  }, [waitForJob]);

  async function runOperation(path: string, body?: object) {
    setError('');
    setBusy(true);
    try {
      const response = await fetch(path, {
        method: 'POST',
        headers: body ? { 'Content-Type': 'application/json' } : undefined,
        body: body ? JSON.stringify(body) : undefined
      });
      const value = (await response.json()) as Job & { error?: string };
      if (!response.ok) throw new Error(value.error ?? 'Request failed');
      if (value.id) {
        setJob(value);
        localStorage.setItem(pendingJobKey, value.id);
        await waitForJob(value.id);
      } else {
        await load();
      }
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Request failed');
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="appShell">
      <header className="topbar">
        <div className="brand">
          <strong>desktopctl</strong>
          <span>Companion App</span>
        </div>
      </header>
      <section className="content">
        {error ? (
          <div className="alert" role="alert">
            {error}
            <button onClick={() => setError('')}>Dismiss</button>
          </div>
        ) : null}
        {job && ['queued', 'running'].includes(job.state) ? (
          <div className="jobNotice" role="status">
            {job.action} {job.desktop_id}: {job.state}. This operation continues on the server if
            you close this page.
          </div>
        ) : null}
        <div className="pageTitle">
          <div>
            <h1>Fleet</h1>
            <p>Desktops managed by this desktopctl installation</p>
          </div>
          <div className="pageActions">
            <button
              className="primary"
              onClick={() => setShowCreate((current) => !current)}
              disabled={busy}
            >
              Create desktop
            </button>
            <button
              className="secondary"
              onClick={() => void load().catch((reason: Error) => setError(reason.message))}
              disabled={busy}
            >
              ↻ Refresh
            </button>
          </div>
        </div>
        {showCreate ? (
          <CreatePanel
            busy={busy}
            submit={(value) => {
              setShowCreate(false);
              void runOperation('/api/fleet/desktops', value);
            }}
          />
        ) : null}
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
                    onClick={() =>
                      router.push(`/desktops/${encodeURIComponent(desktop.desktop_id)}`)
                    }
                  >
                    <td className="mono">
                      <Link
                        href={`/desktops/${encodeURIComponent(desktop.desktop_id)}`}
                        onClick={(event) => event.stopPropagation()}
                      >
                        {desktop.desktop_id}
                      </Link>
                    </td>
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
                      No desktops found in this fleet.
                    </td>
                  </tr>
                ) : null}
              </tbody>
            </table>
          </div>
        </div>
      </section>
    </main>
  );
}

function CreatePanel({ busy, submit }: { busy: boolean; submit: (value: object) => void }) {
  return (
    <form
      className="createPanel"
      onSubmit={(event) => {
        event.preventDefault();
        const data = new FormData(event.currentTarget);
        const owner = String(data.get('owner') ?? '').trim();
        const name = String(data.get('name') ?? '').trim();
        const repos = String(data.get('repos') ?? '')
          .split(/[,\n]/)
          .map((value) => value.trim())
          .filter(Boolean);
        if (
          !window.confirm(`Create a new AWS desktop for ${owner}? This will incur cloud charges.`)
        )
          return;
        submit({
          owner,
          name,
          repos,
          instance_type: String(data.get('instance_type') ?? '').trim(),
          market_type: String(data.get('market_type') ?? 'on-demand')
        });
      }}
    >
      <h2>Create desktop</h2>
      <p>
        Uses the same provisioning and readiness checks as <code>desktopctl create</code>.
      </p>
      <div className="createFields">
        <label>
          GitHub owner <input name="owner" required maxLength={100} placeholder="your-org" />
        </label>
        <label>
          Desktop name <input name="name" maxLength={100} placeholder="optional" />
        </label>
        <label>
          Repositories <textarea name="repos" rows={2} placeholder="owner/repo, one per line" />
        </label>
        <label>
          Instance type <input name="instance_type" placeholder="configured default" />
        </label>
        <label>
          Market{' '}
          <select name="market_type">
            <option value="on-demand">On demand</option>
            <option value="spot">Spot</option>
          </select>
        </label>
      </div>
      <button className="primary" type="submit" disabled={busy}>
        Create
      </button>
    </form>
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
function formatDate(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? value
    : new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(date);
}
