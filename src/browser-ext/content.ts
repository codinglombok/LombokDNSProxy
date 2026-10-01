/**
 * LombokDNSProxy — Content Script
 * Injected at document_start into every page.
 * Responsibilities:
 *   1. Observe network requests via PerformanceObserver to feed domain stats to background.
 *   2. Inject a minimal cosmetic filter (hides ad containers whose requests were blocked).
 *   3. Report page domain to background for per-tab tracking.
 *
 * Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
 */

// ─── Report current page domain ──────────────────────────────────────────────

chrome.runtime.sendMessage({
  type: "QUERY_LOG",
  payload: {
    domain: location.hostname,
    url: location.href,
    tabId: -1, // background fills this in
  },
});

// ─── PerformanceObserver: catch resource requests ─────────────────────────────

if (typeof PerformanceObserver !== "undefined") {
  const observer = new PerformanceObserver((list) => {
    for (const entry of list.getEntries()) {
      if (entry.entryType !== "resource") continue;
      const resourceEntry = entry as PerformanceResourceTiming;
      try {
        const url = new URL(resourceEntry.name);
        const domain = url.hostname;
        if (!domain || domain === location.hostname) continue;

        chrome.runtime.sendMessage({
          type: "RESOURCE_DOMAIN",
          payload: {
            domain,
            url: resourceEntry.name,
            durationMs: Math.round(resourceEntry.duration),
            transferSize: resourceEntry.transferSize,
          },
        });
      } catch {
        // ignore unparseable URLs
      }
    }
  });

  observer.observe({ type: "resource", buffered: true });
}

// ─── Cosmetic filter: hide blocked ad containers ──────────────────────────────

// Selectors for common ad container elements that should be hidden
// when their requests are blocked by DNS or declarativeNetRequest.
const COSMETIC_SELECTORS = [
  ".ad-container",
  ".advertisement",
  "#google_ads_iframe",
  ".adsbygoogle",
  "[id^='ad_'][class*='banner']",
  "iframe[src*='doubleclick']",
  "iframe[src*='googleads']",
];

function applyCosmeticFilter(): void {
  const style = document.createElement("style");
  style.textContent = COSMETIC_SELECTORS
    .map((sel) => `${sel} { display: none !important; visibility: hidden !important; }`)
    .join("\n");
  (document.head || document.documentElement).appendChild(style);
}

// Apply at document_start (head may not exist yet, but documentElement will)
applyCosmeticFilter();

// Re-apply after DOM is ready in case of dynamic injection
document.addEventListener("DOMContentLoaded", applyCosmeticFilter, { once: true });
