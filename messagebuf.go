package rtmp

import (
	"bytes"
	"container/list"
	"github.com/thelazyfox/gortmp/log"
	"sync"
)

var GlobalBufferPool BufferPool

func init() {
	GlobalBufferPool = NewBufferPool()
}

type BufferPool interface {
	Alloc() *bytes.Buffer
	Free(*bytes.Buffer)
	Clone(*bytes.Buffer) *bytes.Buffer
	Close()
}

type bufferPool struct {
	alloc chan *bytes.Buffer
	free  chan *bytes.Buffer

	doneOnce sync.Once
	done     chan bool
}

func (bp *bufferPool) loop() {
	var buffers list.List

	buffers.PushFront(&bytes.Buffer{})

	for {
		select {
		case bp.alloc <- buffers.Front().Value.(*bytes.Buffer):
			buffers.Remove(buffers.Front())
			if buffers.Len() == 0 {
				buffers.PushFront(&bytes.Buffer{})
			}
		case buf := <-bp.free:
			buf.Reset()
			buffers.PushFront(buf)
		case <-bp.done:
			return
		}
	}
}

func (bp *bufferPool) Alloc() *bytes.Buffer {
	select {
	case buf := <-bp.alloc:
		return buf
	case <-bp.done:
		return nil
	}
}

func (bp *bufferPool) Free(buf *bytes.Buffer) {
	select {
	case bp.free <- buf:
	case <-bp.done:
	}
}

func (bp *bufferPool) Clone(buf *bytes.Buffer) *bytes.Buffer {
	newBuf := bp.Alloc()
	newBuf.Write(buf.Bytes())
	return newBuf
}

func (bp *bufferPool) Close() {
	bp.doneOnce.Do(func() {
		close(bp.done)
	})
}

func NewBufferPool() BufferPool {
	bp := &bufferPool{
		alloc: make(chan *bytes.Buffer),
		free:  make(chan *bytes.Buffer),
	}

	go bp.loop()

	return bp
}
