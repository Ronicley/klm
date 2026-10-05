/// Compatibility for already-installed frontends whose rail logo is still a div.
/// New frontends use a footer button and reserve the logo for the general agent.
const installHostNavigation = r'''
(() => {
  if (window.__klmHostNavigationInstalled) return;
  window.__klmHostNavigationInstalled = true;
  const handle = event => {
    const brand = event.target instanceof Element ? event.target.closest('.rail-brand') : null;
    if (!brand || brand.hasAttribute('data-klm-host-navigation') || brand.hasAttribute('data-klm-general-navigation')) return;
    if (event.type === 'keydown' && event.key !== 'Enter' && event.key !== ' ') return;
    event.preventDefault();
    event.stopImmediatePropagation();
    window.KlmMobile.postMessage('home');
  };
  document.addEventListener('click', handle, true);
  document.addEventListener('keydown', handle, true);
  const decorate = () => {
    const brand = document.querySelector('.rail-brand');
    if (!brand) return false;
    if (!brand.hasAttribute('data-klm-host-navigation') && !brand.hasAttribute('data-klm-general-navigation')) {
      brand.setAttribute('role', 'button');
      brand.setAttribute('aria-label', 'Web clients');
      brand.setAttribute('title', 'Web clients');
      brand.setAttribute('tabindex', '0');
      brand.style.cursor = 'pointer';
    }
    return true;
  };
  if (!decorate()) {
    const observer = new MutationObserver(() => { if (decorate()) observer.disconnect(); });
    observer.observe(document.documentElement, {childList: true, subtree: true});
  }
})();
''';
