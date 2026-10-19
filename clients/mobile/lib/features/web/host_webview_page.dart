import 'dart:async';
import 'package:flutter/material.dart';
import 'package:webview_flutter/webview_flutter.dart';
import '../../design_system/components.dart';
import '../../design_system/icons.dart';
import '../../design_system/theme.dart';
import '../hosts/host_store.dart';
import 'host_audio_bridge.dart';
import 'host_navigation.dart';

class HostWebViewPage extends StatefulWidget {
  const HostWebViewPage({super.key, required this.host});
  final SavedHost host;

  @override
  State<HostWebViewPage> createState() => _HostWebViewPageState();
}

class _HostWebViewPageState extends State<HostWebViewPage> with WidgetsBindingObserver {
  late final WebViewController _controller;
  late final HostAudioBridge _audio;
  Timer? _timeout;
  bool _loading = true;
  bool _leaving = false;
  String? _error;
  String _pageUrl = '';

  @override
  void initState() {
    super.initState();
    _controller = WebViewController();
    _audio = HostAudioBridge(_controller, widget.host.url.origin);
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
            _audio.invalidate();
            if (!mounted) return;
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
                if (uri.origin == widget.host.url.origin) await _audio.install();
              }
            } catch (_) {
              _fail('Could not connect web client navigation. Please retry.');
              return;
            }
            _timeout?.cancel();
            if (mounted && _error == null) setState(() => _loading = false);
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
      ['http', 'https'].contains(uri.scheme) &&
      uri.host == widget.host.url.host;

  void _startTimeout() {
    _timeout?.cancel();
    _timeout = Timer(
      const Duration(seconds: 25),
      () => _fail(
        'The web client took too long to respond. Check your connection and retry.',
      ),
    );
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    _startTimeout();
    try {
      await _controller.loadRequest(widget.host.url);
    } catch (_) {
      _fail('Could not open this web client. Please retry.');
    }
  }

  void _fail(String message) {
    _audio.invalidate();
    _timeout?.cancel();
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
    if (state == AppLifecycleState.paused || state == AppLifecycleState.detached) {
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
                        widget.host.url.toString(),
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
