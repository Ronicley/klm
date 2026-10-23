import 'package:flutter/material.dart';
import '../../design_system/components.dart';
import '../../design_system/icons.dart';
import '../../design_system/theme.dart';
import 'host_address.dart';
import 'host_store.dart';
import 'wifi_picker_page.dart';

class HostFormPage extends StatefulWidget {
  const HostFormPage({
    super.key,
    required this.store,
    this.host,
    this.hostIndex,
  }) : assert((host == null) == (hostIndex == null));

  final HostStore store;
  final SavedHost? host;
  final int? hostIndex;

  @override
  State<HostFormPage> createState() => _HostFormPageState();
}

class _HostFormPageState extends State<HostFormPage> {
  static const _emojis = [
    '💻',
    '🖥️',
    '🏠',
    '🏢',
    '☁️',
    '🌐',
    '📡',
    '🧪',
    '🛠️',
    '🚀',
    '🤖',
    '🧠',
  ];

  final _form = GlobalKey<FormState>();
  final _name = TextEditingController();
  final _address = TextEditingController();
  final _localAddress = TextEditingController();
  List<String> _wifiSsids = [];
  String? _emoji;
  bool _saving = false;
  String? _error;

  bool get _editing => widget.host != null;

  @override
  void initState() {
    super.initState();
    final host = widget.host;
    if (host != null) {
      _name.text = host.name;
      _address.text = host.url.toString();
      _emoji = host.emoji;
      _localAddress.text = host.localUrl?.toString() ?? '';
      _wifiSsids = [...host.wifiSsids];
    }
  }

  Future<void> _save() async {
    if (_saving || !_form.currentState!.validate()) return;
    setState(() {
      _saving = true;
      _error = null;
    });
    try {
      final host = SavedHost(
        name: _name.text.trim(),
        url: parseHostAddress(_address.text),
        emoji: _emoji,
        localUrl: _wifiSsids.isEmpty
            ? null
            : parseHostAddress(_localAddress.text),
        wifiSsids: List.unmodifiable(_wifiSsids),
      );
      if (_editing) {
        await widget.store.updateAt(widget.hostIndex!, host);
      } else {
        await widget.store.add(host);
      }
      if (mounted) Navigator.of(context).pop(true);
    } catch (_) {
      if (mounted) {
        setState(() {
          _saving = false;
          _error = 'Could not save this web client. Please retry.';
        });
      }
    }
  }

  @override
  void dispose() {
    _name.dispose();
    _address.dispose();
    _localAddress.dispose();
    super.dispose();
  }

  Future<void> _selectNetworks() async {
    final selected = await Navigator.of(context).push<List<String>>(
      MaterialPageRoute(builder: (_) => WifiPickerPage(selected: _wifiSsids)),
    );
    if (mounted && selected != null) setState(() => _wifiSsids = selected);
  }

  @override
  Widget build(BuildContext context) => PopScope(
    canPop: !_saving,
    child: KlmPage(
      title: Text(_editing ? 'Edit web client' : 'Add web client'),
      leading: KlmIconButton(
        label: 'Back',
        onPressed: _saving ? null : () => Navigator.of(context).pop(),
        child: const KlmIcon(KlmSymbol.arrowLeft),
      ),
      child: SingleChildScrollView(
        padding: const EdgeInsets.all(KlmSpace.xl),
        child: Form(
          key: _form,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              KlmCard(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    KlmField(
                      label: 'Name',
                      hint: 'Home workstation',
                      controller: _name,
                      enabled: !_saving,
                      validator: (value) =>
                          value == null || value.trim().isEmpty
                          ? 'Enter a name.'
                          : value.trim().length > 60
                          ? 'Use up to 60 characters.'
                          : null,
                    ),
                    const SizedBox(height: KlmSpace.xl),
                    KlmField(
                      label: 'Web client URL',
                      hint: 'https://klm.example.com',
                      controller: _address,
                      keyboardType: TextInputType.url,
                      action: TextInputAction.done,
                      enabled: !_saving,
                      validator: (value) {
                        try {
                          parseHostAddress(value ?? '');
                          return null;
                        } on FormatException catch (error) {
                          return error.message;
                        }
                      },
                    ),
                    const SizedBox(height: KlmSpace.xl),
                    if (_wifiSsids.isEmpty)
                      OutlinedButton.icon(
                        onPressed: _saving ? null : _selectNetworks,
                        icon: const KlmIcon(KlmSymbol.plus),
                        label: const Text('Add local URL'),
                      )
                    else ...[
                      Row(
                        children: [
                          const Expanded(
                            child: Text(
                              'Local connection',
                              style: TextStyle(fontWeight: FontWeight.w500),
                            ),
                          ),
                          TextButton(
                            onPressed: _saving
                                ? null
                                : () => setState(() {
                                    _wifiSsids = [];
                                    _localAddress.clear();
                                  }),
                            child: const Text('Remove'),
                          ),
                        ],
                      ),
                      TextButton(
                        onPressed: _saving ? null : _selectNetworks,
                        child: Align(
                          alignment: Alignment.centerLeft,
                          child: Text(_wifiSsids.join(', ')),
                        ),
                      ),
                      const SizedBox(height: KlmSpace.sm),
                      KlmField(
                        label: 'Local URL',
                        hint: '192.168.1.10',
                        controller: _localAddress,
                        keyboardType: TextInputType.url,
                        action: TextInputAction.done,
                        enabled: !_saving,
                        validator: (value) {
                          try {
                            parseHostAddress(value ?? '');
                            return null;
                          } on FormatException catch (error) {
                            return error.message;
                          }
                        },
                      ),
                    ],
                    const SizedBox(height: KlmSpace.xl),
                    const Text(
                      'Icon',
                      style: TextStyle(
                        fontSize: 13,
                        fontWeight: FontWeight.w500,
                      ),
                    ),
                    const SizedBox(height: KlmSpace.sm),
                    Wrap(
                      spacing: KlmSpace.sm,
                      runSpacing: KlmSpace.sm,
                      children: [
                        _HostIconChoice(
                          label: 'Server icon',
                          selected: _emoji == null,
                          onTap: _saving
                              ? null
                              : () => setState(() => _emoji = null),
                          child: KlmIcon(
                            KlmSymbol.server,
                            color: KlmColors.of(context).muted,
                          ),
                        ),
                        for (final emoji in _emojis)
                          _HostIconChoice(
                            label: '$emoji icon',
                            selected: _emoji == emoji,
                            onTap: _saving
                                ? null
                                : () => setState(() => _emoji = emoji),
                            child: Text(
                              emoji,
                              style: const TextStyle(fontSize: 24),
                            ),
                          ),
                      ],
                    ),
                  ],
                ),
              ),
              if (_error != null) ...[
                const SizedBox(height: KlmSpace.lg),
                KlmError(_error!),
              ],
              const SizedBox(height: KlmSpace.xl),
              KlmButton(
                label: _saving
                    ? 'Saving...'
                    : _editing
                    ? 'Save changes'
                    : 'Add web client',
                busy: _saving,
                onPressed: _save,
              ),
            ],
          ),
        ),
      ),
    ),
  );
}

class _HostIconChoice extends StatelessWidget {
  const _HostIconChoice({
    required this.label,
    required this.selected,
    required this.onTap,
    required this.child,
  });

  final String label;
  final bool selected;
  final VoidCallback? onTap;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final colors = KlmColors.of(context);
    return Semantics(
      button: true,
      selected: selected,
      label: label,
      child: Material(
        color: selected ? colors.accentSoft : colors.surface,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(8),
          side: BorderSide(color: selected ? colors.accent : colors.border),
        ),
        clipBehavior: Clip.antiAlias,
        child: InkWell(
          onTap: onTap,
          child: SizedBox.square(dimension: 48, child: Center(child: child)),
        ),
      ),
    );
  }
}
