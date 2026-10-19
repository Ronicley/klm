import 'package:flutter_test/flutter_test.dart';
import 'package:klm_harness/features/hosts/host_address.dart';
import 'package:klm_harness/features/hosts/host_connection_route.dart';
import 'package:klm_harness/features/hosts/host_store.dart';

void main() {
  test(
    'legacy hosts retain their URL, including an explicit standard port',
    () {
      final host = SavedHost.fromJson({
        'name': 'Existing',
        'url': 'http://192.168.1.10:80',
        'emoji': null,
      });
      expect(host.url.toString(), 'http://192.168.1.10');
      expect(host.url.port, 80);
      expect(host.localUrl, isNull);
      expect(HostConnectionRoute(host, 'Home').url.port, 80);
    },
  );

  test('local settings round-trip and match any selected SSID exactly', () {
    final host = SavedHost.fromJson(
      SavedHost(
        name: 'Workstation',
        url: parseHostAddress('https://klm.example.com'),
        localUrl: parseHostAddress('192.168.1.10:5173'),
        wifiSsids: ['Home', 'Home 5G'],
      ).toJson(),
    );
    expect(
      HostConnectionRoute(host, 'Home').url.toString(),
      'http://192.168.1.10:5173',
    );
    expect(HostConnectionRoute(host, 'Home 5G').usingLocal, isTrue);
    for (final ssid in [null, 'home', 'Other']) {
      final route = HostConnectionRoute(host, ssid);
      expect(route.url.toString(), 'https://klm.example.com');
      expect(route.didFallback, isFalse);
      expect(route.fallbackToPublic(), isFalse);
    }
  });

  test(
    'a failed local attempt falls back once, preserving public URL rules',
    () {
      final route = HostConnectionRoute(
        SavedHost(
          name: 'Workstation',
          url: parseHostAddress('https://klm.example.com:8443'),
          localUrl: parseHostAddress('192.168.1.10'),
          wifiSsids: ['Home'],
        ),
        'Home',
      );
      expect(route.url.port, 7332);
      expect(route.fallbackToPublic(), isTrue);
      expect(route.usingLocal, isFalse);
      expect(route.didFallback, isTrue);
      expect(route.url.toString(), 'https://klm.example.com:8443');
      expect(route.fallbackToPublic(), isFalse);
    },
  );

  test('incomplete or invalid local settings cannot enter saved storage', () {
    final base = {'name': 'Host', 'url': 'https://klm.example.com'};
    for (final local in [
      {'localUrl': 'http://10.0.0.1:7332'},
      {
        'wifiSsids': ['Home'],
      },
      {
        'localUrl': 'ftp://10.0.0.1',
        'wifiSsids': ['Home'],
      },
      {
        'localUrl': 'http://10.0.0.1',
        'wifiSsids': [42],
      },
    ]) {
      expect(
        () => SavedHost.fromJson({...base, ...local}),
        throwsFormatException,
      );
    }
  });
}
