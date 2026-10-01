// Package analytics_test — LombokDNSProxy · RAPPOR Analytics Tests
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package analytics

import (
	"math"
	"testing"
)

func defaultReporter() *Reporter {
	return NewReporter(DefaultRAPPORConfig(), 0)
}

// ─── Bloom filter ─────────────────────────────────────────────────────────────

func TestBloomFilter_SameValueSameOutput(t *testing.T) {
	r := defaultReporter()
	a := r.bloomFilter("google.com")
	b := r.bloomFilter("google.com")

	for i := range a {
		if a[i] != b[i] {
			t.Errorf("bloom filter should be deterministic: byte %d differs", i)
		}
	}
}

func TestBloomFilter_DifferentValuesProduceDifferentFilters(t *testing.T) {
	r := defaultReporter()
	a := r.bloomFilter("google.com")
	b := r.bloomFilter("facebook.com")

	same := true
	for i := range a {
		if a[i] != b[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("different values should (with high probability) produce different Bloom filters")
	}
}

func TestBloomFilter_CorrectLength(t *testing.T) {
	r := defaultReporter()
	bloom := r.bloomFilter("test.com")
	expected := DefaultRAPPORConfig().BloomBits / 8
	if len(bloom) != expected {
		t.Errorf("expected %d bytes, got %d", expected, len(bloom))
	}
}

// ─── Permanent randomised response ───────────────────────────────────────────

func TestPermanentRR_Deterministic(t *testing.T) {
	r := defaultReporter()
	bloom := r.bloomFilter("stable.com")

	prr1 := r.permanentRR("stable.com", bloom)
	prr2 := r.permanentRR("stable.com", bloom)

	for i := range prr1 {
		if prr1[i] != prr2[i] {
			t.Errorf("PRR should be stable per (client,value): byte %d differs", i)
		}
	}
}

func TestPermanentRR_DifferentValuesVary(t *testing.T) {
	r := defaultReporter()
	bloomA := r.bloomFilter("domain-a.com")
	bloomB := r.bloomFilter("domain-b.com")

	prrA := r.permanentRR("domain-a.com", bloomA)
	prrB := r.permanentRR("domain-b.com", bloomB)

	same := true
	for i := range prrA {
		if prrA[i] != prrB[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("PRR for different values should (with high probability) differ")
	}
}

// ─── Full encode pipeline ─────────────────────────────────────────────────────

func TestEncode_OutputLengthMatchesConfig(t *testing.T) {
	r := defaultReporter()
	out := r.encode("ads.tracker.com")
	expected := DefaultRAPPORConfig().BloomBits / 8
	if len(out) != expected {
		t.Errorf("expected %d bytes, got %d", expected, len(out))
	}
}

func TestEncode_OutputVariesAcrossReports(t *testing.T) {
	r := defaultReporter()
	// IRR adds fresh noise, so repeated encodes should differ (with high probability)
	reports := make([][]byte, 20)
	for i := range reports {
		reports[i] = r.encode("ads.tracker.com")
	}

	allSame := true
	for i := 1; i < len(reports); i++ {
		for j := range reports[0] {
			if reports[0][j] != reports[i][j] {
				allSame = false
				break
			}
		}
	}
	if allSame {
		t.Error("repeated encode calls should produce varied output due to IRR noise")
	}
}

// ─── Reporter.Report and Flush ────────────────────────────────────────────────

func TestReporter_ReportAccumulates(t *testing.T) {
	r := defaultReporter()
	r.Report("google.com")
	r.Report("facebook.com")
	r.Report("twitter.com")

	reports := r.Flush()
	if len(reports) != 3 {
		t.Errorf("expected 3 reports, got %d", len(reports))
	}
}

func TestReporter_FlushResetsBuffer(t *testing.T) {
	r := defaultReporter()
	r.Report("example.com")
	r.Flush()

	second := r.Flush()
	if len(second) != 0 {
		t.Errorf("second flush should be empty, got %d reports", len(second))
	}
}

// ─── Epsilon calculation ──────────────────────────────────────────────────────

func TestReporter_Epsilon_DefaultConfig(t *testing.T) {
	r := defaultReporter()
	eps := r.Epsilon()

	// With default p=0.5, q=0.75: ε = 2*ln((0.75*0.5)/(0.5*0.25)) = 2*ln(3) ≈ 2.197
	expected := 2 * math.Log(3)
	if math.Abs(eps-expected) > 0.001 {
		t.Errorf("expected ε ≈ %.3f, got %.3f", expected, eps)
	}
}

func TestReporter_Epsilon_HighPrivacy(t *testing.T) {
	// When p=q=0.5: reports are pure noise, ε=0 (maximum privacy)
	r := NewReporter(RAPPORConfig{
		BloomBits: 256, HashFunctions: 2,
		F: 0.5, P: 0.5, Q: 0.5,
	}, 0)
	eps := r.Epsilon()
	if !math.IsInf(eps, 0) && eps > 0.001 {
		t.Errorf("p=q should give ε≈0 or undefined, got %.3f", eps)
	}
}

// ─── Aggregator ───────────────────────────────────────────────────────────────

func TestAggregator_FrequencyEstimate_EmptyReturnsNil(t *testing.T) {
	a := NewAggregator(DefaultRAPPORConfig())
	est := a.FrequencyEstimate()
	if est != nil {
		t.Error("empty aggregator should return nil")
	}
}

func TestAggregator_FrequencyEstimate_AllBitsSet(t *testing.T) {
	cfg := DefaultRAPPORConfig()
	a := NewAggregator(cfg)

	// Add 100 reports where all bits are 1
	allOnes := make([]byte, cfg.BloomBits/8)
	for i := range allOnes {
		allOnes[i] = 0xFF
	}
	for i := 0; i < 100; i++ {
		a.Add(allOnes)
	}

	est := a.FrequencyEstimate()
	if len(est) != cfg.BloomBits {
		t.Fatalf("expected %d estimates, got %d", cfg.BloomBits, len(est))
	}

	// With all bits reported as 1, the corrected estimate for each bit should be high
	for i, e := range est {
		if e < 0 || e > 1 {
			t.Errorf("estimate[%d] = %.3f out of [0,1] range", i, e)
		}
	}
}

func TestAggregator_FrequencyEstimate_AllBitsClear(t *testing.T) {
	cfg := DefaultRAPPORConfig()
	a := NewAggregator(cfg)

	// Add 100 reports where all bits are 0
	zeros := make([]byte, cfg.BloomBits/8)
	for i := 0; i < 100; i++ {
		a.Add(zeros)
	}

	est := a.FrequencyEstimate()
	// Corrected estimate should be near 0 (some noise expected)
	for i, e := range est {
		if e < 0 || e > 1 {
			t.Errorf("estimate[%d] = %.3f out of [0,1] range", i, e)
		}
	}
}

// ─── Different cohorts produce different PRR ──────────────────────────────────

func TestReporter_CohortIsolation(t *testing.T) {
	r0 := NewReporter(DefaultRAPPORConfig(), 0)
	r1 := NewReporter(DefaultRAPPORConfig(), 1)

	bloom0 := r0.bloomFilter("same.value.com")
	bloom1 := r1.bloomFilter("same.value.com")

	// Different cohorts should produce different Bloom filters (via different hash seeds)
	same := true
	for i := range bloom0 {
		if bloom0[i] != bloom1[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("different cohorts should produce different Bloom filters")
	}
}
