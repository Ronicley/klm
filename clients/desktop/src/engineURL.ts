// Pure URL policy shared by endpoint resolution, registration and pairing.
// This leaf module must not import platform/authentication or read storage.
export function normalizeEngineURL(value: string) {
  let address = value.trim();
  const hasScheme = /^[a-z][a-z\d+.-]*:\/\//i.test(address);
  if (!hasScheme) {
    // A bare IPv6 literal needs brackets before URL parsing.
    if (/^[\da-f:]+$/i.test(address) && address.includes("::")) address = "[" + address + "]";
    const candidate = new URL("http://" + address);
    const ip = candidate.hostname.startsWith("[") || /^\d+\.\d+\.\d+\.\d+$/.test(candidate.hostname);
    address = (ip ? "http://" : "https://") + address;
  }
  const url = new URL(address);
  if (url.username || url.password) throw new Error("The engine URL cannot include credentials.");
  const ip = url.hostname.startsWith("[") || /^\d+\.\d+\.\d+\.\d+$/.test(url.hostname);
  // URL.port omits explicit :80/:443, so inspect the original authority.
  const authority = address.slice(address.indexOf("://") + 3).split(/[/?#]/, 1)[0];
  // Bare IP input uses the engine port; absolute URLs retain standard default
  // ports so re-normalizing a canonical URL is idempotent (including :80/:443).
  if (ip && !hasScheme && !/:\d+$/.test(authority)) url.port = "7331";
  if (url.protocol !== 'http:' && url.protocol !== 'https:') throw new Error('Use an HTTP or HTTPS URL.');
  const loopback = url.hostname === 'localhost' || url.hostname === '127.0.0.1' || url.hostname === '[::1]';
  if (url.protocol !== 'https:' && !loopback) throw new Error('Use HTTPS for remote engines.');
  if (url.search || url.hash) throw new Error('The engine URL cannot include a query or fragment.');
  return url.toString().replace(/\/+$/, '');
}
