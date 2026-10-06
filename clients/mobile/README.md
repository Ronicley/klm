# KLM Harness

Flutter mobile client in `clients/mobile`. Native host list and host form, followed
by the selected host's existing web frontend inside `webview_flutter`.

## Run

From this directory, with Flutter stable and an Android emulator/device available:

```powershell
flutter pub get
flutter run -d emulator-5554
```

For the default Android emulator, `10.0.2.2` reaches the development computer.
A real device uses that computer's LAN IP or the configured public domain.
KLM Desktop must be running to serve its bundled frontend on 7332. The mobile
client does not start the desktop or engine.

## Hosts

**Add host** opens the native **Name / IP or domain** form. **Save** persists the
name and resolved frontend URL using `SharedPreferencesAsync`, key `klm.hosts.v1`.
The host list is local to this app installation and survives process restarts.
Saving does not require the server to be reachable. Failed writes keep the form;
failed reads offer Retry instead of replacing saved hosts with an empty list.
The home lists saved hosts, without claiming a live connection status.

Address rules:

| Input | Frontend URL |
| --- | --- |
| `192.168.1.10` | `http://192.168.1.10:7332` |
| `http://192.168.1.10:80` | HTTP port 80, preserved |
| `192.168.1.10:8080` | `http://192.168.1.10:8080` |
| `[2001:db8::1]` | `http://[2001:db8::1]:7332` |
| `[2001:db8::1]:8443` | `http://[2001:db8::1]:8443` |
| `klm.example.com` | `https://klm.example.com` (no added port) |
| `http://klm.example.com` | `http://klm.example.com` |
| `https://klm.example.com:8443` | `https://klm.example.com:8443` |

Only literal IP addresses receive the implicit 7332 port. Explicit ports always
win, including 80/443. Provided HTTP(S) is preserved; bare domains use HTTPS.
Addresses are server origins, not paths, credentials, queries or fragments.
The app opens the frontend URL; API/SSE routing remains the responsibility of the
served frontend and its tunnel/reverse-proxy deployment configuration.

## WebView and home navigation

The web page occupies the safe area with no extra native toolbar while loaded.
The KLM logo (first item of the project rail) opens the engine's general-agent
chat. Tap **Back to hosts**, below Settings at the rail footer, to dispose the
WebView route and return to the native hosts screen. This footer button is shown
only when the frontend detects the Flutter bridge. The narrow JS channel accepts only
`KlmMobile.postMessage('home')`; it exposes no native file/execution operations.
The callback checks that the current page belongs to the selected host.

The React rail explicitly supports this bridge. A delegated click/keyboard handler
also adapts the existing `.rail-brand` in previously installed frontend builds,
so the mobile app works without immediately reinstalling desktop. This fallback
skips `data-klm-general-navigation` so it cannot intercept the new general-agent
logo, and only runs in the WebView on the selected host. The logo also retains the
legacy `data-klm-host-navigation` escape marker for already-installed mobile apps;
the marker does not assign a home action. Only the footer button calls the bridge.
Android's system back also allows leaving the route.

Main-document loading failures/timeouts offer native Retry and a KLM home button.
TLS certificate errors are not bypassed. Android permits cleartext HTTP for local
hosts; iOS declares local-network access and an ATS exception for web content.
Desktop/browser/mobile drafts and web storage are independent.

## Design system

`lib/design_system` recreates the needed web primitives and tokens:

- Inter (bundled under `assets/fonts`, SIL OFL) and Lucide-outline SVG icons.
- KLM ring mark and Harness badge, page header, 44px touch icon buttons.
- Primary button, outlined input fields, neutral list cards and error treatment.
- Matching light/dark colors, 4/8/12/16/24/32 spacing, 8/12px radii.

Native appearance follows the device theme. Behavior stays in `features/hosts`
and `features/web`; generic visual components contain no host/network operations.
Launcher icons are generated from `assets/brand/icon.png` with
`dart run flutter_launcher_icons`.

## Verification — 2026-09-14

- `flutter test test/host_address_test.dart`: three targeted tests passed, covering
  IP/domain defaults, explicit ports/protocols and rejected addresses.
- Web integration: `npx tsc -b` passed in `clients/desktop`.
- `flutter run -d emulator-5554 --no-resident`: Android debug APK built, installed
  and launched on Android 15 / API 35, Flutter 3.44.8, Dart 3.12.2.
- Emulator observation: the existing **Home Workstation** host loaded the real LAN
  frontend; tapping the rail logo returned to the native list. The host remained
  after an explicit app process stop/relaunch. The native add-host form was opened
  and visually checked without adding another host.

APK: `build/app/outputs/flutter-apk/app-debug.apk`. iOS scaffolding/configuration is
included but has not been built on Windows. Physical-device behavior and a real
tunnel/proxy deployment remain for human validation. No full test suite was run.
