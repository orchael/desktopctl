import type { Desktop } from '@/lib/desktop';

export function confirmTermination(
  desktop: Desktop
): { confirm: string; force?: boolean } | undefined {
  const confirm = window.prompt(
    `Type ${desktop.desktop_id} to permanently terminate this desktop and delete its disk:`
  );
  if (confirm !== desktop.desktop_id) return undefined;
  if (!['failed', 'provisioning_failed'].includes(desktop.lifecycle_state)) return { confirm };
  if (
    !window.confirm(
      `This desktop is ${desktop.lifecycle_state.replaceAll('_', ' ')}. Force termination will attempt to destroy any remaining resources. Continue?`
    )
  )
    return undefined;
  return { confirm, force: true };
}
