import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'package:path_provider/path_provider.dart';
import 'package:record/record.dart';
import 'package:webview_flutter/webview_flutter.dart';

/// Only the selected frontend origin receives the per-document capability.
/// Keep it in a main-frame closure: JS channels alone do not identify callers.
class HostAudioBridge {
  HostAudioBridge(this.controller, this.origin);

  final WebViewController controller;
  final String origin;
  final AudioRecorder _recorder = AudioRecorder();
  Future<void> _queue = Future.value();
  StreamSubscription<RecordState>? _stateSubscription;
  Timer? _limit;
  String? _token;
  String? _wantedId;
  File? _file;
  bool _disposed = false;
  bool _foreground = true;
  bool _recording = false;

  Future<void> install() async {
    if (_disposed) return;
    final token = base64UrlEncode(
      List<int>.generate(32, (_) => Random.secure().nextInt(256)),
    );
    _token = token;
    await controller.runJavaScript('''
      (() => {
        if (window.top !== window || location.origin !== ${jsonEncode(origin)}) return;
        const token = ${jsonEncode(token)};
        window.KlmNativeAudio = Object.freeze({
          postMessage: (action, id) => window.KlmAudio.postMessage(JSON.stringify({token, action, id}))
        });
      })();
    ''');
  }

  void receive(JavaScriptMessage message) {
    if (_disposed || _token == null) return;
    dynamic input;
    try {
      input = jsonDecode(message.message);
    } catch (_) {
      return;
    }
    if (input is! Map || input['token'] != _token) return;
    final id = input['id'];
    final action = input['action'];
    if (id is! String || !RegExp(r'^[a-zA-Z0-9-]{1,100}$').hasMatch(id)) return;
    if (!['start', 'stop', 'cancel'].contains(action)) return;
    final token = _token!;
    if (action == 'cancel') {
      if (_wantedId != id) return;
      _wantedId = null; // Invalidate even while a permission dialog is open.
    } else if (action == 'start') {
      if (_wantedId != null || !_foreground) {
        unawaited(_reply(token, id, 'error', error: 'Microphone is unavailable. Retry.'));
        return;
      }
      _wantedId = id;
    } else if (_wantedId != id) {
      return;
    }
    _enqueue(() async {
      try {
        if (action == 'cancel') {
          await _cleanup();
        } else if (_valid(token, id)) {
          if (action == 'start') {
            await _start(token, id);
          } else {
            await _stop(token, id);
          }
        }
      } catch (_) {
        if (_wantedId == id) _wantedId = null;
        await _cleanup();
        await _reply(token, id, 'error', error: 'Audio recording failed. Retry.');
      }
    });
  }

  bool _valid(String token, String id) =>
      !_disposed && _foreground && _token == token && _wantedId == id;

  void _enqueue(Future<void> Function() operation) {
    // Recorder calls are serialized, including cancellation during permission/start.
    _queue = _queue.then((_) => operation()).catchError((Object _) {});
  }

  Future<void> _start(String token, String id) async {
    final current = Uri.tryParse(await controller.currentUrl() ?? '');
    if (current == null || !current.hasAuthority || current.origin != origin || !_valid(token, id)) return;
    if (!await _recorder.hasPermission()) {
      if (_wantedId == id) _wantedId = null;
      await _reply(token, id, 'error', error: 'Microphone access was denied. Enable it in your device settings and retry.');
      return;
    }
    if (!_valid(token, id)) return;
    final directory = await getTemporaryDirectory();
    if (!_valid(token, id)) return;
    _file = File('${directory.path}/klm-audio-$id.m4a');
    await _recorder.start(
      const RecordConfig(
        encoder: AudioEncoder.aacLc,
        bitRate: 64000,
        sampleRate: 44100,
        numChannels: 1,
        audioInterruption: AudioInterruptionMode.pause,
      ),
      path: _file!.path,
    );
    if (!_valid(token, id)) {
      await _cleanup();
      return;
    }
    _recording = true;
    _stateSubscription = _recorder.onStateChanged().listen((state) {
      if (_recording && state != RecordState.record) {
        cancel('Audio recording was interrupted. Retry.');
      }
    }, onError: (Object _) => cancel('Audio recording failed. Retry.'));
    // AAC at 64 kbit/s stays well below the engine's 25 MiB upload limit.
    _limit = Timer(const Duration(minutes: 10), () {
      cancel('Recording reached the 10-minute limit. Record a shorter message.');
    });
    await _reply(token, id, 'started');
  }

  Future<void> _stop(String token, String id) async {
    _recording = false;
    _limit?.cancel();
    await _stateSubscription?.cancel();
    _stateSubscription = null;
    await _recorder.stop();
    final file = _file;
    if (file == null || !await file.exists() || await file.length() == 0) {
      throw StateError('No audio');
    }
    if (await file.length() > 25 * 1024 * 1024) {
      throw StateError('Audio is too large');
    }
    // Small messages avoid WebView/Android IPC limits for longer recordings.
    await for (final bytes in file.openRead().transform(_audioChunks())) {
      if (!_valid(token, id)) break;
      await _reply(token, id, 'data', data: base64Encode(bytes));
    }
    if (_valid(token, id)) await _reply(token, id, 'complete');
    if (_wantedId == id) _wantedId = null;
    await _cleanup();
  }

  StreamTransformer<List<int>, List<int>> _audioChunks() =>
      StreamTransformer.fromHandlers(handleData: (bytes, sink) {
        for (var offset = 0; offset < bytes.length; offset += 48 * 1024) {
          sink.add(bytes.sublist(offset, min(offset + 48 * 1024, bytes.length)));
        }
      });

  Future<void> _reply(String token, String id, String status, {String? data, String? error}) async {
    if (_disposed || _token != token) return;
    try {
      final payload = jsonEncode({'id': id, 'status': status, 'data': data, 'error': error});
      await controller.runJavaScript('''
        if (location.origin === ${jsonEncode(origin)}) {
          window.dispatchEvent(new CustomEvent('klm-audio-response', {detail: $payload}));
        }
      ''');
    } catch (_) {
      cancel();
    }
  }

  void setForeground(bool foreground) {
    _foreground = foreground;
    if (!foreground) cancel('Audio recording was cancelled when the app left the foreground.');
  }

  void cancel([String? error]) {
    final id = _wantedId;
    final token = _token;
    _wantedId = null;
    _recording = false;
    _limit?.cancel();
    if (id != null && token != null && error != null) {
      unawaited(_reply(token, id, 'error', error: error));
    }
    _enqueue(_cleanup);
  }

  void invalidate() {
    _token = null;
    cancel();
  }

  Future<void> _cleanup() async {
    _recording = false;
    _limit?.cancel();
    await _stateSubscription?.cancel();
    _stateSubscription = null;
    try {
      await _recorder.cancel();
    } catch (_) {
      // A denied permission/start can leave no native recorder to cancel.
    } finally {
      final file = _file;
      _file = null;
      if (file != null && await file.exists()) await file.delete();
    }
  }

  void dispose() {
    _disposed = true;
    invalidate();
    _enqueue(() => _recorder.dispose());
  }
}
