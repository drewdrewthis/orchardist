package loaders

// NewLoadersForTest builds a loader bundle whose every loader dispatches
// its batch the instant `batchCapacity` keys are queued, instead of
// waiting for the 1ms timer window. A batch test that issues exactly N
// Load calls passes batchCapacity == N to get deterministic single-batch
// dispatch under -race contention, where the wall-clock window otherwise
// races the Load calls and splits the batch (issue #818).
//
// Production uses NewLoaders (batchCapacity 0, timer-driven) — its option
// sets are unchanged by this seam.
func NewLoadersForTest(providers *ProvidersBundle, batchCapacity int) *Loaders {
	return newLoaders(providers, batchCapacity)
}
