import { expect, it, vi } from 'vitest';
import { JobStatusUnavailableError, pollJob } from '@/lib/job-polling';

it('keeps tracking after one failed status request and accepts the next result', async () => {
  const fetchStatus = vi
    .fn()
    .mockRejectedValueOnce(new Error('temporary connection loss'))
    .mockResolvedValueOnce(
      Response.json({ id: 'job-1', action: 'create', desktop_id: 'd-1234', state: 'succeeded' })
    );
  const pause = vi.fn().mockResolvedValue(undefined);
  const onUpdate = vi.fn();

  const result = await pollJob('job-1', onUpdate, fetchStatus, pause);

  expect(result.state).toBe('succeeded');
  expect(fetchStatus).toHaveBeenCalledTimes(2);
  expect(pause).toHaveBeenCalledWith(500);
  expect(onUpdate).toHaveBeenCalledOnce();
});

it('reports unavailable status after bounded retries without claiming job failure', async () => {
  const fetchStatus = vi.fn().mockRejectedValue(new Error('connection lost'));
  const pause = vi.fn().mockResolvedValue(undefined);
  await expect(pollJob('job-1', vi.fn(), fetchStatus, pause)).rejects.toBeInstanceOf(
    JobStatusUnavailableError
  );
  expect(fetchStatus).toHaveBeenCalledTimes(5);
});
