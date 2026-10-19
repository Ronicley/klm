import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:permission_handler/permission_handler.dart';
import '../../design_system/components.dart';
import '../../design_system/icons.dart';
import '../../design_system/theme.dart';
import 'wifi_networks.dart';

class WifiPickerPage extends StatefulWidget {
  const WifiPickerPage({super.key, required this.selected});
  final List<String> selected;

  @override
  State<WifiPickerPage> createState() => _WifiPickerPageState();
}

class _WifiPickerPageState extends State<WifiPickerPage> {
  late final Set<String> _selected = widget.selected.toSet();
  late final Set<String> _networks = widget.selected.toSet();
  final _name = TextEditingController();
  String? _current;
  String? _error;
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _scan();
  }

  Future<void> _scan() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final result = await WifiNetworks().discover();
      if (mounted)
        setState(() {
          _networks.addAll(result.ssids);
          _current = result.currentSsid;
        });
    } catch (error) {
      if (mounted)
        setState(() {
          _error = error is FormatException
              ? error.message
              : 'Could not read Wi-Fi networks. Please retry.';
        });
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  void _addNetwork() {
    final name = _name.text;
    if (name.trim().isEmpty || utf8.encode(name).length > 32) {
      setState(() => _error = 'Enter a Wi-Fi name using up to 32 bytes.');
      return;
    }
    setState(() {
      _networks.add(name);
      _selected.add(name);
      _name.clear();
      _error = null;
    });
  }

  @override
  void dispose() {
    _name.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final networks = _networks.toList()..sort();
    final colors = KlmColors.of(context);
    return KlmPage(
      title: const Text('Wi-Fi networks'),
      leading: KlmIconButton(
        label: 'Back',
        onPressed: () => Navigator.of(context).pop(),
        child: const KlmIcon(KlmSymbol.arrowLeft),
      ),
      actions: [
        KlmIconButton(
          label: 'Refresh networks',
          onPressed: _loading ? null : _scan,
          child: const KlmIcon(KlmSymbol.retry),
        ),
      ],
      child: Column(
        children: [
          if (_loading) const LinearProgressIndicator(),
          Expanded(
            child: ListView(
              padding: const EdgeInsets.all(24),
              children: [
                if (_error != null) ...[
                  KlmError(_error!),
                  Align(
                    alignment: Alignment.centerLeft,
                    child: TextButton(
                      onPressed: openAppSettings,
                      child: const Text('Open settings'),
                    ),
                  ),
                ],
                for (final ssid in networks)
                  CheckboxListTile(
                    contentPadding: EdgeInsets.zero,
                    title: Text(ssid),
                    subtitle: ssid == _current
                        ? Text(
                            'Connected',
                            style: TextStyle(color: colors.muted),
                          )
                        : null,
                    value: _selected.contains(ssid),
                    onChanged: (value) => setState(() {
                      if (value == true) {
                        _selected.add(ssid);
                      } else {
                        _selected.remove(ssid);
                      }
                    }),
                  ),
                if (!_loading && networks.isEmpty && _error == null)
                  const Text('No Wi-Fi networks found.'),
                const SizedBox(height: 24),
                KlmField(
                  label: 'Wi-Fi name',
                  hint: 'Network name (SSID)',
                  controller: _name,
                  action: TextInputAction.done,
                ),
                Align(
                  alignment: Alignment.centerLeft,
                  child: TextButton(
                    onPressed: _addNetwork,
                    child: const Text('Add network'),
                  ),
                ),
              ],
            ),
          ),
          Padding(
            padding: const EdgeInsets.all(24),
            child: SizedBox(
              width: double.infinity,
              child: KlmButton(
                label: 'Use selected networks',
                onPressed: _selected.isEmpty
                    ? null
                    : () =>
                          Navigator.of(context).pop(_selected.toList()..sort()),
              ),
            ),
          ),
        ],
      ),
    );
  }
}
