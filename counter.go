package rtmp

import (
	"sync"
)

type Counter interface {
	Get() int64
	Set(int64)
	Add(int64)
}

type counter struct {
	sync.RWMutex
	value int64
}

func NewCounter() Counter {
	return &counter{}
}

func (c *counter) Get() int64 {
	c.RLock()
	defer c.RUnlock()

	return c.value
}

func (c *counter) Set(x int64) {
	c.Lock()
	defer c.Unlock()

	c.value = x
}

func (c *counter) Add(x int64) {
	c.Lock()
	defer c.Unlock()

	c.value += x
}
