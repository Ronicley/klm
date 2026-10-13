import { useEffect, useState } from 'react';
import { Check, Settings } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { Menu } from '../../design-system/Menu';
import './background-picker.css';

type Theme = 'light' | 'dark';
const gradients = [
  { id: 'default', light: 'Aurora', dark: 'Midnight' },
  { id: 'ocean', light: 'Coast', dark: 'Ocean' },
  { id: 'rose', light: 'Rose', dark: 'Ember' },
  { id: 'forest', light: 'Meadow', dark: 'Forest' },
] as const;
type Gradient = typeof gradients[number]['id'];
const storageKey = 'klm.backgrounds.v1';

function readPreferences(): Record<Theme, Gradient> {
  const fallback: Record<Theme, Gradient> = { light: 'default', dark: 'default' };
  try {
    const saved: unknown = JSON.parse(localStorage.getItem(storageKey) ?? 'null');
    if (!saved || typeof saved !== 'object' || Array.isArray(saved)) return fallback;
    for (const theme of ['light', 'dark'] as const) {
      const value = (saved as Record<string, unknown>)[theme];
      fallback[theme] = gradients.find(gradient => gradient.id === value)?.id ?? 'default';
    }
  } catch { /* Use the original gradients when storage is unavailable. */ }
  return fallback;
}

function currentTheme(): Theme {
  return document.documentElement.dataset.theme === 'dark' ? 'dark' : 'light';
}

export function BackgroundPicker({ hidden }: { hidden: boolean }) {
  const [preferences, setPreferences] = useState(readPreferences);
  const [theme, setTheme] = useState(currentTheme);
  const [open, setOpen] = useState(false);

  useEffect(() => {
    const observer = new MutationObserver(() => setTheme(currentTheme()));
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
    setTheme(currentTheme());
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    document.documentElement.style.setProperty('--gradient-canvas', `var(--gradient-canvas-${preferences[theme]})`);
    return () => { document.documentElement.style.removeProperty('--gradient-canvas'); };
  }, [preferences, theme]);

  useEffect(() => {
    if (hidden) setOpen(false);
    const narrow = window.matchMedia('(max-width: 760px)');
    const closeOnNarrow = () => { if (narrow.matches) setOpen(false); };
    narrow.addEventListener('change', closeOnNarrow);
    return () => narrow.removeEventListener('change', closeOnNarrow);
  }, [hidden]);

  function choose(gradient: Gradient) {
    const next = { ...preferences, [theme]: gradient };
    setPreferences(next);
    try { localStorage.setItem(storageKey, JSON.stringify(next)); }
    catch { /* The selection still works for this page without storage. */ }
  }

  return <div className="background-settings" hidden={hidden}>
    <Menu label="Background" open={open && !hidden} onOpenChange={setOpen} className="background-picker"
      trigger={props => <IconButton {...props} label="Choose background" className="background-settings-trigger"><Settings /></IconButton>}>
      <h2>Background</h2>
      <div className="background-picker-grid">
        {gradients.map(gradient => <Button key={gradient.id} variant="ghost" className="background-choice"
          aria-pressed={preferences[theme] === gradient.id} onClick={() => choose(gradient.id)}>
          <span className="background-choice-preview" style={{ backgroundImage: `var(--gradient-canvas-${gradient.id})` }} aria-hidden="true" />
          <span className="background-choice-label">{gradient[theme]}{preferences[theme] === gradient.id && <Check aria-hidden="true" />}</span>
        </Button>)}
      </div>
    </Menu>
  </div>;
}
