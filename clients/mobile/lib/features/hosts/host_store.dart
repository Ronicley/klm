import 'dart:convert';
import 'package:shared_preferences/shared_preferences.dart';

class SavedHost {
  const SavedHost({
    required this.name,
    required this.url,
    this.emoji,
    this.localUrl,
    this.wifiSsids = const [],
  });
  final String name;
  final Uri url;
  final String? emoji;
  final Uri? localUrl;
  final List<String> wifiSsids;

  bool prefersLocal(String? ssid) =>
      localUrl != null && ssid != null && wifiSsids.contains(ssid);

  Map<String, Object?> toJson() => {
    'name': name,
    'url': url.toString(),
    'emoji': emoji,
    if (localUrl != null) 'localUrl': localUrl.toString(),
    if (wifiSsids.isNotEmpty) 'wifiSsids': wifiSsids,
  };

  factory SavedHost.fromJson(Object? json) {
    if (json is! Map<String, dynamic> ||
        json['name'] is! String ||
        json['url'] is! String) {
      throw const FormatException('Invalid saved host.');
    }
    final rawEmoji = json['emoji'];
    if (rawEmoji != null && rawEmoji is! String) {
      throw const FormatException('Invalid saved host.');
    }
    final name = (json['name'] as String).trim();
    final url = Uri.parse(json['url'] as String);
    final emoji = (rawEmoji as String?)?.trim();
    if (name.isEmpty ||
        (emoji != null && (emoji.isEmpty || emoji.length > 16)) ||
        !_validStoredUrl(url)) {
      throw const FormatException('Invalid saved host.');
    }
    final rawLocal = json['localUrl'];
    final rawSsids = json['wifiSsids'];
    if ((rawLocal != null && rawLocal is! String) ||
        (rawSsids != null && rawSsids is! List)) {
      throw const FormatException('Invalid local connection.');
    }
    final localUrl = rawLocal == null ? null : Uri.parse(rawLocal as String);
    final ssids = <String>[];
    for (final value in (rawSsids as List? ?? const [])) {
      if (value is! String || value.isEmpty || utf8.encode(value).length > 32) {
        throw const FormatException('Invalid Wi-Fi network.');
      }
      if (!ssids.contains(value)) ssids.add(value);
    }
    if (localUrl != null && (!_validStoredUrl(localUrl) || ssids.isEmpty) ||
        localUrl == null && ssids.isNotEmpty) {
      throw const FormatException('Invalid local connection.');
    }
    // Stored URLs are already normalized. Do not re-add 7332 to an explicit :80.
    return SavedHost(
      name: name,
      url: url,
      emoji: emoji,
      localUrl: localUrl,
      wifiSsids: List.unmodifiable(ssids),
    );
  }

  static bool _validStoredUrl(Uri url) =>
      ['http', 'https'].contains(url.scheme) &&
      url.host.isNotEmpty &&
      url.userInfo.isEmpty &&
      !url.hasQuery &&
      !url.hasFragment &&
      (url.path.isEmpty || url.path == '/');
}

class HostStore {
  final _preferences = SharedPreferencesAsync();
  static const _key = 'klm.hosts.v1';

  Future<List<SavedHost>> load() async {
    final value = await _preferences.getString(_key);
    if (value == null) return [];
    final decoded = jsonDecode(value);
    if (decoded is! List) {
      throw const FormatException('Invalid saved host list.');
    }
    return decoded.map(SavedHost.fromJson).toList();
  }

  Future<void> add(SavedHost host) async {
    final hosts = await load();
    await _save([...hosts, host]);
  }

  Future<void> updateAt(int index, SavedHost host) async {
    final hosts = await load();
    hosts[index] = host;
    await _save(hosts);
  }

  Future<void> removeAt(int index) async {
    final hosts = await load();
    hosts.removeAt(index);
    await _save(hosts);
  }

  Future<void> _save(List<SavedHost> hosts) => _preferences.setString(
    _key,
    jsonEncode(hosts.map((entry) => entry.toJson()).toList()),
  );
}
