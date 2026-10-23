import { useEffect, useId, useRef, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { X } from 'lucide-react';
import { IconButton } from './Button';

export function BottomSheet({ title, children, onClose, className = '' }: {
  title: string; children: ReactNode; onClose: () => void; className?: string;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const returnFocus = useRef<HTMLElement | null>(null);
  const titleId = useId();
  useEffect(() => {
    const element = dialog.current;
    if (!returnFocus.current) returnFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    if (element && !element.open) element.showModal();
    return () => { if (!element?.isConnected && returnFocus.current?.isConnected) returnFocus.current.focus(); };
  }, []);

  return createPortal(<dialog ref={dialog} className={`bottom-sheet ${className}`} aria-labelledby={titleId}
    onCancel={event => { event.preventDefault(); event.stopPropagation(); onClose(); }} onClose={event => { event.stopPropagation(); onClose(); }}
    onClick={event => {
      if (event.target !== event.currentTarget) return;
      const bounds = event.currentTarget.getBoundingClientRect();
      if (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom) onClose();
    }}>
    <div className="bottom-sheet-handle" aria-hidden="true" />
    <header className="bottom-sheet-heading"><h2 id={titleId}>{title}</h2><IconButton label={`Close ${title.toLowerCase()}`} onClick={onClose}><X /></IconButton></header>
    <div className="bottom-sheet-content">{children}</div>
  </dialog>, document.body);
}
