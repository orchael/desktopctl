import type { Desktop, DesktopSummary, Readiness } from './types';

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    headers: { Accept: 'application/json' },
    ...init
  });
  if (!response.ok) {
    let message = `HTTP ${response.status}`;
    try {
      const body = (await response.json()) as { error?: string };
      if (body.error) message = body.error;
    } catch {
      // Keep the status-derived message when the response is not JSON.
    }
    throw new Error(message);
  }
  return response.json() as Promise<T>;
}

export function getReadiness(): Promise<Readiness> {
  return request<Readiness>('/readyz');
}

export function listDesktops(): Promise<Desktop[]> {
  return request<Desktop[]>('/api/desktops');
}

export function refreshDesktop(id: string): Promise<DesktopSummary> {
  return request<DesktopSummary>(`/api/desktops/${id}/refresh`, { method: 'POST' });
}

export function startDesktop(id: string): Promise<unknown> {
  return request(`/api/desktops/${id}/start`, { method: 'POST' });
}

export function stopDesktop(id: string): Promise<unknown> {
  return request(`/api/desktops/${id}/stop`, { method: 'POST' });
}
