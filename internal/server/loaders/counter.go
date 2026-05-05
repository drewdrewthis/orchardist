package loaders

import "sync/atomic"

// batchCounter is a tiny atomic counter so resolver tests can assert
// "this batch fired exactly once for this query." Lives in its own
// file because the loaders.go body is already long.
type batchCounter struct {
	n atomic.Int64
}

func (c *batchCounter) inc()       { c.n.Add(1) }
func (c *batchCounter) value() int { return int(c.n.Load()) }
