/**
 * LombokDNSProxy — Browser Extension Background Service Worker
 * Connects to the OS-level LombokDNSProxy daemon via WebSocket (localhost:7788)
 * and provides real-time stats, per-tab domain info, and block/allow controls.
 *
 * Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
 */

// Presence of `export {}` marks this file as a module so its top-level
// declarations (DAEMON_WS_URL, etc.) don't collide with firefox/background-mv2.ts
// when both are type-checked together — at runtime each targets a different
// browser and is never loaded in the same context.
export {};

const DAEMON_WS_URL = "ws://127.0.0.1:7788/ext";
const RECONNECT_DELAY_MS = 3000;
const HEARTBEAT_INTERVAL_MS = 15000;

// Messages sent to / received from the OS daemon
interface DaemonMessage {
  type:
    | "stats"
    | "block"
    | "allow"
    | "query_log"
    | "ping"
    | "pong"
    | "config_update";
  payload?: unknown;
}

interface DomainStats {
  domain: string;
  blocked: boolean;
  latencyMs: number;
  upstream: string;
  cached: boolean;
}

interface GlobalStats {
  totalQueries: number;
  blockedCount: number;
  cacheHitRate: number;
  avgLatencyMs: number;
  uptime: number;
}

// --- WebSocket connection to OS daemon ---

class DaemonConnection {
  private ws: WebSocket | null = null;
  private heartbeatTimer: ReturnType<typeof setInterval> | null = null;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private listeners: Map<string, ((payload: unknown) => void)[]> = new Map();

  connect() {
    if (this.ws?.readyState === WebSocket.OPEN) return;

    try {
      this.ws = new WebSocket(DAEMON_WS_URL);
    } catch {
      this.scheduleReconnect();
      return;
    }

    this.ws.onopen = () => {
      console.log("[LombokDNSProxy] Connected to daemon");
      this.startHeartbeat();
      this.notifyIcon("connected");
    };

    this.ws.onmessage = (event: MessageEvent) => {
      try {
        const msg: DaemonMessage = JSON.parse(event.data as string);
        this.dispatch(msg.type, msg.payload);
      } catch {
        // ignore malformed messages
      }
    };

    this.ws.onclose = () => {
      console.log("[LombokDNSProxy] Daemon disconnected, retrying...");
      this.stopHeartbeat();
      this.notifyIcon("disconnected");
      this.scheduleReconnect();
    };

    this.ws.onerror = () => {
      this.ws?.close();
    };
  }

  send(msg: DaemonMessage) {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(msg));
    }
  }

  on(type: string, listener: (payload: unknown) => void) {
    if (!this.listeners.has(type)) {
      this.listeners.set(type, []);
    }
    this.listeners.get(type)!.push(listener);
  }

  private dispatch(type: string, payload: unknown) {
    const handlers = this.listeners.get(type) ?? [];
    for (const handler of handlers) {
      handler(payload);
    }
  }

  private startHeartbeat() {
    this.heartbeatTimer = setInterval(() => {
      this.send({ type: "ping" });
    }, HEARTBEAT_INTERVAL_MS);
  }

  private stopHeartbeat() {
    if (this.heartbeatTimer !== null) {
      clearInterval(this.heartbeatTimer);
      this.heartbeatTimer = null;
    }
  }

  private scheduleReconnect() {
    if (this.reconnectTimer !== null) return;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      this.connect();
    }, RECONNECT_DELAY_MS);
  }

  private notifyIcon(state: "connected" | "disconnected") {
    chrome.action.setIcon({
      path: state === "connected"
        ? { 16: "icons/icon16.png", 128: "icons/icon128.png" }
        : { 16: "icons/icon16-grey.png", 128: "icons/icon128-grey.png" },
    });
  }
}

const daemon = new DaemonConnection();

// --- Per-tab domain tracking ---

const tabDomainCache: Map<number, DomainStats[]> = new Map();

chrome.webRequest.onBeforeRequest.addListener(
  (details) => {
    const url = new URL(details.url);
    const domain = url.hostname;
    if (!domain) return;

    daemon.send({
      type: "query_log",
      payload: { tabId: details.tabId, domain, url: details.url },
    });
  },
  { urls: ["<all_urls>"] }
);

daemon.on("query_log", (payload) => {
  const { tabId, domain, blocked, latencyMs, upstream, cached } =
    payload as DomainStats & { tabId: number };
  if (!tabDomainCache.has(tabId)) {
    tabDomainCache.set(tabId, []);
  }
  const stats = tabDomainCache.get(tabId)!;
  stats.unshift({ domain, blocked, latencyMs, upstream, cached });
  if (stats.length > 200) stats.pop();

  // Update badge with blocked count on this tab
  const blockedCount = stats.filter((s) => s.blocked).length;
  if (blockedCount > 0) {
    chrome.action.setBadgeText({ text: String(blockedCount), tabId });
    chrome.action.setBadgeBackgroundColor({ color: "#E53E3E", tabId });
  }
});

// Clean up when tab closes
chrome.tabs.onRemoved.addListener((tabId) => {
  tabDomainCache.delete(tabId);
});

// --- Message passing to popup ---

chrome.runtime.onMessage.addListener(
  (request: { type: string; payload?: unknown }, _sender, sendResponse) => {
    switch (request.type) {
      case "GET_TAB_STATS": {
        chrome.tabs.query({ active: true, currentWindow: true }, ([tab]) => {
          const stats = tab?.id ? (tabDomainCache.get(tab.id) ?? []) : [];
          sendResponse({ stats });
        });
        return true; // keep channel open for async response
      }

      case "GET_GLOBAL_STATS": {
        // Ask daemon for global stats
        daemon.send({ type: "stats" });
        const handler = (payload: unknown) => {
          sendResponse({ stats: payload as GlobalStats });
          daemon["listeners"].get("stats")?.splice(0, 1); // one-shot
        };
        daemon.on("stats", handler);
        return true;
      }

      case "BLOCK_DOMAIN": {
        daemon.send({ type: "block", payload: request.payload });
        sendResponse({ ok: true });
        break;
      }

      case "ALLOW_DOMAIN": {
        daemon.send({ type: "allow", payload: request.payload });
        sendResponse({ ok: true });
        break;
      }
    }
    return false;
  }
);

// --- Startup ---

daemon.connect();

// Persist stats to chrome.storage every 60s for popup use when daemon offline
setInterval(() => {
  daemon.send({ type: "stats" });
}, 60_000);

daemon.on("stats", (payload) => {
  chrome.storage.local.set({ globalStats: payload });
});
