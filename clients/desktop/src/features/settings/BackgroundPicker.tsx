import { useEffect, useRef, useState } from 'react';
import { Check, ImagePlus, Settings } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { Menu } from '../../design-system/Menu';
import { Slider } from '../../design-system/Slider';
import { backgroundPhotoSettingsKey, readBackgroundPhoto, readBackgroundPhotoSettings, removeBackgroundPhoto, saveBackgroundPhoto, type BackgroundPhotoSettings } from './backgroundPhoto';
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
  const [photoSettings, setPhotoSettings] = useState(readBackgroundPhotoSettings);
  const [photo, setPhoto] = useState<Blob>();
  const [photoURL, setPhotoURL] = useState('');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const fileInput = useRef<HTMLInputElement>(null);

  useEffect(() => {
    let active = true;
    void readBackgroundPhoto().then(value => { if (active) setPhoto(value); }, () => {
      if (active) setError('Saved background image could not be loaded. Choose an image to try again.');
    }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, []);

  useEffect(() => {
    if (!photo) { setPhotoURL(''); return; }
    const url = URL.createObjectURL(photo);
    setPhotoURL(url);
    return () => URL.revokeObjectURL(url);
  }, [photo]);

  function updatePhotoSettings(patch: Partial<BackgroundPhotoSettings>) {
    const next = { ...photoSettings, ...patch };
    setPhotoSettings(next);
    try { localStorage.setItem(backgroundPhotoSettingsKey, JSON.stringify(next)); }
    catch { setError('Background adjustments could not be saved.'); }
  }

  async function choosePhoto(file: File) {
    if (saving || loading) return;
    setError('');
    if (!file.type.startsWith('image/')) { setError('Choose an image file.'); return; }
    if (file.size > 20 * 1024 * 1024) { setError('Choose an image smaller than 20 MB.'); return; }
    setSaving(true);
    const url = URL.createObjectURL(file);
    try {
      const image = new Image();
      image.src = url;
      try { await image.decode(); } catch { throw new Error('This image could not be opened. Choose another file.'); }
      try { await saveBackgroundPhoto(file); } catch { throw new Error('This image could not be saved. Free browser storage or choose a smaller image.'); }
      setPhoto(file);
      updatePhotoSettings({ mode: 'image' });
    } catch (cause) { setError(cause instanceof Error ? cause.message : 'Background image could not be saved.'); }
    finally { URL.revokeObjectURL(url); setSaving(false); }
  }

  async function removePhoto() {
    setSaving(true); setError('');
    try {
      await removeBackgroundPhoto();
      setPhoto(undefined);
      updatePhotoSettings({ mode: 'gradient' });
    } catch { setError('Background image could not be removed. Try again.'); }
    finally { setSaving(false); }
  }

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
    updatePhotoSettings({ mode: 'gradient' });
    try { localStorage.setItem(storageKey, JSON.stringify(next)); }
    catch { /* The selection still works for this page without storage. */ }
  }

  const imageActive = photoSettings.mode === 'image' && !!photoURL;
  return <>
    {imageActive && <div className="focus-photo-background" aria-hidden="true" hidden={hidden}>
      <div className="focus-photo-image" style={{ backgroundImage: `url("${photoURL}")`, filter: `blur(${photoSettings.blur}px)`, inset: -photoSettings.blur * 3 }} />
      <div className="focus-photo-mask" style={{ backgroundColor: photoSettings.color, opacity: photoSettings.mask / 100 }} />
    </div>}
    <div className="background-settings" hidden={hidden}>
    <input ref={fileInput} type="file" accept="image/*" hidden onChange={event => {
      const file = event.currentTarget.files?.[0];
      event.currentTarget.value = '';
      if (file) void choosePhoto(file);
    }} />
    <Menu label="Background" open={open && !hidden} onOpenChange={setOpen} className="background-picker"
      trigger={props => <IconButton {...props} label="Choose background" className="background-settings-trigger"><Settings /></IconButton>}>
      <h2>Background</h2>
      <div className="background-picker-grid">
        {gradients.map(gradient => <Button key={gradient.id} variant="ghost" className="background-choice"
          aria-pressed={!imageActive && preferences[theme] === gradient.id} disabled={saving} onClick={() => choose(gradient.id)}>
          <span className="background-choice-preview" style={{ backgroundImage: `var(--gradient-canvas-${gradient.id})` }} aria-hidden="true" />
          <span className="background-choice-label">{gradient[theme]}{!imageActive && preferences[theme] === gradient.id && <Check aria-hidden="true" />}</span>
        </Button>)}
      </div>
      <div className="background-image-settings">
        {photoURL && <Button variant="ghost" className="background-choice background-photo-choice" aria-pressed={imageActive} disabled={saving} onClick={() => updatePhotoSettings({ mode: 'image' })}>
          <span className="background-choice-preview" style={{ backgroundImage: `url("${photoURL}")`, backgroundSize: 'cover', backgroundPosition: 'center' }} aria-hidden="true" />
          <span className="background-choice-label">Image{imageActive && <Check aria-hidden="true" />}</span>
        </Button>}
        <div className="background-image-actions">
          <Button size="sm" disabled={loading || saving} onClick={() => fileInput.current?.click()}><ImagePlus />{saving ? 'Saving…' : photo ? 'Change image' : 'Choose image'}</Button>
          {photo && <Button size="sm" variant="ghost" disabled={saving} onClick={() => void removePhoto()}>Remove</Button>}
        </div>
        {imageActive && <div className="background-image-adjustments">
          <Slider label="Blur" value={photoSettings.blur} min={0} max={80} valueText={`${photoSettings.blur}px`} disabled={saving} onValueChange={blur => updatePhotoSettings({ blur })} />
          <Slider label="Mask" value={photoSettings.mask} min={0} max={100} valueText={`${photoSettings.mask}%`} disabled={saving} onValueChange={mask => updatePhotoSettings({ mask })} />
          <label className="background-mask-color">Mask color<input type="color" value={photoSettings.color} disabled={saving} onChange={event => updatePhotoSettings({ color: event.currentTarget.value })} /></label>
        </div>}
        {error && <p className="form-error" role="alert">{error}</p>}
      </div>
    </Menu>
    </div>
  </>;
}
