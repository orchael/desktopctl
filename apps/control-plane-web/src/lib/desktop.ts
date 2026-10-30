export type Desktop = {
  desktop_id: string;
  lifecycle_state: string;
  github_owner: string;
  region: string;
  updated_at: string;
  novnc_url?: string;
  [field: string]: string | boolean | number | string[] | undefined;
};

export function noVNCURL(desktop: Desktop): string | undefined {
  if (!desktop.novnc_url) return undefined;
  try {
    return new URL(desktop.novnc_url).protocol === 'https:' ? desktop.novnc_url : undefined;
  } catch {
    return undefined;
  }
}

export function formatDesktopField(value: Desktop[string]): string {
  if (Array.isArray(value)) return value.join(', ') || '—';
  if (typeof value === 'boolean') return value ? 'Yes' : 'No';
  if (value === undefined || value === '') return '—';
  return String(value);
}
