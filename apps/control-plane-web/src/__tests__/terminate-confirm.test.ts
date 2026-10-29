import { afterEach, expect, it, vi } from 'vitest';
import { confirmTermination } from '@/lib/terminate-confirm';
import type { Desktop } from '@/lib/desktop';

afterEach(() => vi.restoreAllMocks());

const desktop = (state: string): Desktop => ({
  desktop_id: 'd-test001',
  lifecycle_state: state,
  github_owner: 'orchael',
  region: 'us-east-2',
  updated_at: '2026-10-27T00:00:00Z'
});

it.each(['failed', 'provisioning_failed'])(
  'requires an extra confirmation to force terminate %s desktops',
  (state) => {
    vi.spyOn(window, 'prompt').mockReturnValue('d-test001');
    const approve = vi.spyOn(window, 'confirm').mockReturnValue(false);
    expect(confirmTermination(desktop(state))).toBeUndefined();
    approve.mockReturnValue(true);
    expect(confirmTermination(desktop(state))).toEqual({ confirm: 'd-test001', force: true });
  }
);

it('does not force terminate a healthy desktop or accept an incorrect ID', () => {
  const prompt = vi.spyOn(window, 'prompt').mockReturnValue('wrong');
  const approve = vi.spyOn(window, 'confirm');
  expect(confirmTermination(desktop('ready'))).toBeUndefined();
  prompt.mockReturnValue('d-test001');
  expect(confirmTermination(desktop('ready'))).toEqual({ confirm: 'd-test001' });
  expect(approve).not.toHaveBeenCalled();
});
