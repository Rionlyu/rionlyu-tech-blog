package contact

import (
	"strconv"
	"testing"
	"time"
)

func TestLimiterBoundsDistinctClientState(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	limiter := NewLimiter(5, time.Hour, func() time.Time { return now })

	for index := 0; index < maxLimiterBuckets; index++ {
		if allowed, _ := limiter.Allow(strconv.Itoa(index)); !allowed {
			t.Fatalf("client %d rejected before capacity", index)
		}
	}
	if allowed, retry := limiter.Allow("over-capacity"); allowed || retry != time.Hour {
		t.Fatalf("over-capacity attempt = (%v, %v)", allowed, retry)
	}
	if len(limiter.buckets) != maxLimiterBuckets {
		t.Fatalf("bucket count = %d", len(limiter.buckets))
	}

	now = now.Add(time.Hour)
	if allowed, _ := limiter.Allow("after-expiry"); !allowed {
		t.Fatal("client rejected after expired state should have been pruned")
	}
	if len(limiter.buckets) != 1 {
		t.Fatalf("bucket count after expiry = %d", len(limiter.buckets))
	}
}
