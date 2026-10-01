/**
 * LombokDNSProxy — Extension protocol fuzzing via LombokFuzzer
 *
 * Fuzzes the WSMessage JSON envelope parsing that background.ts uses to talk
 * to the OS daemon over ws://127.0.0.1:7788/ext. The daemon is untrusted input
 * from the extension's point of view (a compromised or buggy daemon build
 * should never crash the extension process), so this is exactly the kind of
 * boundary LombokFuzzer's mutation engine is meant to harden.
 *
 * Uses LombokFuzzer's DNS protocol module for the wire-format side (the
 * daemon's actual DNS responses), and a plain mutation harness for the
 * JSON control-channel side (ping/pong/block/allow/stats/query_log).
 *
 * Requires: npm install (pulls in `lombokfuzzer` from package.json)
 * Run:      npm run fuzz
 *
 * Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
 * LombokFuzzer: https://github.com/codinglombok/LombokFuzzer
 */

import { LombokFuzzer, FuzzMode, HarnessMode, FuzzEvent, DnsFuzzer, PRNG } from "lombokfuzzer";

// ─── The function under test ──────────────────────────────────────────────────
// Mirrors the parsing path in background.ts's `ws.onmessage` handler:
// arbitrary bytes from the daemon → JSON.parse → dispatch by `.type`.

interface WSMessage {
  type: string;
  payload?: unknown;
}

function parseDaemonMessage(raw: Uint8Array): WSMessage {
  const text = new TextDecoder("utf-8", { fatal: false }).decode(raw);
  const msg = JSON.parse(text) as WSMessage;

  if (typeof msg.type !== "string") {
    throw new TypeError("WSMessage.type must be a string");
  }
  return msg;
}

// ─── Mutation fuzzing: the JSON control-channel envelope ──────────────────────

describe("WSMessage envelope — mutation fuzzing", () => {
  it("never crashes the process on malformed daemon input", async () => {
    const fuzzer = new LombokFuzzer({
      name: "ws-message-envelope",
      mode: FuzzMode.Mutation,
      maxExecutions: 50_000,
      harness: {
        mode: HarnessMode.InProcess,
        targetFunction: (data: Uint8Array) => {
          try {
            parseDaemonMessage(data);
          } catch {
            // JSON.parse / TypeError on bad input is expected and fine —
            // an *uncaught* crash (segfault-equivalent: unhandled exception
            // escaping the harness, infinite loop, or OOM) is what we're
            // actually hunting for.
          }
        },
      },
    });

    // Seed with every real message type background.ts sends/receives
    const seeds = [
      '{"type":"ping"}',
      '{"type":"pong"}',
      '{"type":"stats","payload":{"totalQueries":1}}',
      '{"type":"block","payload":{"domain":"ads.example.com"}}',
      '{"type":"allow","payload":{"domain":"safe.com","ttl":3600}}',
      '{"type":"query_log","payload":{"tabId":1,"domain":"x.com"}}',
      "{}",
      "not json at all",
      '{"type": 123}', // wrong type for `type`
      '{"type":"ping",'.repeat(500), // unbalanced, deeply nested
    ];
    for (const s of seeds) {
      fuzzer.addSeed(new TextEncoder().encode(s));
    }

    const crashes: string[] = [];
    fuzzer.on(FuzzEvent.CrashFound, ({ crash }) => {
      crashes.push(`[${crash.severity}] ${crash.category} — ${crash.id}`);
    });

    const stats = await fuzzer.run();

    if (crashes.length > 0) {
      throw new Error(
        `LombokFuzzer found ${crashes.length} unhandled crash(es) in WSMessage parsing:\n` +
          crashes.join("\n")
      );
    }
    expect(stats.uniqueCrashes).toBe(0);
  }, 60_000);
});

// ─── Protocol fuzzing: the DNS wire-format side ────────────────────────────────
// The daemon itself resolves real DNS packets; this exercises LombokFuzzer's
// built-in DNS protocol fuzzer against the same wire format, independent of
// the Go-side fuzz targets in tests/fuzz/dns_parser_fuzz_test.go. Two engines,
// two languages, same target format — the kind of redundancy that catches
// bugs a single fuzzer's mutation strategy happens to miss.

describe("DNS wire format — protocol fuzzing", () => {
  it("LombokFuzzer's DnsFuzzer generates and mutates valid-shaped packets", () => {
    const prng = new PRNG(42n);
    const dnsFuzzer = new DnsFuzzer();
    expect(dnsFuzzer.name).toBe("DNS");

    // generate() should always produce a well-formed-ish, non-empty query.
    const generated = dnsFuzzer.generate(prng);
    expect(generated.protocol).toBe("DNS");
    expect(generated.data).toBeInstanceOf(Uint8Array);
    expect(generated.data.length).toBeGreaterThan(0);

    // mutate() is explicitly allowed to degrade the message arbitrarily —
    // including truncating it to zero bytes, which some mutation strategies
    // do on purpose to hunt empty-input parser bugs. So after repeated
    // mutation we only assert the type-level invariants, not a size floor.
    let msg = generated;
    for (let i = 0; i < 100; i++) {
      msg = dnsFuzzer.mutate(msg, prng);
    }
    expect(msg.protocol).toBe("DNS");
    expect(msg.data).toBeInstanceOf(Uint8Array);
  });
});
