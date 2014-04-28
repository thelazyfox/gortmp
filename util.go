package rtmp

import (
	"bytes"
	"github.com/thelazyfox/goamf"
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

func dumpAmf3(buf *bytes.Buffer) []interface{} {
	buf.ReadByte()
	return dumpAmf0(buf)
}

func dumpAmf0(buf *bytes.Buffer) []interface{} {
	var objs []interface{}
	for buf.Len() > 0 {
		obj, err := amf.ReadValue(buf)
		if err != nil {
			return objs
		}

		objs = append(objs, obj)
	}
	return objs
}
