import { useId, useLayoutEffect, useRef, useState, type ButtonHTMLAttributes, type ComponentProps, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { Button } from './Button';

const focusableSelector = 'button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), a[href], [tabindex="0"]';

export function Menu({ label, open, onOpenChange, trigger, children, className = '', side = 'top', align = 'end', position, role = 'dialog' }: {
  label: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  trigger: (props: ButtonHTMLAttributes<HTMLButtonElement>) => ReactNode;
  children: ReactNode;
  className?: string;
  side?: 'top' | 'bottom';
  align?: 'start' | 'center' | 'end';
  position?: { x: number; y: number };
  role?: 'dialog' | 'menu';
}) {
  const id = useId();
  const anchor = useRef<HTMLSpanElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const [portalTarget, setPortalTarget] = useState<HTMLElement | null>(null);
  const change = useRef(onOpenChange);
  change.current = onOpenChange;

  useLayoutEffect(() => {
    if (open) setPortalTarget(anchor.current?.closest<HTMLDialogElement>('dialog[open]') ?? document.body);
  }, [open]);

  useLayoutEffect(() => {
    if (!open) return;
    const element = panel.current;
    const reference = anchor.current;
    if (!element || !reference) return;
    const triggerButton = reference.querySelector('button');
    const place = () => {
      const rect = reference.getBoundingClientRect();
      const bounds = element.getBoundingClientRect();
      const gap = 6;
      const above = rect.top - bounds.height - gap;
      const below = rect.bottom + gap;
      const preferAbove = side === 'top' ? above >= 8 || below + bounds.height > innerHeight - 8 : below + bounds.height > innerHeight - 8 && above >= 8;
      const left = align === 'center' ? rect.left + (rect.width - bounds.width) / 2 : align === 'start' ? rect.left : rect.right - bounds.width;
      // Centered rail popovers may use the viewport edge to stay over their trigger.
      const horizontalInset = align === 'center' ? 0 : 8;
      element.style.left = `${Math.max(horizontalInset, Math.min(position?.x ?? left, innerWidth - bounds.width - horizontalInset))}px`;
      element.style.top = `${Math.max(8, Math.min(position?.y ?? (preferAbove ? above : below), innerHeight - bounds.height - 8))}px`;
      element.style.visibility = 'visible';
    };
    place();
    (element.querySelector<HTMLElement>('[data-menu-autofocus]') ?? element.querySelector<HTMLElement>(focusableSelector) ?? element).focus();
    const observer = new ResizeObserver(place);
    observer.observe(element); observer.observe(reference);
    window.addEventListener('resize', place);
    window.addEventListener('scroll', place, true);

    const outside = (event: PointerEvent) => {
      if (event.target instanceof Node && !element.contains(event.target) && !reference.contains(event.target)) change.current(false);
    };
    const keyboard = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault(); event.stopPropagation();
        change.current(false); triggerButton?.focus();
      } else if (role === 'menu' && element.contains(document.activeElement) && ['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
        event.preventDefault();
        const items = [...element.querySelectorAll<HTMLElement>('[role="menuitem"]:not(:disabled)')];
        const index = items.indexOf(document.activeElement as HTMLElement);
        const next = event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length;
        items[next]?.focus();
      } else if (event.key === 'Tab' && element.contains(document.activeElement)) {
        const items = [...element.querySelectorAll<HTMLElement>(focusableSelector)].filter(item => item.getClientRects().length > 0);
        const atBoundary = event.shiftKey ? document.activeElement === items[0] : document.activeElement === items.at(-1);
        if (!atBoundary) return;
        event.preventDefault(); change.current(false);
        if (event.shiftKey) { triggerButton?.focus(); return; }
        const scope = element.closest('dialog[open]') ?? document;
        const outsideItems = [...scope.querySelectorAll<HTMLElement>(focusableSelector)].filter(item => !element.contains(item) && !item.closest('[inert]') && item.getClientRects().length > 0);
        const index = outsideItems.indexOf(triggerButton as HTMLElement);
        (outsideItems[index + 1] ?? triggerButton)?.focus();
      }
    };
    document.addEventListener('pointerdown', outside, true);
    document.addEventListener('keydown', keyboard, true);
    return () => {
      observer.disconnect();
      window.removeEventListener('resize', place);
      window.removeEventListener('scroll', place, true);
      document.removeEventListener('pointerdown', outside, true);
      document.removeEventListener('keydown', keyboard, true);
      if (element.contains(document.activeElement)) triggerButton?.focus();
    };
  }, [open, side, align, position?.x, position?.y, role, portalTarget]);

  return <>
    <span ref={anchor} className="menu-anchor">{trigger({
      type: 'button', 'aria-haspopup': role, 'aria-expanded': open, 'aria-controls': open ? id : undefined,
      onClick: () => onOpenChange(!open),
    })}</span>
    {open && portalTarget && createPortal(<div ref={panel} id={id} role={role} aria-label={label} tabIndex={-1} className={`menu ${className}`} style={{ visibility: 'hidden' }}>
      {children}
    </div>, portalTarget)}
  </>;
}

export function MenuItem({ selected, className = '', ...props }: ComponentProps<typeof Button> & { selected?: boolean }) {
  return <Button variant="ghost" size="sm" className={`menu-item ${className}`} aria-pressed={selected} {...props} />;
}
