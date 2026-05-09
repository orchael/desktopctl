import { useEffect, useState } from 'react';
import DesktopStatus from './components/DesktopStatus';
import type { DesktopInfo } from './types';

export default function App() {
  const [info, setInfo] = useState<DesktopInfo | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    fetch('/api/desktop')
      .then((r) => r.json())
      .then(setInfo)
      .catch((e) => setError(String(e)));
  }, []);

  return (
    <div style={{ maxWidth: 900, margin: '0 auto', padding: '2rem' }}>
      <Header />
      {error && <ErrorBanner message={error} />}
      {info ? <DesktopStatus info={info} /> : !error && <Loading />}
    </div>
  );
}

function Header() {
  return (
    <header style={{ marginBottom: '2rem', borderBottom: '1px solid var(--border)', paddingBottom: '1rem' }}>
      <h1 style={{ color: 'var(--accent)', fontSize: '1.5rem', fontWeight: 700 }}>
        AI Desktop
      </h1>
      <p style={{ color: 'var(--muted)', marginTop: '0.25rem', fontSize: '0.85rem' }}>
        Local desktop status dashboard
      </p>
    </header>
  );
}

function Loading() {
  return <p style={{ color: 'var(--muted)' }}>Loading desktop info…</p>;
}

function ErrorBanner({ message }: { message: string }) {
  return (
    <div style={{ background: '#450a0a', border: '1px solid var(--red)', borderRadius: 6, padding: '0.75rem 1rem', marginBottom: '1rem', color: 'var(--red)', fontSize: '0.85rem' }}>
      {message}
    </div>
  );
}
