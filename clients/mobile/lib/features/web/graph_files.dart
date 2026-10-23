import 'dart:convert';
import 'dart:typed_data';
import 'package:file_picker/file_picker.dart';

// The host page requests a picker; it never supplies a filesystem path.
Future<Map<String, dynamic>> graphFileRequest(
  Map<String, dynamic> request,
) async {
  if (request['action'] == 'open') {
    final file = await FilePicker.pickFile(
      dialogTitle: 'Import graph',
      type: FileType.custom,
      allowedExtensions: ['json'],
    );
    if (file == null) return {'cancelled': true};
    final bytes = BytesBuilder(copy: false);
    await for (final chunk in file.readAsByteStream()) {
      if (bytes.length + chunk.length > 512 * 1024) {
        throw const FormatException('Graph JSON exceeds 512 KiB.');
      }
      bytes.add(chunk);
    }
    return {'content': utf8.decode(bytes.takeBytes())};
  }
  if (request['action'] != 'save') {
    throw const FormatException('Unknown file action.');
  }
  final name = request['name'];
  final content = request['content'];
  if (name is! String ||
      content is! String ||
      name.length > 240 ||
      name.contains(RegExp(r'[/\\:]')) ||
      !(name.endsWith('.json') || name.endsWith('.svg'))) {
    throw const FormatException('Use a JSON or SVG filename.');
  }
  final bytes = Uint8List.fromList(utf8.encode(content));
  if (bytes.length > 2 * 1024 * 1024) {
    throw const FormatException('Graph file exceeds 2 MiB.');
  }
  final result = await FilePicker.saveFile(
    dialogTitle: 'Export graph',
    fileName: name,
    bytes: bytes,
    mimeType: name.endsWith('.json') ? 'application/json' : 'image/svg+xml',
  );
  return {'cancelled': result == null};
}
