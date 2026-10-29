'use client';

import Link from 'next/link';
import { useCallback, useEffect, useRef, useState } from 'react';
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

export function DesktopDetails({ id }: { id: string }) {
  const [desktop, setDesktop] = useState<Desktop>();
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [job, setJob] = useState<Job>();
  const [trackingUnavailable, setTrackingUnavailable] = useState(false);
  const [copyStatus, setCopyStatus] = useState<{ url: string; label: string }>();
  const mounted = useRef(false);
  const jobPoll = useRef<AbortController | null>(null);
  const access = useDesktopAccessURL(desktop);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      jobPoll.current?.abort();
    };
  }, []);

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
      if (!mounted.current) return;
      jobPoll.current?.abort();
      const controller = new AbortController();
      jobPoll.current = controller;
      setBusy(true);
      setTrackingUnavailable(false);
      setError('');
      try {
        const current = await pollJob(jobId, setJob, fetch, undefined, controller.signal);
        controller.signal.throwIfAborted();
        clearTrackedJob(jobId);
        if (current.state === 'failed') throw new Error(current.error ?? 'Operation failed');
        await load();
      } catch (reason) {
        if (controller.signal.aborted) return;
        if (reason instanceof JobStatusUnavailableError) setTrackingUnavailable(true);
        setError(reason instanceof Error ? reason.message : 'Operation failed');
      } finally {
        if (jobPoll.current === controller) jobPoll.current = null;
        if (!controller.signal.aborted) setBusy(Boolean(localStorage.getItem(pendingJobKey)));
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
      body = confirmTermination(desktop);
      if (!body) return;
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
      setBusy(Boolean(localStorage.getItem(pendingJobKey)));
    }
  }

  async function reconcileJob() {
    const pending = localStorage.getItem(pendingJobKey);
    if (!pending) return;
    try {
      const response = await fetch('/api/fleet/desktops?all=true', { cache: 'no-store' });
      const value = (await response.json()) as Desktop[] | { error?: string };
      if (!response.ok || !Array.isArray(value))
        throw new Error(('error' in value && value.error) || 'Could not refresh the fleet');
      const current = value.find((entry) => entry.desktop_id === id);
      setDesktop(current);
      clearTrackedJob(pending);
      setJob(undefined);
      setTrackingUnavailable(false);
      setError(current ? '' : 'Desktop record was not found in the refreshed fleet.');
      setBusy(false);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Could not refresh the fleet');
    }
  }

  async function copyAccessURL(url: string) {
    try {
      await navigator.clipboard.writeText(url);
      setCopyStatus({ url, label: 'Copied' });
    } catch {
      setCopyStatus({ url, label: 'Copy failed' });
    }
  }

  const browserURL = access.url;
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
        ) : job && ['queued', 'running'].includes(job.state) ? (
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
                  {desktop.desktop_id} ·{' '}
                  <span className="detailState">
                    {desktop.lifecycle_state.replaceAll('_', ' ')}
                  </span>
                </p>
              </div>
              {browserURL ? (
                <a className="primary" href={browserURL} target="_blank" rel="noopener noreferrer">
                  Open desktop ↗
                </a>
              ) : null}
            </div>
            <div className="detailToolbar">
              {noVNCURL(desktop) ? (
                <button onClick={() => void access.refresh()} disabled={access.loading}>
                  Refresh desktop URL
                </button>
              ) : null}
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
              {noVNCURL(desktop) ? (
                <div>
                  <dt>Live NoVNC URL</dt>
                  <dd>
                    {browserURL ? (
                      <span className="liveURLRow">
                        <a
                          className="accessURL"
                          href={browserURL}
                          target="_blank"
                          rel="noopener noreferrer"
                        >
                          {browserURL}
                        </a>
                        <button
                          type="button"
                          aria-label="Copy live NoVNC URL"
                          onClick={() => void copyAccessURL(browserURL)}
                        >
                          {copyStatus?.url === browserURL ? copyStatus.label : 'Copy'}
                        </button>
                      </span>
                    ) : access.loading ? (
                      'Getting desktop URL…'
                    ) : (
                      access.error || 'Desktop URL unavailable'
                    )}
                  </dd>
                </div>
              ) : null}
              {Object.entries(desktop).map(([field, value]) => (
                <div key={field}>
                  <dt>{field === 'novnc_url' ? 'Stored noVNC URL' : field.replaceAll('_', ' ')}</dt>
                  <dd>{formatDesktopField(value)}</dd>
                </div>
              ))}
            </dl>
          </>
        )}
      </section>
    </main>
  );
}
