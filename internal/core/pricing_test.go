package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestEstimateCostWithCache verifies that EstimateCostWithCache adds cache-read
// and cache-write charges on top of the input+output cost, and returns -1 for
// unknown models. AC#1 of fizeau-38cb69d4.
func TestEstimateCostWithCache(t *testing.T) {
	pt := PricingTable{
		"test-model": {
			InputPerMTok:   3.00,
			OutputPerMTok:  15.00,
			CacheReadPerM:  0.30,
			CacheWritePerM: 3.75,
		},
		"free-model": {
			InputPerMTok:  0,
			OutputPerMTok: 0,
		},
	}

	t.Run("includes cache costs on top of input+output", func(t *testing.T) {
		// 1M input @ $3, 1M output @ $15, 1M cache-read @ $0.30, 1M cache-write @ $3.75
		got := pt.EstimateCostWithCache("test-model", 1_000_000, 1_000_000, 1_000_000, 1_000_000)
		want := 3.00 + 15.00 + 0.30 + 3.75
		assert.InDelta(t, want, got, 1e-9)
	})

	t.Run("strictly greater than input+output only", func(t *testing.T) {
		withCache := pt.EstimateCostWithCache("test-model", 100_000, 50_000, 200_000, 10_000)
		withoutCache := pt.EstimateCost("test-model", 100_000, 50_000)
		assert.Greater(t, withCache, withoutCache)
	})

	t.Run("unknown model returns -1", func(t *testing.T) {
		got := pt.EstimateCostWithCache("no-such-model", 100, 100, 100, 100)
		assert.Equal(t, -1.0, got)
	})

	t.Run("zero cache tokens adds nothing", func(t *testing.T) {
		got := pt.EstimateCostWithCache("test-model", 1_000_000, 1_000_000, 0, 0)
		want := pt.EstimateCost("test-model", 1_000_000, 1_000_000)
		assert.InDelta(t, want, got, 1e-12)
	})

	t.Run("free model returns 0 even with cache tokens", func(t *testing.T) {
		got := pt.EstimateCostWithCache("free-model", 1_000_000, 1_000_000, 500_000, 100_000)
		assert.Equal(t, 0.0, got)
	})
}
