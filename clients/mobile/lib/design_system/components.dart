import 'package:flutter/material.dart';
import 'icons.dart';
import 'theme.dart';

class KlmMark extends StatelessWidget {
  const KlmMark({super.key, this.size = 25});
  final double size;

  @override
  Widget build(BuildContext context) => Image.asset(
    'assets/brand/logo.png',
    width: size,
    height: size,
    fit: BoxFit.contain,
    excludeFromSemantics: true,
  );
}

class KlmBrand extends StatelessWidget {
  const KlmBrand({super.key});

  @override
  Widget build(BuildContext context) => Semantics(
    label: 'KLM Harness',
    excludeSemantics: true,
    child: Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        const KlmMark(size: 28),
        const SizedBox(width: 7),
        const Text(
          'KLM',
          style: TextStyle(
            fontWeight: FontWeight.w700,
            letterSpacing: -.6,
            fontSize: 20,
          ),
        ),
        const SizedBox(width: 8),
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 4),
          decoration: BoxDecoration(
            color: KlmColors.of(context).text,
            borderRadius: BorderRadius.circular(4),
          ),
          child: Text(
            'HARNESS',
            style: TextStyle(
              color: KlmColors.of(context).surface,
              fontSize: 9,
              fontWeight: FontWeight.w700,
              letterSpacing: .8,
            ),
          ),
        ),
      ],
    ),
  );
}

class KlmPage extends StatelessWidget {
  const KlmPage({
    super.key,
    required this.title,
    required this.child,
    this.leading,
    this.actions = const [],
  });
  final Widget title, child;
  final Widget? leading;
  final List<Widget> actions;

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(
      automaticallyImplyLeading: false,
      title: title,
      leading: leading,
      leadingWidth: leading == null ? null : 64,
      actions: [...actions, const SizedBox(width: 12)],
    ),
    body: SafeArea(
      top: false,
      child: Align(
        alignment: Alignment.topCenter,
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 640),
          child: child,
        ),
      ),
    ),
  );
}

class KlmIconButton extends StatelessWidget {
  const KlmIconButton({
    super.key,
    required this.label,
    required this.child,
    this.onPressed,
  });
  final String label;
  final Widget child;
  final VoidCallback? onPressed;

  @override
  Widget build(BuildContext context) => IconButton(
    tooltip: label,
    onPressed: onPressed,
    icon: child,
    style: IconButton.styleFrom(
      foregroundColor: KlmColors.of(context).muted,
      minimumSize: const Size(44, 44),
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(8)),
    ),
  );
}

class KlmButton extends StatelessWidget {
  const KlmButton({
    super.key,
    required this.label,
    this.icon,
    this.onPressed,
    this.busy = false,
  });
  final String label;
  final KlmSymbol? icon;
  final VoidCallback? onPressed;
  final bool busy;

  @override
  Widget build(BuildContext context) => FilledButton(
    onPressed: busy ? null : onPressed,
    style: FilledButton.styleFrom(
      minimumSize: const Size(48, 48),
      padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 12),
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(8)),
      textStyle: const TextStyle(
        fontFamily: 'Inter',
        fontSize: 14,
        fontWeight: FontWeight.w500,
      ),
    ),
    child: Row(
      mainAxisSize: MainAxisSize.min,
      mainAxisAlignment: MainAxisAlignment.center,
      children: [
        if (busy) ...[
          const SizedBox.square(
            dimension: 16,
            child: CircularProgressIndicator(strokeWidth: 2),
          ),
          const SizedBox(width: 8),
        ] else if (icon != null) ...[
          KlmIcon(icon!, size: 18, color: KlmColors.of(context).onAccent),
          const SizedBox(width: 8),
        ],
        Text(label),
      ],
    ),
  );
}

class KlmCard extends StatelessWidget {
  const KlmCard({super.key, required this.child, this.onTap});
  final Widget child;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) => Material(
    color: KlmColors.of(context).surface,
    shape: RoundedRectangleBorder(
      borderRadius: BorderRadius.circular(12),
      side: BorderSide(color: KlmColors.of(context).border),
    ),
    clipBehavior: Clip.antiAlias,
    child: InkWell(
      onTap: onTap,
      child: Padding(padding: const EdgeInsets.all(16), child: child),
    ),
  );
}

class KlmField extends StatelessWidget {
  const KlmField({
    super.key,
    required this.label,
    required this.controller,
    this.hint,
    this.validator,
    this.onSubmitted,
    this.keyboardType,
    this.autofocus = false,
    this.enabled = true,
    this.action = TextInputAction.next,
  });
  final String label;
  final String? hint;
  final TextEditingController controller;
  final FormFieldValidator<String>? validator;
  final ValueChanged<String>? onSubmitted;
  final TextInputType? keyboardType;
  final bool autofocus, enabled;
  final TextInputAction action;

  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      Text(
        label,
        style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w500),
      ),
      const SizedBox(height: 8),
      TextFormField(
        controller: controller,
        validator: validator,
        onFieldSubmitted: onSubmitted,
        keyboardType: keyboardType,
        textInputAction: action,
        autofocus: autofocus,
        enabled: enabled,
        autocorrect: false,
        enableSuggestions: false,
        style: const TextStyle(fontSize: 14),
        decoration: InputDecoration(hintText: hint),
      ),
    ],
  );
}

class KlmError extends StatelessWidget {
  const KlmError(this.message, {super.key});
  final String message;

  @override
  Widget build(BuildContext context) => Semantics(
    liveRegion: true,
    child: Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        KlmIcon(KlmSymbol.alert, size: 18, color: KlmColors.of(context).danger),
        const SizedBox(width: 8),
        Expanded(
          child: Text(
            message,
            style: TextStyle(
              color: KlmColors.of(context).danger,
              fontSize: 13,
              height: 1.5,
            ),
          ),
        ),
      ],
    ),
  );
}
