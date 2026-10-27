export type Job = {
  id: string;
  action: string;
  desktop_id: string;
  state: string;
  error?: string;
};

export const pendingJobKey = 'desktopctl-companion-job';

export class JobStatusUnavailableError extends Error {
  constructor() {
    super(
      'Job status is unavailable. The operation may still be running. Retry tracking or reconcile the fleet before starting another operation.'
    );
  }
}

const wait = (milliseconds: number) =>
  new Promise<void>((resolve) => setTimeout(resolve, milliseconds));
type StatusFetch = (path: string, init: RequestInit) => Promise<Response>;

export async function pollJob(
  id: string,
  onUpdate: (job: Job) => void,
  fetchStatus: StatusFetch = fetch,
  pause: (milliseconds: number) => Promise<void> = wait
): Promise<Job> {
  let failures = 0;
  for (;;) {
    let current: Job;
    try {
      const response = await fetchStatus(`/api/fleet/jobs/${encodeURIComponent(id)}`, {
        cache: 'no-store',
        signal: AbortSignal.timeout(15_000)
      });
      const value = (await response.json()) as Job & { error?: string };
      if (!response.ok) throw new Error(value.error ?? 'Could not read operation');
      current = value;
      failures = 0;
    } catch {
      failures++;
      if (failures >= 5) throw new JobStatusUnavailableError();
      await pause(Math.min(500 * 2 ** (failures - 1), 4000));
      continue;
    }
    onUpdate(current);
    if (current.state === 'succeeded' || current.state === 'failed') return current;
    await pause(2000);
  }
}

export function clearTrackedJob(id: string): void {
  if (localStorage.getItem(pendingJobKey) === id) localStorage.removeItem(pendingJobKey);
}
