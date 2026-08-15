import type { Desktop, DesktopSummary, Readiness } from './types';

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers);
  headers.set('Accept', 'application/json');
  const response = await fetch(path, {
    ...init,
    headers
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

function mutationInit(token: string): RequestInit {
  return {
    method: 'POST',
    headers: token ? { Authorization: `Bearer ${token}` } : {}
  };
}

export function getReadiness(): Promise<Readiness> {
  return request<Readiness>('/readyz');
}

export function listDesktops(): Promise<Desktop[]> {
  return request<Desktop[]>('/api/desktops');
}

export function refreshDesktop(id: string, token: string): Promise<DesktopSummary> {
  return request<DesktopSummary>(`/api/desktops/${id}/refresh`, mutationInit(token));
}

export function startDesktop(id: string, token: string): Promise<unknown> {
  return request(`/api/desktops/${id}/start`, mutationInit(token));
}

export function stopDesktop(id: string, token: string): Promise<unknown> {
  return request(`/api/desktops/${id}/stop`, mutationInit(token));
}
