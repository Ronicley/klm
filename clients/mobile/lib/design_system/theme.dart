import 'package:flutter/material.dart';

/// Mirrors clients/desktop/src/design-system/tokens.css.
class KlmColors {
  const KlmColors({
    required this.surface,
    required this.sidebar,
    required this.subtle,
    required this.border,
    required this.text,
    required this.heading,
    required this.muted,
    required this.faint,
    required this.accent,
    required this.accentSoft,
    required this.onAccent,
    required this.danger,
  });

  final Color surface, sidebar, subtle, border, text, heading;
  final Color muted, faint, accent, accentSoft, onAccent, danger;

  static const light = KlmColors(
    surface: Color(0xffffffff),
    sidebar: Color(0xfff8f9fa),
    subtle: Color(0xfff3f4f6),
    border: Color(0xffe5e7eb),
    text: Color(0xff1f2937),
    heading: Color(0xff111827),
    muted: Color(0xff687180),
    faint: Color(0xff9299a5),
    accent: Color(0xff2563eb),
    accentSoft: Color(0xffdbeafe),
    onAccent: Color(0xffffffff),
    danger: Color(0xffdc2626),
  );
  static const dark = KlmColors(
    surface: Color(0xff101111),
    sidebar: Color(0xff080808),
    subtle: Color(0xff191a1a),
    border: Color(0xff292a2a),
    text: Color(0xffe3e6e4),
    heading: Color(0xfff5f6f5),
    muted: Color(0xffa6aaa9),
    faint: Color(0xff949997),
    accent: Color(0xff91adff),
    accentSoft: Color(0xff182238),
    onAccent: Color(0xff080808),
    danger: Color(0xfff0939e),
  );

  static KlmColors of(BuildContext context) =>
      Theme.of(context).brightness == Brightness.dark ? dark : light;
}

abstract final class KlmSpace {
  static const double xs = 4, sm = 8, md = 12, lg = 16, xl = 24, xxl = 32;
}

abstract final class KlmTheme {
  static final light = _build(Brightness.light, KlmColors.light);
  static final dark = _build(Brightness.dark, KlmColors.dark);

  static ThemeData _build(Brightness brightness, KlmColors colors) {
    final base = ThemeData(
      useMaterial3: true,
      brightness: brightness,
      fontFamily: 'Inter',
      colorScheme: ColorScheme.fromSeed(
        seedColor: colors.accent,
        brightness: brightness,
        primary: colors.accent,
        onPrimary: colors.onAccent,
        surface: colors.surface,
        onSurface: colors.text,
        error: colors.danger,
      ),
    );
    return base.copyWith(
      scaffoldBackgroundColor: colors.sidebar,
      textTheme: base.textTheme.apply(
        bodyColor: colors.text,
        displayColor: colors.heading,
      ),
      appBarTheme: AppBarTheme(
        backgroundColor: colors.surface,
        foregroundColor: colors.heading,
        surfaceTintColor: Colors.transparent,
        elevation: 0,
        scrolledUnderElevation: 0,
        toolbarHeight: 72,
        titleSpacing: KlmSpace.xl,
        titleTextStyle: TextStyle(
          fontFamily: 'Inter',
          fontSize: 18,
          fontWeight: FontWeight.w600,
          color: colors.heading,
        ),
        shape: Border(bottom: BorderSide(color: colors.border)),
      ),
      inputDecorationTheme: InputDecorationTheme(
        filled: true,
        fillColor: colors.surface,
        contentPadding: const EdgeInsets.symmetric(
          horizontal: 14,
          vertical: 15,
        ),
        hintStyle: TextStyle(color: colors.faint, fontSize: 14),
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(8),
          borderSide: BorderSide(color: colors.border),
        ),
        enabledBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(8),
          borderSide: BorderSide(color: colors.border),
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(8),
          borderSide: BorderSide(color: colors.accent, width: 1.5),
        ),
        errorMaxLines: 3,
      ),
      textSelectionTheme: TextSelectionThemeData(
        cursorColor: colors.accent,
        selectionColor: colors.accentSoft,
      ),
      dividerColor: colors.border,
      progressIndicatorTheme: ProgressIndicatorThemeData(color: colors.accent),
    );
  }
}
