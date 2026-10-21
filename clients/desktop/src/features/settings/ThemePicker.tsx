import { Moon, Sun } from 'lucide-react';
import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties } from 'react';
import { IconButton } from '../../design-system/Button';
import { Menu } from '../../design-system/Menu';
import './theme-picker.css';

type Mode = 'light' | 'dark';
const themes = {
  light: [
    { id: 'pearl', name: 'Pearl', colors: ['#ffffff', '#f8f9fa', '#e8eaed', '#687180', '#2563eb'] },
    { id: 'sand', name: 'Sand', colors: ['#fffcf5', '#f5f0e6', '#eee5d6', '#756956', '#95602c'] },
    { id: 'mist', name: 'Mist', colors: ['#f5faf9', '#edf4f2', '#ddeae7', '#5d756e', '#23756c'] },
  ],
  dark: [
    { id: 'onyx', name: 'Onyx', colors: ['#101111', '#080808', '#232424', '#a6aaa9', '#91adff'] },
    { id: 'slate', name: 'Slate', colors: ['#1b1d23', '#17191e', '#2b2f39', '#a0a8b8', '#91adff'] },
    { id: 'dusk', name: 'Dusk', colors: ['#211c29', '#19151f', '#393044', '#b6a8c0', '#c9aff0'] },
  ],
} as const;
const storageKey = 'klm.theme-variants.v1';

function readPreferences(): Record<Mode, string> {
  const preferences = { light: 'pearl', dark: 'onyx' };
  try {
    const saved: unknown = JSON.parse(localStorage.getItem(storageKey) ?? 'null');
    if (saved && typeof saved === 'object' && !Array.isArray(saved)) {
      for (const mode of ['light', 'dark'] as const) {
        preferences[mode] = themes[mode].find(theme => theme.id === (saved as Record<string, unknown>)[mode])?.id ?? preferences[mode];
      }
    }
  } catch { /* Keep the current light and dark palettes as defaults. */ }
  return preferences;
}

export function ThemePicker() {
  const [mode, setMode] = useState<Mode>(() => document.documentElement.dataset.theme === 'dark' ? 'dark' : 'light');
  const [preferences, setPreferences] = useState(readPreferences);
  const [open, setOpen] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const pointerStart = useRef<{ x: number; y: number } | null>(null);
  const suppressClick = useRef(false);

  function clearPress() {
    clearTimeout(timer.current);
    timer.current = undefined;
    pointerStart.current = null;
  }

  useEffect(() => () => clearTimeout(timer.current), []);
  useEffect(() => {
    // The project rail and minimized Focus bar share the document's theme.
    const root = document.documentElement;
    const observer = new MutationObserver(() => {
      const nextMode: Mode = root.dataset.theme === 'dark' ? 'dark' : 'light';
      const nextPreferences = readPreferences();
      nextPreferences[nextMode] = themes[nextMode].find(theme => theme.id === root.dataset.themeVariant)?.id ?? nextPreferences[nextMode];
      setMode(nextMode);
      setPreferences(current => current.light === nextPreferences.light && current.dark === nextPreferences.dark ? current : nextPreferences);
    });
    observer.observe(root, { attributes: true, attributeFilter: ['data-theme', 'data-theme-variant'] });
    return () => observer.disconnect();
  }, []);
  useLayoutEffect(() => {
    const root = document.documentElement;
    root.dataset.theme = mode;
    root.dataset.themeVariant = preferences[mode];
    document.querySelector('meta[name="theme-color"]')?.setAttribute('content', getComputedStyle(root).getPropertyValue('--color-rail').trim());
    try {
      localStorage.setItem('klm.theme', mode);
      localStorage.setItem(storageKey, JSON.stringify(preferences));
    } catch { /* Theme selection still works without browser storage. */ }
  }, [mode, preferences]);

  return <Menu label={`${mode === 'dark' ? 'Dark' : 'Light'} themes`} open={open} onOpenChange={setOpen}
    className="theme-picker" side="top" align="center"
    trigger={props => <IconButton {...props} className="theme-picker-trigger"
      label={mode === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'}
      aria-description="Hold to choose a theme, or press Arrow Up."
      onPointerDown={event => {
        if (!event.isPrimary || event.button !== 0) return;
        clearPress();
        suppressClick.current = false;
        pointerStart.current = { x: event.clientX, y: event.clientY };
        event.currentTarget.setPointerCapture(event.pointerId);
        timer.current = setTimeout(() => {
          timer.current = undefined;
          suppressClick.current = true;
          setOpen(true);
        }, 500);
      }}
      onPointerMove={event => {
        const start = pointerStart.current;
        if (start && Math.hypot(event.clientX - start.x, event.clientY - start.y) > 8) {
          suppressClick.current = true;
          clearPress();
        }
      }}
      onPointerUp={clearPress}
      onPointerCancel={() => { suppressClick.current = true; clearPress(); }}
      onLostPointerCapture={clearPress}
      onBlur={clearPress}
      onContextMenu={event => { event.preventDefault(); clearPress(); suppressClick.current = true; setOpen(true); }}
      onKeyDown={event => {
        if (['ArrowUp', 'ArrowDown', 'ContextMenu'].includes(event.key) || event.shiftKey && event.key === 'F10') {
          event.preventDefault(); clearPress(); setOpen(true);
        }
      }}
      onClick={event => {
        if (suppressClick.current) {
          suppressClick.current = false;
          if (event.detail !== 0) return;
        }
        setOpen(false);
        setMode(current => current === 'dark' ? 'light' : 'dark');
      }}>{mode === 'dark' ? <Sun /> : <Moon />}</IconButton>}>
    {themes[mode].map(theme => <button key={theme.id} type="button" className="theme-picker-option"
      title={theme.name} aria-label={theme.name} aria-pressed={preferences[mode] === theme.id}
      data-menu-autofocus={preferences[mode] === theme.id ? '' : undefined}
      onClick={() => { setPreferences(current => ({ ...current, [mode]: theme.id })); setOpen(false); }}>
      <span className="theme-preview" aria-hidden="true" style={{
        '--preview-surface': theme.colors[0], '--preview-sidebar': theme.colors[1],
        '--preview-subtle': theme.colors[2], '--preview-text': theme.colors[3], '--preview-accent': theme.colors[4],
      } as CSSProperties}>
        <span className="theme-preview-rail"><span /><span /><span /></span>
        <span className="theme-preview-content"><span className="theme-preview-heading" /><span className="theme-preview-line" /><span className="theme-preview-composer" /></span>
      </span>
    </button>)}
  </Menu>;
}
