package holdout

import (
	"hash/fnv"
	"strings"
)

type Filter struct {
	Mod       int
	MinBucket int
	MaxBucket int
}

func (f Filter) Enabled() bool {
	return f.Mod > 0
}

func (f Filter) Include(id string) bool {
	if !f.Enabled() {
		return true
	}
	if strings.TrimSpace(id) == "" {
		return false
	}
	bucket := StableBucket(id, f.Mod)
	return bucket >= f.MinBucket && bucket < f.effectiveMaxBucket()
}

func (f Filter) SampleLimit(limit int) int {
	if !f.Enabled() || limit <= 0 {
		return limit
	}
	acceptedBuckets := f.effectiveMaxBucket() - f.MinBucket
	if acceptedBuckets <= 0 {
		return limit
	}
	return (limit*f.Mod + acceptedBuckets - 1) / acceptedBuckets
}

func (f Filter) EffectiveMaxBucket() int {
	return f.effectiveMaxBucket()
}

func (f Filter) effectiveMaxBucket() int {
	if f.MaxBucket > 0 {
		return f.MaxBucket
	}
	return f.Mod
}

func StableBucket(id string, mod int) int {
	if mod <= 0 {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return int(h.Sum32() % uint32(mod))
}
