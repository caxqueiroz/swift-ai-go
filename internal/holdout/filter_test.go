package holdout

import "testing"

func TestFilterIncludesIDsAtOrAboveMinBucket(t *testing.T) {
	filter := Filter{Mod: 10, MinBucket: 8}

	for _, id := range []string{"addr-2", "addr-7", "addr-11"} {
		bucket := StableBucket(id, filter.Mod)
		if bucket < filter.MinBucket && filter.Include(id) {
			t.Fatalf("included id %q in bucket %d below min bucket %d", id, bucket, filter.MinBucket)
		}
		if bucket >= filter.MinBucket && !filter.Include(id) {
			t.Fatalf("excluded id %q in bucket %d at or above min bucket %d", id, bucket, filter.MinBucket)
		}
	}
	if filter.Include("") {
		t.Fatal("empty id should not pass enabled holdout filter")
	}
}

func TestFilterDisabledIncludesEveryID(t *testing.T) {
	filter := Filter{}
	if !filter.Include("") {
		t.Fatal("disabled filter should include empty id")
	}
	if !filter.Include("addr-1") {
		t.Fatal("disabled filter should include non-empty id")
	}
}

func TestFilterSampleLimitOversamplesForAcceptedBuckets(t *testing.T) {
	filter := Filter{Mod: 10, MinBucket: 8}
	if got := filter.SampleLimit(20); got != 100 {
		t.Fatalf("SampleLimit() = %d, want 100", got)
	}
}

func TestFilterCanSelectComplementaryBucketRange(t *testing.T) {
	filter := Filter{Mod: 10, MinBucket: 0, MaxBucket: 8}

	for _, id := range []string{"addr-2", "addr-7", "addr-11"} {
		bucket := StableBucket(id, filter.Mod)
		if bucket < 8 && !filter.Include(id) {
			t.Fatalf("excluded id %q in bucket %d below max bucket", id, bucket)
		}
		if bucket >= 8 && filter.Include(id) {
			t.Fatalf("included id %q in bucket %d at or above max bucket", id, bucket)
		}
	}
	if got := filter.SampleLimit(80); got != 100 {
		t.Fatalf("SampleLimit() = %d, want 100", got)
	}
}
