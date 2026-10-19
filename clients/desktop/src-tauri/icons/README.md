# Desktop icons

The transparent 1000×1000 sphere in `../../src/features/projects/klm-icon.png`
is the source for the desktop window, tray and installer icons. It is a centered
crop of the supplied transparent image, with its original pixel scale preserved.

Regenerate from `clients/desktop`:

```powershell
npx tauri icon src/features/projects/klm-icon.png --output src-tauri/icons
```

The Windows bundle uses `icon.ico` and `icon.png`. Other generated platform assets
are ignored. The web favicon is a 64×64 PNG derived from the same transparent crop.
