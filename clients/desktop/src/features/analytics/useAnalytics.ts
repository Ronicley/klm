import { useCallback, useEffect, useState } from 'react';
import { getAnalytics, type AnalyticsQuery, type AnalyticsResponse } from './analyticsApi';

export function useAnalytics(visible: boolean, query: AnalyticsQuery) {
  const key = JSON.stringify(query);
  const [state, setState] = useState<{ key: string; data?: AnalyticsResponse; error?: string; loading: boolean }>({ key, loading: false });
  const [retry, setRetry] = useState(0);
  const refresh = useCallback(() => setRetry(value => value + 1), []);
  useEffect(() => {
    if (!visible) return;
    let controller: AbortController | undefined;
    let sequence = 0;
    const load = () => {
      if (document.visibilityState === 'hidden') { controller?.abort(); return; }
      controller?.abort(); controller = new AbortController();
      const signal = controller.signal;
      const generation = ++sequence;
      setState(current => ({ key, data: current.key === key ? current.data : undefined, loading: true }));
      void getAnalytics(query, signal).then(data => {
        if (!signal.aborted && generation === sequence) setState({ key, data, loading: false });
      }, reason => {
        if (!signal.aborted && generation === sequence) setState(current => ({ key, data: current.key === key ? current.data : undefined, loading: false, error: reason instanceof Error ? reason.message : 'Could not load Analytics.' }));
      });
    };
    load();
    const timer = window.setInterval(load, 15000);
    window.addEventListener('focus', load);
    document.addEventListener('visibilitychange', load);
    return () => { sequence++; controller?.abort(); window.clearInterval(timer); window.removeEventListener('focus', load); document.removeEventListener('visibilitychange', load); };
    // key is the full request identity; obsolete responses cannot cross scopes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [visible, key, retry]);
  return { data: state.key === key ? state.data : undefined, error: state.key === key ? state.error : undefined, loading: state.key !== key || state.loading, refresh };
}
