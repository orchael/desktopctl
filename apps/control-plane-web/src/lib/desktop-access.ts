import { useCallback, useEffect, useRef, useState } from 'react';
import { noVNCURL, type Desktop } from '@/lib/desktop';

type AccessState = { id?: string; url?: string; loading: boolean; error?: string };

export function useDesktopAccessURL(desktop?: Desktop) {
  const id = desktop?.desktop_id;
  const baseURL = desktop ? noVNCURL(desktop) : undefined;
  const stateRequest = useRef(0);
  const [state, setState] = useState<AccessState>({ loading: false });

  const refresh = useCallback(async () => {
    const request = ++stateRequest.current;
    if (!id || !baseURL || !['ready', 'unhealthy'].includes(desktop?.lifecycle_state ?? '')) {
      setState({ id, loading: false });
      return;
    }
    setState({ id, loading: true });
    try {
      const response = await fetch(`/api/fleet/desktops/${encodeURIComponent(id)}/access`, {
        cache: 'no-store',
        signal: AbortSignal.timeout(15_000)
      });
      if (!response.ok) throw new Error('Could not get desktop access URL');
      const value = (await response.json()) as { url?: string };
      const url = new URL(value.url ?? '');
      if (
        url.protocol !== 'https:' ||
        url.hostname !== new URL(baseURL).hostname ||
        url.pathname !== '/access' ||
        !url.searchParams.get('token')
      )
        throw new Error('Desktop returned an invalid access URL');
      if (stateRequest.current === request) setState({ id, url: url.href, loading: false });
    } catch (reason) {
      if (stateRequest.current === request)
        setState({
          id,
          loading: false,
          error: reason instanceof Error ? reason.message : 'Could not get desktop access URL'
        });
    }
  }, [id, baseURL, desktop?.lifecycle_state]);

  useEffect(() => {
    void refresh();
    return () => {
      stateRequest.current++;
    };
  }, [refresh]);

  return {
    url: state.id === id ? state.url : undefined,
    loading: state.id === id && state.loading,
    error: state.id === id ? state.error : undefined,
    refresh
  };
}
