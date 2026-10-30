'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import Link from 'next/link';
import { formatDesktopField, noVNCURL, type Desktop } from '@/lib/desktop';
import { useDesktopAccessURL } from '@/lib/desktop-access';
import { confirmTermination } from '@/lib/terminate-confirm';
import {
  clearTrackedJob,
  JobStatusUnavailableError,
  pendingJobKey,
  pollJob,
  type Job
} from '@/lib/job-polling';

const pendingCreateKey = 'desktopctl-companion-create-request';
type CreateAttempt = { key: string; body: object };
const hasPendingOperation = () =>
  Boolean(localStorage.getItem(pendingJobKey) || localStorage.getItem(pendingCreateKey));

function createRequestKey(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = Array.from(bytes, (value) => value.toString(16).padStart(2, '0')).join('');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

export function Dashboard() {
  const [desktops, setDesktops] = useState<Desktop[]>([]);
  const [selectedId, setSelectedId] = useState<string>();
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [job, setJob] = useState<Job>();
  const [trackingUnavailable, setTrackingUnavailable] = useState(false);
  const [createUncertain, setCreateUncertain] = useState(false);
  const [showCreate, setShowCreate] = useState(false);
  const mounted = useRef(false);
  const jobPoll = useRef<AbortController | null>(null);
  const selected = desktops.find((desktop) => desktop.desktop_id === selectedId) ?? desktops[0];
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
    setSelectedId((current) =>
      next.some((desktop) => desktop.desktop_id === current) ? current : next[0]?.desktop_id
    );
  }, []);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      jobPoll.current?.abort();
    };
  }, []);

  useEffect(() => {
    load().catch((reason: Error) => setError(reason.message));
  }, [load]);

  const waitForJob = useCallback(
    async (id: string) => {
      if (!mounted.current) return;
      jobPoll.current?.abort();
      const controller = new AbortController();
      jobPoll.current = controller;
      setBusy(true);
      setTrackingUnavailable(false);
      setError('');
      try {
        const current = await pollJob(id, setJob, fetch, undefined, controller.signal);
        controller.signal.throwIfAborted();
        clearTrackedJob(id);
        if (current.state === 'failed') throw new Error(current.error ?? 'Operation failed');
        await load();
      } catch (reason) {
        if (controller.signal.aborted) return;
        if (reason instanceof JobStatusUnavailableError) setTrackingUnavailable(true);
        setError(reason instanceof Error ? reason.message : 'Operation failed');
      } finally {
        if (jobPoll.current === controller) jobPoll.current = null;
        if (!controller.signal.aborted) setBusy(hasPendingOperation());
      }
    },
    [load]
  );

  useEffect(() => {
    const pending = localStorage.getItem(pendingJobKey);
    if (pending) {
      localStorage.removeItem(pendingCreateKey);
      void waitForJob(pending);
    } else if (localStorage.getItem(pendingCreateKey)) {
      setCreateUncertain(true);
      setBusy(true);
    }
  }, [waitForJob]);

  async function submitCreate(body: object, key = createRequestKey()) {
    localStorage.setItem(pendingCreateKey, JSON.stringify({ key, body } satisfies CreateAttempt));
    setCreateUncertain(false);
    setBusy(true);
    setError('');
    try {
      const response = await fetch('/api/fleet/desktops', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Idempotency-Key': key },
        body: JSON.stringify(body)
      });
      const value = (await response.json()) as Job & { error?: string };
      if (!response.ok) {
        if (
          response.status >= 400 &&
          response.status < 500 &&
          response.status !== 408 &&
          response.status !== 429
        )
          localStorage.removeItem(pendingCreateKey);
        throw new Error(value.error ?? 'Create request failed');
      }
      if (!value.id) throw new Error('Create response did not include a job ID');
      setJob(value);
      localStorage.setItem(pendingJobKey, value.id);
      localStorage.removeItem(pendingCreateKey);
      await waitForJob(value.id);
    } catch (reason) {
      if (localStorage.getItem(pendingCreateKey)) {
        setCreateUncertain(true);
        setError(
          'Create response is unavailable. Retry this request to recover the same operation.'
        );
      } else {
        setError(reason instanceof Error ? reason.message : 'Create request failed');
      }
    } finally {
      setBusy(hasPendingOperation());
    }
  }

  function retryCreate() {
    const saved = localStorage.getItem(pendingCreateKey);
    if (!saved) return;
    try {
      const attempt = JSON.parse(saved) as CreateAttempt;
      if (!attempt.key || !attempt.body) throw new Error('Invalid saved create request');
      void submitCreate(attempt.body, attempt.key);
    } catch {
      localStorage.removeItem(pendingCreateKey);
      setCreateUncertain(false);
      setBusy(false);
      setError(
        'Could not restore the saved create request. Check the fleet before creating another desktop.'
      );
    }
  }

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
      setBusy(hasPendingOperation());
    }
  }

  async function reconcileJob() {
    const pending = localStorage.getItem(pendingJobKey);
    if (!pending) return;
    try {
      await load();
      clearTrackedJob(pending);
      setJob(undefined);
      setTrackingUnavailable(false);
      setError('');
      setBusy(false);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Could not refresh the fleet');
    }
  }

  function action(name: 'refresh' | 'start' | 'stop' | 'terminate') {
    if (!selected) return;
    let body: object | undefined;
    if (name === 'terminate') {
      body = confirmTermination(selected);
      if (!body) return;
    }
    void runOperation(
      `/api/fleet/desktops/${encodeURIComponent(selected.desktop_id)}/${name}`,
      body
    );
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
        {trackingUnavailable ? (
          <div className="jobNotice" role="status">
            Job tracking is paused. The operation may still be running.
            <div className="trackingActions">
              <button
                onClick={() => {
                  const pending = localStorage.getItem(pendingJobKey);
                  if (pending) void waitForJob(pending);
                }}
              >
                Retry tracking
              </button>
              <button onClick={() => void reconcileJob()}>Refresh fleet and clear tracking</button>
            </div>
          </div>
        ) : createUncertain ? (
          <div className="jobNotice" role="status">
            The create request may still be running. Retry it to recover the same desktop and job.
            <div className="trackingActions">
              <button onClick={retryCreate}>Retry create request</button>
            </div>
          </div>
        ) : job && ['queued', 'running'].includes(job.state) ? (
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
              void submitCreate(value);
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
                    className={desktop.desktop_id === selected?.desktop_id ? 'selected' : ''}
                    tabIndex={0}
                    onClick={() => setSelectedId(desktop.desktop_id)}
                    onKeyDown={(event) => {
                      if (event.key === 'Enter' || event.key === ' ') {
                        event.preventDefault();
                        setSelectedId(desktop.desktop_id);
                      }
                    }}
                  >
                    <td className="mono">
                      <Link
                        href={`/desktops/${encodeURIComponent(desktop.desktop_id)}`}
                        onClick={(event) => event.stopPropagation()}
                        title={`View all details for ${desktop.desktop_id}`}
                      >
                        {desktop.desktop_id} ↗
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
          <DesktopDetail desktop={selected} busy={busy} action={action} />
        </div>
      </section>
    </main>
  );
}

function DesktopDetail({
  desktop,
  busy,
  action
}: {
  desktop?: Desktop;
  busy: boolean;
  action: (name: 'refresh' | 'start' | 'stop' | 'terminate') => void;
}) {
  const access = useDesktopAccessURL(desktop);
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
          <h2>{desktop.desktop_name || desktop.desktop_id}</h2>
        </div>
        <Link
          className="detailIcon"
          href={`/desktops/${encodeURIComponent(desktop.desktop_id)}`}
          aria-label={`View all details for ${desktop.desktop_id}`}
          title="View all details"
        >
          ↗
        </Link>
      </div>
      <dl>
        {[
          ['Hostname', desktop.hostname],
          ['Instance', desktop.instance_id],
          ['Type', desktop.instance_type],
          ['Market', desktop.market_type],
          ['Stack', desktop.stack_name],
          ['Readiness', desktop.readiness]
        ].map(([label, value]) => (
          <div key={String(label)}>
            <dt>{label}</dt>
            <dd>{formatDesktopField(value)}</dd>
          </div>
        ))}
      </dl>
      {noVNCURL(desktop) ? (
        <div className="desktopAccess">
          {access.url ? (
            <a href={access.url} target="_blank" rel="noopener noreferrer">
              Open desktop ↗
            </a>
          ) : (
            <span role="status">
              {access.loading ? 'Getting desktop URL…' : access.error || 'Desktop URL unavailable'}
            </span>
          )}
        </div>
      ) : null}
      <div className="actions">
        <button
          onClick={() => {
            if (access.error) void access.refresh();
            action('refresh');
          }}
          disabled={busy}
        >
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
        <button
          className="dangerLink"
          onClick={() => action('terminate')}
          disabled={busy || desktop.lifecycle_state === 'terminated'}
        >
          Terminate
        </button>
      </div>
    </aside>
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
