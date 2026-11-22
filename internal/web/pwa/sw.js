// Paperlens service worker: caches only the app shell so the installed app
// opens to an offline notice instead of a browser error. Documents,
// thumbnails, previews, downloads, search results and the home page (which
// carries a CSRF token and archive vocabulary) are never stored.
const VERSION = "__PAPERLENS_VERSION__";
const SHELL = ["__PAPERLENS_SHELL__"];
const CACHE = "paperlens-" + VERSION;
const OFFLINE = "/offline";
const SHELL_PATHS = new Set(SHELL);

self.addEventListener("install", event => {
  event.waitUntil(
    caches.open(CACHE)
      .then(cache => cache.addAll([...SHELL, OFFLINE].map(url => new Request(url, { cache: "no-store" }))))
      .then(() => self.skipWaiting())
  );
});

self.addEventListener("activate", event => {
  event.waitUntil(
    caches.keys()
      .then(keys => Promise.all(keys
        .filter(key => key.startsWith("paperlens-") && key !== CACHE)
        .map(key => caches.delete(key))))
      .then(() => self.clients.claim())
  );
});

self.addEventListener("fetch", event => {
  const request = event.request;
  if (request.method !== "GET") return;
  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;
  if (/^\/documents\/[^/]+\/(preview|download|thumbnail)$/.test(url.pathname)) return;

  // Top-level pages only; iframes (inline PDF preview) go straight to the network.
  if (request.mode === "navigate" && request.destination === "document") {
    event.respondWith(fetch(request).catch(async () => {
      const offline = await caches.match(OFFLINE, { cacheName: CACHE });
      return offline || Response.error();
    }));
    return;
  }

  if (SHELL_PATHS.has(url.pathname) && url.search === "") {
    event.respondWith(fetch(request).catch(async () => {
      const cached = await caches.match(url.pathname, { cacheName: CACHE });
      return cached || Response.error();
    }));
  }
});
