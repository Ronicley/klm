export function captureScrollAnchor(root: HTMLElement, selector = '[data-history-anchor], [data-work-id]') {
  root.style.overflowAnchor = 'none';
  const viewport = root.getBoundingClientRect();
  const candidates = [...root.querySelectorAll<HTMLElement>(selector)];
  const element = candidates.find(item => item.getClientRects().length && !item.querySelector('[data-work-id]') && item.getBoundingClientRect().bottom > viewport.top) ?? candidates.find(item => item.getBoundingClientRect().bottom > viewport.top);
  const attribute = element?.hasAttribute('data-work-id') ? 'data-work-id' : 'data-history-anchor';
  const id = element?.getAttribute(attribute);
  const top = element?.getBoundingClientRect().top;
  const scrollTop = root.scrollTop;
  return () => {
    const target = element?.isConnected ? element : id ? root.querySelector<HTMLElement>(`[${attribute}="${CSS.escape(id)}"]`) : undefined;
    if (target && top !== undefined) root.scrollTop += target.getBoundingClientRect().top - top;
    else root.scrollTop = scrollTop;
  };
}
