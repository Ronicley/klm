import 'host_store.dart';

/// Per-opening route selection. A failed local load gets one public fallback.
class HostConnectionRoute {
  HostConnectionRoute(this.host, String? ssid)
    : usingLocal = host.prefersLocal(ssid);

  final SavedHost host;
  bool usingLocal;
  bool didFallback = false;

  Uri get url => usingLocal ? host.localUrl! : host.url;

  bool fallbackToPublic() {
    if (!usingLocal) return false;
    usingLocal = false;
    didFallback = true;
    return true;
  }
}
