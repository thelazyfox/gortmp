package rtmp

import (
	"sync/atomic"
)

type Counter struct {
	val int64
}

func (c *Counter) Add(x int64) {
	atomic.AddInt64(&c.val, x)
}

func (c *Counter) Get() int64 {
	return atomic.LoadInt64(&c.val)
}

func (c *Counter) Set(x int64) {
	atomic.StoreInt64(&c.val, x)
}
