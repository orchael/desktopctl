import type { DesktopInfo, ServiceStatus } from '../types';

interface Props {
  info: DesktopInfo;
}

export default function DesktopStatus({ info }: Props) {
  return (
    <div>
      <Section title="Identity">
        <Row label="Desktop ID" value={info.desktop_id} mono />
        <Row label="Hostname" value={info.hostname} mono />
        <Row label="Environment" value={info.environment} />
        <Row label="GitHub Owner" value={info.github_owner} />
      </Section>

      <Section title="Repositories">
        {info.repos.length === 0 ? (
          <p style={{ color: 'var(--muted)', fontSize: '0.85rem' }}>No repositories configured.</p>
        ) : (
          <ul style={{ listStyle: 'none', display: 'flex', flexWrap: 'wrap', gap: '0.5rem' }}>
            {info.repos.map((r) => (
              <li key={r}>
                <Tag>{r}</Tag>
              </li>
            ))}
          </ul>
        )}
      </Section>

      <Section title="Services">
        <div style={{ display: 'flex', flexDirection: 'column', gap: '0.4rem' }}>
          {info.services.map((s) => (
            <ServiceRow key={s.name} service={s} />
          ))}
        </div>
      </Section>

      <Section title="Quick Access">
        <a
          href={info.novnc_url}
          target="_blank"
          rel="noopener noreferrer"
          style={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: '0.4rem',
            background: 'var(--accent)',
            color: '#fff',
            padding: '0.5rem 1rem',
            borderRadius: 6,
            fontSize: '0.85rem',
            fontWeight: 600,
            textDecoration: 'none',
          }}
        >
          Open noVNC Desktop
        </a>
        <p style={{ color: 'var(--muted)', fontSize: '0.75rem', marginTop: '0.5rem' }}>
          Bridge port: {info.bridge_port}
        </p>
      </Section>
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section
      style={{
        background: 'var(--surface)',
        border: '1px solid var(--border)',
        borderRadius: 8,
        padding: '1rem 1.25rem',
        marginBottom: '1rem',
      }}
    >
      <h2
        style={{
          fontSize: '0.75rem',
          fontWeight: 700,
          textTransform: 'uppercase',
          letterSpacing: '0.08em',
          color: 'var(--muted)',
          marginBottom: '0.75rem',
        }}
      >
        {title}
      </h2>
      {children}
    </section>
  );
}

function Row({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div
      style={{
        display: 'flex',
        justifyContent: 'space-between',
        alignItems: 'center',
        padding: '0.25rem 0',
        borderBottom: '1px solid var(--border)',
        fontSize: '0.85rem',
      }}
    >
      <span style={{ color: 'var(--muted)' }}>{label}</span>
      <span style={mono ? { fontFamily: 'inherit' } : {}}>{value}</span>
    </div>
  );
}

function Tag({ children }: { children: React.ReactNode }) {
  return (
    <span
      style={{
        background: 'var(--bg)',
        border: '1px solid var(--border)',
        borderRadius: 4,
        padding: '0.15rem 0.5rem',
        fontSize: '0.8rem',
        color: 'var(--blue)',
      }}
    >
      {children}
    </span>
  );
}

function ServiceRow({ service }: { service: ServiceStatus }) {
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: '0.6rem',
        fontSize: '0.85rem',
      }}
    >
      <span
        style={{
          width: 8,
          height: 8,
          borderRadius: '50%',
          background: service.active ? 'var(--green)' : 'var(--red)',
          flexShrink: 0,
        }}
      />
      <span style={{ color: service.active ? 'var(--text)' : 'var(--muted)' }}>{service.name}</span>
      <span style={{ marginLeft: 'auto', color: service.active ? 'var(--green)' : 'var(--red)', fontSize: '0.75rem' }}>
        {service.active ? 'active' : 'inactive'}
      </span>
    </div>
  );
}
