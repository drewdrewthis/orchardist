/// <reference types="@sveltejs/kit" />
/// <reference no-default-lib="true"/>
/// <reference lib="esnext" />
/// <reference lib="webworker" />

import { build, files, version } from "$service-worker";

declare const self: ServiceWorkerGlobalScope;

// Cache name versioned by the build — busts on every deploy.
const CACHE = `orchard-static-${version}`;

// Static build artifacts: SvelteKit hashed bundles + static files.
// Excludes anything dynamic (GraphQL, daemon, transcripts, WebSocket).
const STATIC_ASSETS = [
  ...build,  // _app/immutable/** (hashed JS/CSS)
  ...files,  // files in /static (manifest, icons, fonts served locally)
];

// URLs that must NEVER be intercepted — always go to network.
// The daemon is the source of truth; SW must not proxy or cache responses.
const NETWORK_ONLY_PATTERNS = [
  /\/graphql/,
  /\/__daemon/,
  /\/v1\/conversations/,
];

function isNetworkOnly(url: URL): boolean {
  // WebSocket requests (handled outside fetch, but guard anyway).
  if (url.protocol === "ws:" || url.protocol === "wss:") return true;
  return NETWORK_ONLY_PATTERNS.some((re) => re.test(url.pathname));
}

// ── Install: pre-cache all static assets ────────────────────────────────────
self.addEventListener("install", (event) => {
  event.waitUntil(
    caches
      .open(CACHE)
      .then((cache) => cache.addAll(STATIC_ASSETS))
      .then(() => self.skipWaiting()),
  );
});

// ── Activate: evict caches from previous deploys ────────────────────────────
self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) =>
        Promise.all(
          keys
            .filter((key) => key !== CACHE)
            .map((key) => caches.delete(key)),
        ),
      )
      .then(() => self.clients.claim()),
  );
});

// ── Push: show a notification from the daemon's Web Push payload ────────────
//
// Payload shape (JSON):
//   { title: string, body: string, sessionUuid: string }
//
// The daemon sends this when an assistant turn arrives for a subscribed session.
// The SW must NOT touch GraphQL or cache here — just show the notification.
// coalesces by sessionUuid via `tag` so a fast-responding session doesn't stack.
self.addEventListener("push", (event: PushEvent) => {
  if (!event.data) return;

  let title = "Claude responded";
  let body = "";
  let tag = "orchard";

  try {
    const payload = event.data.json() as { title?: string; body?: string; sessionUuid?: string };
    if (payload.title) title = payload.title;
    if (payload.body) body = payload.body.slice(0, 120);
    if (payload.sessionUuid) tag = payload.sessionUuid;
  } catch {
    body = event.data.text().slice(0, 120);
  }

  event.waitUntil(
    self.registration.showNotification(title, {
      body,
      icon: "/icon-192.png",
      tag,
      renotify: true,
    }),
  );
});

// ── Notification click: focus the app tab ────────────────────────────────────
self.addEventListener("notificationclick", (event: NotificationEvent) => {
  event.notification.close();
  event.waitUntil(
    self.clients
      .matchAll({ type: "window", includeUncontrolled: true })
      .then((clientList) => {
        // Focus any already-open tab.
        for (const client of clientList) {
          if ("focus" in client) return (client as WindowClient).focus();
        }
        // No open tab — open the root URL.
        return self.clients.openWindow("/");
      }),
  );
});

// ── Fetch: cache-first for static assets; network-only for everything else ──
self.addEventListener("fetch", (event) => {
  const { request } = event;

  // Only handle GET — POST/PUT/DELETE (GraphQL mutations, etc.) always network.
  if (request.method !== "GET") return;

  const url = new URL(request.url);

  // Hard pass-through: daemon, GraphQL, transcripts, WebSocket.
  if (isNetworkOnly(url)) return;

  // Cache-first for static assets only.
  event.respondWith(
    caches.match(request).then((cached) => {
      if (cached) return cached;
      // Asset not in cache (e.g. font from Google Fonts CDN) — fetch and cache.
      return fetch(request).then((response) => {
        // Only cache valid responses from safe origins.
        if (
          !response.ok ||
          response.type === "opaque" ||
          !(
            url.origin === self.location.origin ||
            url.hostname === "fonts.gstatic.com" ||
            url.hostname === "fonts.googleapis.com"
          )
        ) {
          return response;
        }
        const clone = response.clone();
        caches.open(CACHE).then((cache) => cache.put(request, clone));
        return response;
      });
    }),
  );
});
