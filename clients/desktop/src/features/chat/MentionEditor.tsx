import { forwardRef, useEffect, useImperativeHandle, useLayoutEffect, useRef, type HTMLAttributes } from 'react';
import type { FileMention, ProjectPath } from '../../engine';
import type { ComposerDraft } from './MessageComposer';
import { createMentionBadge } from './MentionBadge';
import { mentionToken, type TextSelection } from './mentions';
import { readMentionEditor, selectMentionEditor } from './mentionEditorDom';

export type MentionEditorHandle = {
  focus: () => void;
  insertText: (text: string) => void;
  insertMention: (path: ProjectPath, range: TextSelection) => void;
};

type Props = Omit<HTMLAttributes<HTMLDivElement>, 'onChange'> & {
  draft: ComposerDraft;
  onChange: (draft: ComposerDraft) => void;
  onCaretChange: (caret: number) => void;
  placeholder: string;
  readOnly: boolean;
};

export const MentionEditor = forwardRef<MentionEditorHandle, Props>(function MentionEditor({ draft, onChange, onCaretChange, placeholder, readOnly, ...props }, ref) {
  const root = useRef<HTMLDivElement>(null);
  // Retain selected identities for native undo. Pasted HTML never enters the editor.
  const known = useRef(new Map<string, FileMention>());
  const caretCallback = useRef(onCaretChange);
  caretCallback.current = onCaretChange;
  for (const mention of draft.mentions) known.current.set(mention.id, mention);

  function changed() {
    if (!root.current) return;
    const snapshot = readMentionEditor(root.current, known.current);
    onChange({ ...draft, text: snapshot.text, mentions: snapshot.mentions });
    onCaretChange(snapshot.range?.start === snapshot.range?.end ? snapshot.range?.start ?? -1 : -1);
  }
  function insertText(text: string) {
    root.current?.focus();
    // insertText escapes clipboard content and preserves the browser's undo stack.
    document.execCommand('insertText', false, text.replace(/\r\n?/g, '\n'));
    changed();
  }
  useImperativeHandle(ref, () => ({
    focus: () => root.current?.focus(),
    insertText,
    insertMention: (path, selection) => {
      const editor = root.current;
      if (!editor) return;
      const mention: FileMention = { ...path, id: crypto.randomUUID(), start: selection.start, end: selection.start + mentionToken(path).length };
      known.current.set(mention.id, mention);
      editor.focus();
      selectMentionEditor(editor, known.current, selection.start, selection.end);
      // Only locally-created, textContent-escaped badge HTML is inserted here.
      document.execCommand('insertHTML', false, createMentionBadge(mention).outerHTML + ' ');
      selectMentionEditor(editor, known.current, mention.end + 1);
      changed();
    },
  }));

  useLayoutEffect(() => {
    const editor = root.current;
    if (!editor) return;
    const current = readMentionEditor(editor, known.current);
    if (current.text === draft.text && JSON.stringify(current.mentions) === JSON.stringify(draft.mentions)) return;
    const fragment = document.createDocumentFragment();
    let offset = 0;
    for (const mention of [...draft.mentions].sort((a, b) => a.start - b.start)) {
      if (mention.start < offset || draft.text.slice(mention.start, mention.end) !== mentionToken(mention)) continue;
      fragment.append(document.createTextNode(draft.text.slice(offset, mention.start)), createMentionBadge(mention));
      offset = mention.end;
    }
    fragment.append(document.createTextNode(draft.text.slice(offset)));
    editor.replaceChildren(fragment);
    if (document.activeElement === editor && current.range) {
      selectMentionEditor(editor, known.current, Math.min(current.range.start, draft.text.length), Math.min(current.range.end, draft.text.length));
    }
  }, [draft]);
  useEffect(() => {
    const updateCaret = () => {
      const editor = root.current;
      if (!editor || document.activeElement !== editor) return;
      const { range } = readMentionEditor(editor, known.current);
      caretCallback.current(range && range.start === range.end ? range.start : -1);
    };
    document.addEventListener('selectionchange', updateCaret);
    return () => document.removeEventListener('selectionchange', updateCaret);
  }, []);

  return <div {...props} ref={root} className="composer-editor" role="textbox" aria-multiline="true" aria-readonly={readOnly}
    contentEditable={!readOnly} suppressContentEditableWarning data-placeholder={placeholder} data-empty={!draft.text || undefined}
    onInput={changed} onPaste={event => { event.preventDefault(); if (!readOnly) insertText(event.clipboardData.getData('text/plain')); }}
    onDrop={event => event.preventDefault()} />;
});
