import { IS_DESKTOP } from '../../platform';
import { useEffect, useRef, type MouseEvent } from 'react';

type Activity = { label: string; blocksSwitch: boolean };
const activities = new Map<symbol, Activity>();
const listeners = new Set<() => void>();
let frozen = false;
const changed = () => listeners.forEach(listener => listener());

export const engineChangePending = () => IS_DESKTOP && frozen;
export const localActivityLabels = () => [...new Set([...activities.values()].map(activity => activity.label))];

// Actual local promises/interactions only. No server-operation ledger, polling,
// timeout, cancellation or replay is implied by releasing a registration.
export function beginLocalActivity(label: string, blocksSwitch = false) {
  if (!IS_DESKTOP) return () => {};
  if (frozen && !activities.size) throw new DOMException('Engine change in progress.', 'AbortError');
  const key = Symbol();
  activities.set(key, { label, blocksSwitch }); changed();
  return () => { activities.delete(key); changed(); };
}

export function useFilePickerActivity() {
  const finish = useRef<(() => void) | undefined>(undefined);
  const input = useRef<HTMLInputElement | undefined>(undefined);
  const cancellation = useRef<((event: Event) => void) | undefined>(undefined);
  function close() {
    if (input.current && cancellation.current) input.current.removeEventListener('cancel', cancellation.current);
    input.current = undefined; cancellation.current = undefined;
    finish.current?.(); finish.current = undefined;
  }
  useEffect(() => close, []);
  return {
    open(event: MouseEvent<HTMLInputElement>) {
      if (!IS_DESKTOP) return;
      close(); finish.current = beginLocalActivity('Project icon picker', true);
      input.current = event.currentTarget;
      cancellation.current = event => { if (IS_DESKTOP) event.stopPropagation(); close(); };
      input.current.addEventListener('cancel', cancellation.current);
    },
    close,
  };
}

export function freezeEngineActions() {
  if (frozen) throw new Error('An engine change is already in progress.');
  const blocker = [...activities.values()].find(activity => activity.blocksSwitch);
  if (blocker) throw new Error(`Finish or cancel ${blocker.label.toLowerCase()} before changing engines.`);
  frozen = true; changed();
}

export function resumeEngineActions() { frozen = false; changed(); }

export function waitForLocalActivities(signal: AbortSignal, update: (labels: string[]) => void) {
  return new Promise<void>((resolve, reject) => {
    let settling: ReturnType<typeof setTimeout> | undefined;
    const clean = () => { clearTimeout(settling); listeners.delete(check); signal.removeEventListener('abort', abort); };
    const abort = () => { clean(); reject(new DOMException('Engine change cancelled.', 'AbortError')); };
    const check = () => {
      clearTimeout(settling); update(localActivityLabels());
      if (signal.aborted) { abort(); return; }
      if (!activities.size) settling = setTimeout(() => { if (!activities.size) { clean(); resolve(); } }, 0);
    };
    listeners.add(check); signal.addEventListener('abort', abort, { once: true }); check();
  });
}
