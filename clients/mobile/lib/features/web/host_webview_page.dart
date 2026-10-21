import 'dart:async';
import 'package:flutter/material.dart';
import 'package:webview_flutter/webview_flutter.dart';
import '../../design_system/components.dart';
import '../../design_system/icons.dart';
import '../../design_system/theme.dart';
import '../hosts/host_store.dart';
import '../hosts/host_connection_route.dart';
import '../hosts/wifi_networks.dart';
import 'host_audio_bridge.dart';
import 'host_navigation.dart';

class HostWebViewPage extends StatefulWidget {
  const HostWebViewPage({super.key, required this.host});
  final SavedHost host;

  @override
  State<HostWebViewPage> createState() => _HostWebViewPageState();
}

class _HostWebViewPageState extends State<HostWebViewPage> {
  HostConnectionRoute? _route;
  int _attempt = 0;
  bool _fallbackNoticeShown = false;

  @override
  void initState() {
    super.initState();
    if (widget.host.localUrl == null) {
      _route = HostConnectionRoute(widget.host, null);
    } else {
      _resolve();
    }
  }

  Future<void> _resolve() async {
    final ssid = await WifiNetworks().currentSsid();
    if (mounted) {
      setState(() => _route = HostConnectionRoute(widget.host, ssid));
    }
  }

  bool _fallback(int attempt) {
    if (!mounted || attempt != _attempt || !_route!.fallbackToPublic()) {
      return false;
    }
    setState(() => _attempt++);
    return true;
  }

  void _connected(int attempt) {
    if (!mounted ||
        attempt != _attempt ||
        !_route!.didFallback ||
        _fallbackNoticeShown) {
      return;
    }
    _fallbackNoticeShown = true;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      showDialog<void>(
        context: context,
        builder: (context) => AlertDialog(
          title: const Text('Connected using public URL'),
          content: const Text(
            'The local URL could not be reached on this Wi-Fi network. You are connected using the public URL.',
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(context).pop(),
              child: const Text('OK'),
            ),
          ],
        ),
      );
    });
  }

  @override
  Widget build(BuildContext context) {
    final route = _route;
    if (route == null) {
      return Scaffold(
        backgroundColor: KlmColors.of(context).surface,
        appBar: AppBar(title: Text(widget.host.name)),
        body: const Center(child: CircularProgressIndicator()),
      );
    }
    final attempt = _attempt;
    return _HostWebViewAttempt(
      key: ValueKey(attempt),
      host: widget.host,
      url: route.url,
      timeout: Duration(seconds: route.usingLocal ? 8 : 25),
      onInitialLoadFailed: () => _fallback(attempt),
      onConnected: () => _connected(attempt),
    );
  }
}

class _HostWebViewAttempt extends StatefulWidget {
  const _HostWebViewAttempt({
    super.key,
    required this.host,
    required this.url,
    required this.timeout,
    required this.onInitialLoadFailed,
    required this.onConnected,
  });
  final SavedHost host;
  final Uri url;
  final Duration timeout;
  final bool Function() onInitialLoadFailed;
  final VoidCallback onConnected;

  @override
  State<_HostWebViewAttempt> createState() => _HostWebViewAttemptState();
}

class _HostWebViewAttemptState extends State<_HostWebViewAttempt>
    with WidgetsBindingObserver {
  late final WebViewController _controller;
  late final HostAudioBridge _audio;
  Timer? _timeout;
  bool _loading = true;
  bool _leaving = false;
  String? _error;
  String _pageUrl = '';
  bool _connected = false;

  @override
  void initState() {
    super.initState();
    _controller = WebViewController();
    _audio = HostAudioBridge(_controller, widget.url.origin);
    WidgetsBinding.instance.addObserver(this);
    _configure();
  }

  Future<void> _configure() async {
    try {
      await _controller.setJavaScriptMode(JavaScriptMode.unrestricted);
      await _controller.addJavaScriptChannel(
        'KlmAudio',
        onMessageReceived: _audio.receive,
      );
      await _controller.addJavaScriptChannel(
        'KlmMobile',
        onMessageReceived: (message) async {
          if (message.message != 'home') return;
          final current = Uri.tryParse(await _controller.currentUrl() ?? '');
          if (mounted && current != null && _belongsToHost(current)) _home();
        },
      );
      await _controller.setNavigationDelegate(
        NavigationDelegate(
          onNavigationRequest: (request) {
            final url = Uri.tryParse(request.url);
            return url != null && ['http', 'https'].contains(url.scheme)
                ? NavigationDecision.navigate
                : NavigationDecision.prevent;
          },
          onPageStarted: (url) {
            if (!mounted) return;
            _audio.invalidate();
            _pageUrl = url;
            setState(() {
              _loading = true;
              _error = null;
            });
            _startTimeout();
          },
          onPageFinished: (url) async {
            if (!mounted || _error != null) return;
            final uri = Uri.tryParse(url);
            try {
              if (uri != null && _belongsToHost(uri)) {
                await _controller.runJavaScript(installHostNavigation);
                if (uri.origin == widget.url.origin) await _audio.install();
              }
            } catch (_) {
              _fail('Could not connect web client navigation. Please retry.');
              return;
            }
            _timeout?.cancel();
            if (mounted && _error == null) {
              setState(() => _loading = false);
              if (!_connected) {
                _connected = true;
                widget.onConnected();
              }
            }
          },
          onWebResourceError: (error) {
            if (error.isForMainFrame == true) {
              _fail('Check the web client URL and your connection.');
            }
          },
          onHttpError: (error) {
            if (error.request?.uri.toString() == _pageUrl) {
              _fail(
                'The web client returned HTTP ${error.response?.statusCode ?? 'error'}. Please retry.',
              );
            }
          },
          onSslAuthError: (error) {
            error.cancel();
            _fail('The web client certificate could not be verified.');
          },
        ),
      );
      if (mounted) await _load();
    } catch (_) {
      _fail('Could not open this web client. Please retry.');
    }
  }

  bool _belongsToHost(Uri uri) =>
      ['http', 'https'].contains(uri.scheme) && uri.host == widget.url.host;

  void _startTimeout() {
    _timeout?.cancel();
    _timeout = Timer(
      widget.timeout,
      () => _fail(
        'The web client took too long to respond. Check your connection and retry.',
      ),
    );
  }

  Future<void> _load() async {
    _connected = false;
    setState(() {
      _loading = true;
      _error = null;
    });
    _startTimeout();
    try {
      await _controller.loadRequest(widget.url);
    } catch (_) {
      _fail('Could not open this web client. Please retry.');
    }
  }

  void _fail(String message) {
    if (!mounted) return;
    _audio.invalidate();
    _timeout?.cancel();
    if (!_connected && widget.onInitialLoadFailed()) return;
    if (mounted) {
      setState(() {
        _error = message;
        _loading = false;
      });
    }
  }

  void _home() {
    if (_leaving || !mounted) return;
    _leaving = true;
    _audio.invalidate();
    Navigator.of(context).pop();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    // Permission dialogs can make the app inactive; only cancel on background.
    if (state == AppLifecycleState.resumed) _audio.setForeground(true);
    if (state == AppLifecycleState.paused ||
        state == AppLifecycleState.detached) {
      _audio.setForeground(false);
    }
  }

  @override
  void dispose() {
    _timeout?.cancel();
    WidgetsBinding.instance.removeObserver(this);
    _audio.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final colors = KlmColors.of(context);
    return Scaffold(
      backgroundColor: colors.surface,
      body: Stack(
        children: [
          SafeArea(child: WebViewWidget(controller: _controller)),
          if (_loading || _error != null)
            KlmPage(
              title: Text(
                widget.host.name,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
              leading: KlmIconButton(
                label: 'Web clients',
                onPressed: _home,
                child: const KlmMark(),
              ),
              child: Center(
                child: SingleChildScrollView(
                  padding: const EdgeInsets.all(32),
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      if (_error == null)
                        const CircularProgressIndicator()
                      else ...[
                        KlmIcon(
                          KlmSymbol.server,
                          size: 32,
                          color: colors.muted,
                        ),
                        const SizedBox(height: 20),
                        const Text(
                          'Web client unavailable',
                          style: TextStyle(
                            fontSize: 18,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                      ],
                      const SizedBox(height: 16),
                      Text(
                        widget.url.toString(),
                        textAlign: TextAlign.center,
                        style: TextStyle(fontSize: 13, color: colors.muted),
                      ),
                      if (_error != null) ...[
                        const SizedBox(height: 24),
                        KlmError(_error!),
                        const SizedBox(height: 24),
                        KlmButton(
                          label: 'Retry',
                          icon: KlmSymbol.retry,
                          onPressed: _load,
                        ),
                      ],
                    ],
                  ),
                ),
              ),
            ),
        ],
      ),
    );
  }
}
