import 'dart:async';
import 'dart:io';
import 'package:network_info_plus/network_info_plus.dart';
import 'package:permission_handler/permission_handler.dart';
import 'package:wifi_scan/wifi_scan.dart';

class WifiDiscovery {
  const WifiDiscovery(this.ssids, this.currentSsid);
  final List<String> ssids;
  final String? currentSsid;
}

class WifiNetworks {
  Future<String?> currentSsid() async {
    try {
      if (!await Permission.locationWhenInUse.isGranted) return null;
      var ssid = await NetworkInfo().getWifiName().timeout(
        const Duration(seconds: 3),
      );
      if (ssid == null) return null;
      if (Platform.isAndroid && ssid.startsWith('"') && ssid.endsWith('"')) {
        ssid = ssid.substring(1, ssid.length - 1);
      }
      return ssid.isEmpty || ssid == '<unknown ssid>' ? null : ssid;
    } catch (_) {
      // Unknown network/permission loss uses the existing public address.
      return null;
    }
  }

  Future<WifiDiscovery> discover() async {
    final permission = await Permission.locationWhenInUse.request();
    if (!permission.isGranted) {
      throw const FormatException(
        'Allow location access to read Wi-Fi networks.',
      );
    }
    final current = await currentSsid();
    final names = <String>{if (current != null) current};
    if (Platform.isAndroid) {
      final scan = WiFiScan.instance;
      if (await scan.canGetScannedResults(askPermissions: false) !=
          CanGetScannedResults.yes) {
        throw const FormatException(
          'Check that Wi-Fi and location services are enabled, then retry.',
        );
      }
      var points = await scan.getScannedResults();
      if (await scan.canStartScan(askPermissions: false) == CanStartScan.yes) {
        final result = Completer<List<WiFiAccessPoint>>();
        final subscription = scan.onScannedResultsAvailable.listen(
          (points) {
            if (!result.isCompleted) result.complete(points);
          },
          onError: (Object error) {
            if (!result.isCompleted) result.complete(points);
          },
        );
        try {
          if (await scan.startScan()) {
            points = await result.future.timeout(
              const Duration(seconds: 5),
              onTimeout: () => points,
            );
          }
        } finally {
          await subscription.cancel();
        }
      }
      names.addAll(
        points.map((point) => point.ssid).where((ssid) => ssid.isNotEmpty),
      );
    }
    final sorted = names.toList()..sort();
    return WifiDiscovery(sorted, current);
  }
}
