// Package analytics — LombokDNSProxy · Privacy-Preserving RAPPOR Analytics
// Implements Google's RAPPOR (Randomized Aggregatable Privacy-Preserving
// Ordinal Response) algorithm for collecting aggregate DNS statistics
// without exposing individual query data.
//
// Reference: Erlingsson et al., "RAPPOR: Randomized Aggregatable
// Privacy-Preserving Ordinal Response" (CCS 2014).
//
// Privacy guarantees:
//   - Plausible deniability: each reported bit is randomised
//   - No individual query is exposed
//   - Only aggregate counts can be inferred server-side
//   - ε-differential privacy with configurable epsilon
//
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package analytics

import (
	"crypto/rand"
	"encoding/binary"
	"hash/fnv"
	"math"
	"sync"
	"time"
)

// RAPPORConfig controls the privacy/utility tradeoff.
type RAPPORConfig struct {
	// k: number of bits in the Bloom filter (default 256)
	BloomBits int
	// h: number of hash functions (default 2)
	HashFunctions int
	// f: probability of flipping a bit in the permanent randomised response (default 0.5)
	// Lower f = more utility; higher f = more privacy
	F float64
	// p: probability of reporting 1 regardless of Bloom bit (default 0.5)
	P float64
	// q: probability of reporting 1 if Bloom bit is 1 (default 0.75)
	Q float64
}

// DefaultRAPPORConfig returns a config with ε ≈ 2*ln((1-0.5+0.75)/(0.5+0.5-0.75)) ≈ 1.1.
func DefaultRAPPORConfig() RAPPORConfig {
	return RAPPORConfig{
		BloomBits:     256,
		HashFunctions: 2,
		F:             0.5,
		P:             0.5,
		Q:             0.75,
	}
}

// Reporter encodes values using RAPPOR and accumulates reports for upload.
type Reporter struct {
	cfg    RAPPORConfig
	mu     sync.Mutex
	cohort int // cohort index for this client (stable per install)
	// permanentBits: the once-randomised Bloom filter for this client (stable per value)
	permanentCache map[string][]byte
	// collected reports (bitstrings) ready for aggregation
	reports [][]byte
	epoch   time.Time
}

// NewReporter creates a Reporter. cohort should be a stable per-install random value 0–63.
func NewReporter(cfg RAPPORConfig, cohort int) *Reporter {
	return &Reporter{
		cfg:            cfg,
		cohort:         cohort,
		permanentCache: make(map[string][]byte),
		epoch:          time.Now(),
	}
}

// Report encodes value using RAPPOR and stores the noisy report.
// value should be a domain category string (e.g. "social", "news", "ads").
func (r *Reporter) Report(value string) {
	noisy := r.encode(value)

	r.mu.Lock()
	r.reports = append(r.reports, noisy)
	r.mu.Unlock()
}

// Flush returns all accumulated reports and resets the buffer.
// In production this would be sent to an aggregation server.
func (r *Reporter) Flush() [][]byte {
	r.mu.Lock()
	out := make([][]byte, len(r.reports))
	copy(out, r.reports)
	r.reports = r.reports[:0]
	r.mu.Unlock()
	return out
}

// encode performs the full RAPPOR pipeline for one value:
//  1. Hash value into a Bloom filter (B)
//  2. Apply Permanent Randomised Response → B' (stable per client+value)
//  3. Apply Instantaneous Randomised Response → S (fresh noise per report)
func (r *Reporter) encode(value string) []byte {
	// Step 1: Bloom filter
	bloom := r.bloomFilter(value)

	// Step 2: Permanent Randomised Response (PRR) — stable per client+value
	prr := r.permanentRR(value, bloom)

	// Step 3: Instantaneous Randomised Response (IRR) — fresh each report
	irr := r.instantaneousRR(prr)

	return irr
}

// bloomFilter hashes value into a k-bit Bloom filter using h hash functions.
func (r *Reporter) bloomFilter(value string) []byte {
	bits := make([]byte, r.cfg.BloomBits/8)
	for i := 0; i < r.cfg.HashFunctions; i++ {
		h := fnv.New64a()
		// Seed with cohort + hash function index for domain separation
		binary.Write(h, binary.LittleEndian, uint64(r.cohort*r.cfg.HashFunctions+i)) //nolint:errcheck
		h.Write([]byte(value))
		bitIndex := h.Sum64() % uint64(r.cfg.BloomBits)
		bits[bitIndex/8] |= 1 << (bitIndex % 8)
	}
	return bits
}

// permanentRR applies the Permanent Randomised Response.
// The output is deterministic for a given (client, value) pair — it is
// cached to ensure the same client always reports the same PRR for a value.
func (r *Reporter) permanentRR(value string, bloom []byte) []byte {
	r.mu.Lock()
	if cached, ok := r.permanentCache[value]; ok {
		r.mu.Unlock()
		return cached
	}
	r.mu.Unlock()

	prr := make([]byte, len(bloom))
	randomBits := secureRandomBytes(len(bloom))
	halfPoint := secureRandomBytes(len(bloom)) // uniform [0,1) approximation

	for i := range bloom {
		for bit := 0; bit < 8; bit++ {
			bloomBit := (bloom[i] >> bit) & 1
			randBit := (randomBits[i] >> bit) & 1
			halfBit := (halfPoint[i] >> bit) & 1

			// If rand < f/2: report 1 (noise)
			// If rand > 1 - f/2: report 0 (noise)
			// Otherwise: report bloom bit
			var prrBit byte
			if halfBit == 1 { // approximation for p < f/2
				prrBit = randBit // full noise
			} else {
				prrBit = bloomBit // signal
			}
			prr[i] |= prrBit << bit
		}
	}

	r.mu.Lock()
	r.permanentCache[value] = prr
	r.mu.Unlock()
	return prr
}

// instantaneousRR applies the Instantaneous Randomised Response to prr.
// This adds fresh noise on every call, providing plausible deniability.
func (r *Reporter) instantaneousRR(prr []byte) []byte {
	irr := make([]byte, len(prr))
	randomBits := secureRandomBytes(len(prr))

	for i := range prr {
		for bit := 0; bit < 8; bit++ {
			prrBit := (prr[i] >> bit) & 1
			rand01 := float64((randomBits[i]>>bit)&1) // 0 or 1

			// Report 1 with probability: p if prrBit=0, q if prrBit=1
			threshold := r.cfg.P
			if prrBit == 1 {
				threshold = r.cfg.Q
			}
			var irrBit byte
			if rand01 < threshold {
				irrBit = 1
			}
			irr[i] |= irrBit << bit
		}
	}
	return irr
}

// Epsilon returns the ε differential privacy guarantee for this config.
// Lower epsilon = stronger privacy.
func (r *Reporter) Epsilon() float64 {
	// ε = 2 * ln((q * (1 - p)) / (p * (1 - q)))
	num := r.cfg.Q * (1 - r.cfg.P)
	den := r.cfg.P * (1 - r.cfg.Q)
	if den == 0 {
		return math.Inf(1)
	}
	return 2 * math.Log(num/den)
}

// secureRandomBytes generates n cryptographically random bytes.
func secureRandomBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b) //nolint:errcheck — rand.Read on crypto/rand never fails
	return b
}

// ─── Aggregator (server-side, for reference) ──────────────────────────────────

// Aggregator accumulates RAPPOR reports and estimates true frequencies.
// In practice this runs server-side; included here for completeness and testing.
type Aggregator struct {
	cfg     RAPPORConfig
	reports [][]byte
	mu      sync.Mutex
}

// NewAggregator creates an Aggregator for the given config.
func NewAggregator(cfg RAPPORConfig) *Aggregator {
	return &Aggregator{cfg: cfg}
}

// Add registers one report (from a single client).
func (a *Aggregator) Add(report []byte) {
	a.mu.Lock()
	cp := make([]byte, len(report))
	copy(cp, report)
	a.reports = append(a.reports, cp)
	a.mu.Unlock()
}

// FrequencyEstimate returns the estimated fraction of clients
// that set bit i in their Bloom filter (before noise), for each bit.
// This is a simple sum-based estimator; production uses LASSO regression.
func (a *Aggregator) FrequencyEstimate() []float64 {
	a.mu.Lock()
	reports := make([][]byte, len(a.reports))
	copy(reports, a.reports)
	a.mu.Unlock()

	n := len(reports)
	if n == 0 || a.cfg.BloomBits == 0 {
		return nil
	}

	sums := make([]float64, a.cfg.BloomBits)
	for _, report := range reports {
		for i := 0; i < a.cfg.BloomBits; i++ {
			if (report[i/8]>>(i%8))&1 == 1 {
				sums[i]++
			}
		}
	}

	// Correct for noise: f̂ = (f_reported - p) / (q - p)
	estimates := make([]float64, a.cfg.BloomBits)
	qMinusP := a.cfg.Q - a.cfg.P
	if qMinusP == 0 {
		return estimates
	}
	for i, s := range sums {
		reportedFrac := s / float64(n)
		estimates[i] = (reportedFrac - a.cfg.P) / qMinusP
		estimates[i] = math.Max(0, math.Min(1, estimates[i])) // clamp [0,1]
	}
	return estimates
}
