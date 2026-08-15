import { useCallback, useEffect, useMemo, useState } from 'react';
import { getReadiness, listDesktops, refreshDesktop, startDesktop, stopDesktop } from './api';
import type { Desktop, Readiness } from './types';

const tokenStorageKey = 'ai-desktops-control-plane-token';

export default function App() {
  const [desktops, setDesktops] = useState<Desktop[]>([]);
  const [selectedID, setSelectedID] = useState<string | null>(null);
  const [readiness, setReadiness] = useState<Readiness | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [apiToken, setAPIToken] = useState(readStoredToken);

  const selected = useMemo(
    () => desktops.find((desktop) => desktop.desktop_id === selectedID) ?? desktops[0],
    [desktops, selectedID]
  );

  const load = useCallback(async () => {
    setError(null);
    try {
      const [ready, fleet] = await Promise.all([getReadiness(), listDesktops()]);
      setReadiness(ready);
      setDesktops(fleet);
      setSelectedID((current) => current ?? fleet[0]?.desktop_id ?? null);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  function updateAPIToken(value: string) {
    setAPIToken(value);
    writeStoredToken(value);
  }

  async function runAction(action: 'refresh' | 'start' | 'stop', desktop: Desktop) {
    const confirmStop =
      action !== 'stop' || window.confirm(`Stop ${desktop.desktop_id}? Active sessions may pause.`);
    if (!confirmStop) return;
    setBusy(`${action}:${desktop.desktop_id}`);
    setError(null);
    try {
      if (action === 'refresh') {
        await refreshDesktop(desktop.desktop_id, apiToken);
      } else if (action === 'start') {
        await startDesktop(desktop.desktop_id, apiToken);
      } else {
        await stopDesktop(desktop.desktop_id, apiToken);
      }
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(null);
    }
  }

  return (
    <main className="shell">
      <header className="topbar">
        <div>
          <p className="eyebrow">ai-desktops</p>
          <h1>Control Plane</h1>
        </div>
        <div className="topbarTools">
          <label className="tokenField">
            <span>API token</span>
            <input
              type="password"
              value={apiToken}
              onChange={(event) => updateAPIToken(event.target.value)}
              autoComplete="off"
            />
          </label>
          <div className="readiness" data-ok={readiness?.ok ?? false}>
            <span>{readiness?.ok ? 'Ready' : 'Not ready'}</span>
            <small>
              {readiness?.environment ?? 'unknown'} / {readiness?.region ?? 'region'}
            </small>
          </div>
        </div>
      </header>

      {error && <div className="error">{error}</div>}

      <section className="summary" aria-label="Fleet summary">
        <Metric label="Fleet" value={desktops.length} />
        <Metric
          label="Running"
          value={desktops.filter((d) => d.lifecycle_state === 'ready').length}
        />
        <Metric
          label="Stopped"
          value={desktops.filter((d) => d.lifecycle_state === 'stopped').length}
        />
        <Metric
          label="Attention"
          value={
            desktops.filter((d) =>
              ['failed', 'unhealthy', 'provisioning_failed'].includes(d.lifecycle_state)
            ).length
          }
        />
      </section>

      <section className="workspace">
        <div className="fleet">
          <div className="sectionHeader">
            <h2>Fleet</h2>
            <button type="button" onClick={() => void load()} disabled={busy !== null}>
              Refresh
            </button>
          </div>
          <table>
            <thead>
              <tr>
                <th>Desktop</th>
                <th>State</th>
                <th>Owner</th>
                <th>Region</th>
                <th>Updated</th>
              </tr>
            </thead>
            <tbody>
              {desktops.map((desktop) => (
                <tr
                  key={desktop.desktop_id}
                  className={desktop.desktop_id === selected?.desktop_id ? 'selected' : ''}
                  onClick={() => setSelectedID(desktop.desktop_id)}
                >
                  <td>{desktop.desktop_id}</td>
                  <td>
                    <StateBadge state={desktop.lifecycle_state} />
                  </td>
                  <td>{desktop.github_owner}</td>
                  <td>{desktop.region}</td>
                  <td>{formatDate(desktop.updated_at)}</td>
                </tr>
              ))}
              {desktops.length === 0 && (
                <tr>
                  <td colSpan={5} className="empty">
                    No desktops found.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>

        <aside className="detail" aria-label="Desktop detail">
          {selected ? (
            <>
              <div className="sectionHeader">
                <h2>{selected.desktop_id}</h2>
                <StateBadge state={selected.lifecycle_state} />
              </div>
              <dl>
                <Row label="Hostname" value={selected.hostname} />
                <Row label="Instance" value={selected.instance_id} />
                <Row label="Type" value={selected.instance_type ?? 'unknown'} />
                <Row label="Market" value={selected.market_type ?? 'on-demand'} />
                <Row label="Stack" value={selected.stack_name} />
                <Row label="Readiness" value={selected.readiness || 'unknown'} />
              </dl>
              <div className="actions">
                <button
                  type="button"
                  onClick={() => void runAction('refresh', selected)}
                  disabled={busy !== null}
                >
                  Refresh state
                </button>
                <button
                  type="button"
                  onClick={() => void runAction('start', selected)}
                  disabled={busy !== null || selected.lifecycle_state === 'ready'}
                >
                  Start
                </button>
                <button
                  type="button"
                  onClick={() => void runAction('stop', selected)}
                  disabled={busy !== null || selected.lifecycle_state === 'stopped'}
                >
                  Stop
                </button>
              </div>
              {selected.novnc_url && (
                <a className="primaryLink" href={selected.novnc_url}>
                  Open desktop
                </a>
              )}
            </>
          ) : (
            <p className="empty">Select a desktop to inspect it.</p>
          )}
        </aside>
      </section>
    </main>
  );
}

function readStoredToken() {
  if (typeof globalThis.localStorage?.getItem !== 'function') return '';
  return globalThis.localStorage.getItem(tokenStorageKey) ?? '';
}

function writeStoredToken(value: string) {
  if (typeof globalThis.localStorage?.setItem !== 'function') return;
  if (value) {
    globalThis.localStorage.setItem(tokenStorageKey, value);
  } else if (typeof globalThis.localStorage.removeItem === 'function') {
    globalThis.localStorage.removeItem(tokenStorageKey);
  }
}

function Metric({ label, value }: { label: string; value: number }) {
  return (
    <div className="metric">
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <>
      <dt>{label}</dt>
      <dd>{value}</dd>
    </>
  );
}

function StateBadge({ state }: { state: string }) {
  return <span className={`state state-${state}`}>{state.replace('_', ' ')}</span>;
}

function formatDate(value: string) {
  if (!value) return 'unknown';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}
