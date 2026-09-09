package loaders

// NewLoadersForTest builds a loader bundle whose every loader dispatches
// its batch solely on the capacity trigger: it sets WithBatchCapacity ==
// batchCapacity AND replaces the 1ms wait window with WithWait(time.Hour),
// so the timer cannot fire a partial batch before the Nth key. A batch
// test MUST issue exactly N == batchCapacity Load calls per loader it
// exercises: fewer than N would never reach the capacity threshold and,
// with the timer disabled, the batch would never dispatch (thunks hang).
// This gives deterministic single-batch dispatch under -race contention,
// where the 1ms wall-clock window otherwise races the sequential Load
// loop and splits the batch (issue #818).
//
// Production uses NewLoaders (batchCapacity 0, timer-driven) — its option
// sets are unchanged by this seam.
//
// capacity == N yields a single batch only because every loader is
// configured with NoCache: duplicate keys are not deduped, so each Load
// enqueues a distinct slot and the capacity threshold is actually reached.
func NewLoadersForTest(providers *ProvidersBundle, batchCapacity int) *Loaders {
	return newLoaders(providers, batchCapacity)
}
