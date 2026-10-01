/**
 * LombokDNSProxy — Firefox MV2 Background Script
 * Adapts the MV3 background.ts service worker to Firefox's persistent
 * background page model. Key differences from MV3:
 *   - No service worker; runs as a persistent page in Firefox
 *   - Uses webRequest (blocking) instead of declarativeNetRequest
 *   - browser_action instead of action
 *   - WebRequest blocking API for real-time request interception
 *
 * Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
 */

// Presence of `export {}` marks this file as a module so its top-level
// declarations don't collide with ../background.ts when both are
// type-checked together — at runtime only one of the two ever loads,
// depending on the target browser.
export {};

// Firefox provides `browser` (Promise-based); Chrome uses `chrome` (callback-based).
// The webextension-polyfill normalises them at runtime. @types/chrome is the
// only types package installed, so type positions below reference `chrome.*`
// even where the runtime call is `browser.*` — the shapes are identical.
declare const browser: typeof chrome;

const DAEMON_WS_URL = "ws://127.0.0.1:7788/ext";
const RECONNECT_DELAY_BASE_MS = 1000;
const HEARTBEAT_MS = 15000;

// ─── Daemon WebSocket connection ──────────────────────────────────────────────

let ws: WebSocket | null = null;
let reconnectDelay = RECONNECT_DELAY_BASE_MS;
let heartbeatTimer: ReturnType<typeof setInterval> | null = null;

function connect() {
  try {
    ws = new WebSocket(DAEMON_WS_URL);
  } catch {
    scheduleReconnect();
    return;
  }

  ws.onopen = () => {
    console.log("[LombokDNSProxy] Firefox: connected to daemon");
    reconnectDelay = RECONNECT_DELAY_BASE_MS;
    heartbeatTimer = setInterval(() => ws?.send(JSON.stringify({ type: "ping" })), HEARTBEAT_MS);
    setIcon("active");
  };

  ws.onclose = () => {
    if (heartbeatTimer) { clearInterval(heartbeatTimer); heartbeatTimer = null; }
    setIcon("inactive");
    scheduleReconnect();
  };

  ws.onerror = () => ws?.close();

  ws.onmessage = (event: MessageEvent) => {
    try {
      const msg = JSON.parse(event.data as string);
      handleDaemonMessage(msg);
    } catch { /* ignore */ }
  };
}

function scheduleReconnect() {
  setTimeout(() => {
    reconnectDelay = Math.min(reconnectDelay * 2, 30_000); // exponential backoff, cap 30s
    connect();
  }, reconnectDelay);
}

function sendToDaemon(msg: object) {
  if (ws?.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify(msg));
  }
}

// ─── Daemon → Extension message dispatch ─────────────────────────────────────

const tabBlockedCount: Map<number, number> = new Map();

function handleDaemonMessage(msg: { type: string; payload?: unknown }) {
  switch (msg.type) {
    case "query_log": {
      const entry = msg.payload as { domain: string; blocked: boolean; tabId: number };
      if (entry.blocked && entry.tabId > 0) {
        const count = (tabBlockedCount.get(entry.tabId) ?? 0) + 1;
        tabBlockedCount.set(entry.tabId, count);
        browser.browserAction.setBadgeText({ text: String(count), tabId: entry.tabId });
        browser.browserAction.setBadgeBackgroundColor({ color: "#E53E3E", tabId: entry.tabId });
      }
      break;
    }
    case "stats":
      browser.storage.local.set({ globalStats: msg.payload });
      break;
    case "pong":
      break;
  }
}

// ─── WebRequest blocking (MV2 — Firefox-specific) ────────────────────────────

// In MV2 we can synchronously block requests by returning { cancel: true }.
// We maintain a local blocklist mirror synced from the daemon.
const localBlocklist = new Set<string>([
  "ads.doubleclick.net",
  "googleadservices.com",
  "scorecardresearch.com",
  "static.hotjar.com",
  "analytics.twitter.com",
]);

browser.webRequest.onBeforeRequest.addListener(
  (details: chrome.webRequest.WebRequestBodyDetails) => {
    try {
      const url = new URL(details.url);
      const domain = url.hostname;

      // Notify daemon (async, non-blocking)
      sendToDaemon({
        type: "query_log",
        payload: { domain, url: details.url, tabId: details.tabId },
      });

      // Local blocklist check (synchronous — can cancel the request)
      if (localBlocklist.has(domain)) {
        return { cancel: true };
      }

      // Wildcard subdomain check
      const parts = domain.split(".");
      for (let i = 1; i < parts.length - 1; i++) {
        const parent = parts.slice(i).join(".");
        if (localBlocklist.has("*." + parent)) {
          return { cancel: true };
        }
      }
    } catch { /* ignore */ }
    return {};
  },
  { urls: ["<all_urls>"] },
  ["blocking"]
);

// ─── Blocklist sync from daemon ───────────────────────────────────────────────

function syncBlocklist() {
  fetch("http://127.0.0.1:7787/api/v1/filter/lists")
    .then((r) => r.json())
    .then(() => {
      // TODO: parse list response and update localBlocklist
    })
    .catch(() => { /* daemon may not be running */ });
}

// Sync on startup and every 5 minutes
syncBlocklist();
setInterval(syncBlocklist, 5 * 60 * 1000);

// ─── Tab cleanup ─────────────────────────────────────────────────────────────

browser.tabs.onRemoved.addListener((tabId: number) => {
  tabBlockedCount.delete(tabId);
});

// ─── Message passing to popup ────────────────────────────────────────────────

browser.runtime.onMessage.addListener(
  (request: { type: string; payload?: unknown }, _sender, sendResponse: (r: unknown) => void) => {
    switch (request.type) {
      case "GET_GLOBAL_STATS":
        browser.storage.local.get("globalStats").then((data) => {
          sendResponse({ stats: data.globalStats ?? null });
        });
        return true;

      case "BLOCK_DOMAIN":
        sendToDaemon({ type: "block", payload: request.payload });
        localBlocklist.add((request.payload as { domain: string }).domain);
        sendResponse({ ok: true });
        break;

      case "ALLOW_DOMAIN":
        sendToDaemon({ type: "allow", payload: request.payload });
        localBlocklist.delete((request.payload as { domain: string }).domain);
        sendResponse({ ok: true });
        break;
    }
    return false;
  }
);

// ─── Icon helpers ─────────────────────────────────────────────────────────────

function setIcon(state: "active" | "inactive") {
  const suffix = state === "active" ? "" : "-grey";
  browser.browserAction.setIcon({
    path: {
      16: `icons/icon16${suffix}.png`,
      48: `icons/icon48${suffix}.png`,
      128: `icons/icon128${suffix}.png`,
    },
  });
}

// ─── Startup ──────────────────────────────────────────────────────────────────

connect();
