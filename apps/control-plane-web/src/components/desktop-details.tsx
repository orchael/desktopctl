'use client';

import Link from 'next/link';
import { useCallback, useEffect, useState } from 'react';
import { formatDesktopField, noVNCURL, type Desktop } from '@/lib/desktop';

type Job = { id: string; action: string; desktop_id: string; state: string; error?: string };
const pendingJobKey = 'desktopctl-companion-job';

export function DesktopDetails({ id }: { id: string }) {
  const [desktop, setDesktop] = useState<Desktop>();
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [job, setJob] = useState<Job>();

  const load = useCallback(async () => {
    const response = await fetch(`/api/fleet/desktops/${encodeURIComponent(id)}`, {
      cache: 'no-store'
    });
    const value = (await response.json()) as Desktop & { error?: string };
    if (!response.ok) throw new Error(value.error ?? 'Could not load desktop');
    setDesktop(value);
  }, [id]);

  const waitForJob = useCallback(
    async (jobId: string) => {
      setBusy(true);
      try {
        for (;;) {
          const response = await fetch(`/api/fleet/jobs/${encodeURIComponent(jobId)}`, {
            cache: 'no-store'
          });
          const current = (await response.json()) as Job & { error?: string };
          if (!response.ok) throw new Error(current.error ?? 'Could not read operation');
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
    void load().catch((reason: Error) => setError(reason.message));
    const pending = localStorage.getItem(pendingJobKey);
    if (pending) void waitForJob(pending);
  }, [load, waitForJob]);

  async function action(name: 'refresh' | 'start' | 'stop' | 'terminate') {
    if (!desktop) return;
    let body: object | undefined;
    if (name === 'terminate') {
      const confirm = window.prompt(
        `Type ${desktop.desktop_id} to permanently terminate this desktop and delete its disk:`
      );
      if (confirm !== desktop.desktop_id) return;
      body = { confirm };
    }
    setError('');
    setBusy(true);
    try {
      const response = await fetch(
        `/api/fleet/desktops/${encodeURIComponent(desktop.desktop_id)}/${name}`,
        {
          method: 'POST',
          headers: body ? { 'Content-Type': 'application/json' } : undefined,
          body: body ? JSON.stringify(body) : undefined
        }
      );
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

  const browserURL = desktop ? noVNCURL(desktop) : undefined;
  return (
    <main className="appShell">
      <header className="topbar">
        <div className="brand">
          <strong>desktopctl</strong>
          <span>Companion App</span>
        </div>
      </header>
      <section className="content detailPage">
        <Link className="backLink" href="/">
          ← Fleet
        </Link>
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
        {!desktop ? (
          <p role="status">Loading desktop details…</p>
        ) : (
          <>
            <div className="detailPageHead">
              <div>
                <span className="detailEyebrow">Desktop details</span>
                <h1>{desktop.desktop_name || desktop.desktop_id}</h1>
                <p>
                  {desktop.desktop_id} · <span className="detailState">{desktop.lifecycle_state.replaceAll('_', ' ')}</span>
                </p>
              </div>
              {browserURL ? (
                <a className="primary" href={browserURL} target="_blank" rel="noopener noreferrer">
                  Open desktop ↗
                </a>
              ) : null}
            </div>
            <div className="detailToolbar">
              <button onClick={() => void action('refresh')} disabled={busy}>
                Refresh state
              </button>
              <button
                onClick={() => void action('start')}
                disabled={busy || desktop.lifecycle_state === 'ready'}
              >
                Start
              </button>
              <button
                onClick={() => void action('stop')}
                disabled={busy || desktop.lifecycle_state === 'stopped'}
              >
                Stop
              </button>
              <button
                className="dangerLink"
                onClick={() => void action('terminate')}
                disabled={busy || desktop.lifecycle_state === 'terminated'}
              >
                Terminate
              </button>
            </div>
            <h2>All details</h2>
            <dl className="detailFields">
              {Object.entries(desktop).map(([field, value]) => (
                <div key={field}>
                  <dt>{field.replaceAll('_', ' ')}</dt>
                  <dd>
                    {field === 'novnc_url' && browserURL ? (
                      <a href={browserURL} target="_blank" rel="noopener noreferrer">
                        {browserURL}
                      </a>
                    ) : (
                      formatDesktopField(value)
                    )}
                  </dd>
                </div>
              ))}
            </dl>
          </>
        )}
      </section>
    </main>
  );
}
