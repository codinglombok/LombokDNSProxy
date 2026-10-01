/**
 * LombokDNSProxy — Desktop GUI Frontend
 * Wails v2 · TypeScript · Vanilla (no framework — fast, lightweight)
 * Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
 */

import { GetStats, BlockDomain, AllowDomain, IsRunning, SetSystemDNS } from "../wailsjs/go/main/App";

// ─── Types ───────────────────────────────────────────────────────────────────

interface GlobalStats {
  totalQueries: number;
  blockedCount: number;
  cacheHitRate: number;
  avgLatencyMs: number;
  uptimeSeconds: number;
  filteredDomains: number;
}

// ─── DOM refs ────────────────────────────────────────────────────────────────

const elTotal        = document.getElementById("stat-total")!;
const elBlocked      = document.getElementById("stat-blocked")!;
const elCache        = document.getElementById("stat-cache")!;
const elLatency      = document.getElementById("stat-latency")!;
const elUptime       = document.getElementById("stat-uptime")!;
const elFilterSize   = document.getElementById("stat-filter-size")!;
const elStatus       = document.getElementById("daemon-status")!;
const elBlockInput   = document.getElementById("block-input") as HTMLInputElement;
const elAllowInput   = document.getElementById("allow-input") as HTMLInputElement;

// ─── Stats polling ───────────────────────────────────────────────────────────

async function refreshStats(): Promise<void> {
  try {
    const stats: GlobalStats = await GetStats();
    elTotal.textContent      = stats.totalQueries.toLocaleString();
    elBlocked.textContent    = stats.blockedCount.toLocaleString();
    elCache.textContent      = stats.cacheHitRate.toFixed(1) + "%";
    elLatency.textContent    = stats.avgLatencyMs.toFixed(1) + "ms";
    elUptime.textContent     = formatUptime(stats.uptimeSeconds);
    elFilterSize.textContent = (stats.filteredDomains / 1_000_000).toFixed(1) + "M";
  } catch {
    elStatus.textContent = "daemon offline";
    elStatus.className = "status-badge offline";
  }
}

function formatUptime(seconds: number): string {
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  return `${h}h ${m}m`;
}

// Poll every 3 seconds
setInterval(refreshStats, 3000);
refreshStats();

// Check daemon status
async function checkStatus(): Promise<void> {
  try {
    const running = await IsRunning();
    elStatus.textContent = running ? "running" : "stopped";
    elStatus.className = `status-badge ${running ? "running" : "stopped"}`;
  } catch {
    elStatus.textContent = "offline";
    elStatus.className = "status-badge offline";
  }
}
setInterval(checkStatus, 5000);
checkStatus();

// ─── Block / Allow actions ───────────────────────────────────────────────────

document.getElementById("btn-block")?.addEventListener("click", async () => {
  const domain = elBlockInput.value.trim();
  if (!domain) return;
  try {
    await BlockDomain(domain);
    showToast(`✓ Blocked: ${domain}`, "success");
    elBlockInput.value = "";
  } catch (e) {
    showToast(`✗ Failed: ${e}`, "error");
  }
});

document.getElementById("btn-allow")?.addEventListener("click", async () => {
  const domain = elAllowInput.value.trim();
  if (!domain) return;
  try {
    await AllowDomain(domain);
    showToast(`✓ Allowed: ${domain}`, "success");
    elAllowInput.value = "";
  } catch (e) {
    showToast(`✗ Failed: ${e}`, "error");
  }
});

document.getElementById("btn-set-system-dns")?.addEventListener("click", async () => {
  try {
    await SetSystemDNS();
    showToast("✓ System DNS set to LombokDNSProxy", "success");
  } catch (e) {
    showToast(`✗ ${e}`, "error");
  }
});

// ─── Toast notifications ─────────────────────────────────────────────────────

function showToast(message: string, type: "success" | "error" = "success"): void {
  const toast = document.createElement("div");
  toast.className = `toast toast-${type}`;
  toast.textContent = message;
  document.body.appendChild(toast);
  requestAnimationFrame(() => toast.classList.add("show"));
  setTimeout(() => {
    toast.classList.remove("show");
    setTimeout(() => toast.remove(), 300);
  }, 3000);
}
