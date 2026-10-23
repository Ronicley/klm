import { ENGINE_URL } from '../../engine';

// Main-window coordinates are offsets from the centered browser shell.
export type FocusMainGeometry = { left: number; top: number; width: number; height: number };
export type FocusLayout = {
  maximized: boolean;
  minimized: boolean;
  front: string;
  sessionIds: string[];
  subagentIds?: Record<string, string>;
  mainGeometry?: FocusMainGeometry;
};
const storageKey = `klm.focus.layout.v1:${ENGINE_URL}`;

export function reachableMainGeometry(value: FocusMainGeometry): FocusMainGeometry {
  if (window.innerWidth <= 760) return value;
  const baseLeft = (window.innerWidth - value.width) / 2;
  const baseTop = (window.innerHeight - value.height) / 2;
  return {
    ...value,
    left: Math.max(80 - value.width, Math.min(window.innerWidth - 80, baseLeft + value.left)) - baseLeft,
    top: Math.max(8, Math.min(window.innerHeight - 56, baseTop + value.top)) - baseTop,
  };
}

export function readFocusLayout(): FocusLayout {
  const fallback: FocusLayout = { maximized: false, minimized: false, front: 'main', sessionIds: [] };
  try {
    const value = JSON.parse(localStorage.getItem(storageKey) ?? 'null') as Partial<FocusLayout> | null;
    if (!value || typeof value !== 'object' || Array.isArray(value)) return fallback;
    const geometry = value.mainGeometry;
    return {
      maximized: value.maximized === true,
      minimized: value.minimized === true,
      front: typeof value.front === 'string' && value.front ? value.front : 'main',
      sessionIds: Array.isArray(value.sessionIds) ? [...new Set(value.sessionIds.filter((id): id is string => typeof id === 'string' && id.length > 0))] : [],
      subagentIds: value.subagentIds && typeof value.subagentIds === 'object' && !Array.isArray(value.subagentIds)
        ? Object.fromEntries(Object.entries(value.subagentIds).filter(([id, childId]) => id && typeof childId === 'string' && childId)) : {},
      mainGeometry: geometry && [geometry.left, geometry.top, geometry.width, geometry.height].every(Number.isFinite) && geometry.width >= 320 && geometry.height >= 200
        ? reachableMainGeometry(geometry) : undefined,
    };
  } catch { return fallback; }
}

export function saveFocusLayout(layout: FocusLayout) {
  try { localStorage.setItem(storageKey, JSON.stringify(layout)); }
  catch { /* The current layout remains usable without browser storage. */ }
}
