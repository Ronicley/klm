# KLM brand assets

`logo.png` is the transparent sphere used throughout the native screens, matching
the web frontend's `src/features/projects/klm-icon.png`.

`icon.png` is the solid-background sphere used only to generate mobile launcher
icons. Both masters are 1000×1000 centered crops of the supplied images, with no
rescaling of the original pixels.

Regenerate the Android and iOS launcher icons with `dart run flutter_launcher_icons`
from `clients/mobile` after updating `icon.png`.
