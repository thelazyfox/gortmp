package rtmp

import (
	"container/list"
	"io"
	"sync"
)

var pool BufferPool

func init() {
	pool = NewBufferPool()
}

type BufferPool interface {
	Alloc() StaticBuffer
	Free(StaticBuffer)
	Close()
}

type bufferPool struct {
	alloc chan StaticBuffer
	free  chan StaticBuffer

	doneOnce sync.Once
	done     chan bool
}

func (bp *bufferPool) loop() {
	var buffers list.List

	buffers.PushFront(NewStaticBuffer(4096))

	for {
		select {
		case bp.alloc <- buffers.Front().Value.(StaticBuffer):
			buffers.Remove(buffers.Front())
			if buffers.Len() == 0 {
				buffers.PushFront(NewStaticBuffer(4096))
			}
		case buf := <-bp.free:
			buf.Reset()
			buffers.PushFront(buf)
		case <-bp.done:
			return
		}
	}
}

func (bp *bufferPool) Alloc() StaticBuffer {
	select {
	case buf := <-bp.alloc:
		return buf
	case <-bp.done:
		return nil
	}
}

func (bp *bufferPool) Free(buf StaticBuffer) {
	select {
	case bp.free <- buf:
	case <-bp.done:
	}
}

func (bp *bufferPool) Close() {
	bp.doneOnce.Do(func() {
		close(bp.done)
	})
}

func NewBufferPool() BufferPool {
	bp := &bufferPool{
		alloc: make(chan StaticBuffer),
		free:  make(chan StaticBuffer),
	}

	go bp.loop()

	return bp
}

type StaticBuffer interface {
	io.Reader
	io.Writer
	io.ReaderFrom

	Peek(b []byte) int
	Reset()
	Len() int

	Clone() StaticBuffer
}

type staticBuffer struct {
	bytes []byte
	roff  int
	woff  int
}

func NewStaticBuffer(size int) StaticBuffer {
	return &staticBuffer{
		bytes: make([]byte, size),
	}
}

func (sb *staticBuffer) Peek(b []byte) int {
	return copy(b, sb.bytes[sb.roff:sb.woff])
}

func (sb *staticBuffer) Read(b []byte) (int, error) {
	if sb.woff-sb.roff == 0 {
		return 0, io.EOF
	}

	n := copy(b, sb.bytes[sb.roff:])
	sb.roff += n

	return n, nil
}

func (sb *staticBuffer) Write(b []byte) (int, error) {
	if sb.woff >= len(sb.bytes) {
		return 0, io.ErrShortWrite
	}

	n := copy(sb.bytes[sb.woff:], b)
	sb.woff += n

	return n, nil
}

func (sb *staticBuffer) Reset() {
	sb.woff = 0
	sb.roff = 0
}

func (sb *staticBuffer) Cap() int {
	return len(sb.bytes)
}

func (sb *staticBuffer) Len() int {
	return sb.woff - sb.roff
}

func (sb *staticBuffer) Clone() StaticBuffer {
	buf := pool.Alloc()
	buf.Write(sb.bytes[:sb.woff])
	return buf
}

func (sb *staticBuffer) ReadFrom(r io.Reader) (int64, error) {
	n, err := r.Read(sb.bytes[sb.woff:])
	sb.woff += n

	if err == io.EOF {
		return int64(n), err
	} else if sb.woff == len(sb.bytes) {
		return int64(n), io.ErrShortWrite
	} else {
		return int64(n), err
	}
}

type DynamicBuffer interface {
	io.Reader
	io.Writer
	io.ByteReader
	io.ByteWriter
	io.Closer

	io.ReaderFrom

	Peek(b []byte) int
	Len() int

	Clone() DynamicBuffer
}

type dynamicBuffer struct {
	buffers list.List
}

func NewDynamicBuffer() DynamicBuffer {
	return &dynamicBuffer{}
}

func (db *dynamicBuffer) Peek(b []byte) int {
	toread := b[:]
	e := db.buffers.Front()
	for len(toread) > 0 {
		if e != nil {
			e.Value.(StaticBuffer).Peek(toread)
			n := e.Value.(StaticBuffer).Peek(toread)
			toread = toread[n:]
			e = e.Next()
		} else {
			return len(b) - len(toread)
		}
	}

	return 0
}

func (db *dynamicBuffer) Read(b []byte) (int, error) {
	toread := b[:]
	for len(toread) > 0 {
		if db.buffers.Len() == 0 {
			return len(b) - len(toread), io.EOF
		}

		buf := db.buffers.Front().Value.(StaticBuffer)
		n, err := buf.Read(toread)
		toread = toread[n:]
		if err == io.EOF {
			db.buffers.Remove(db.buffers.Front())
			pool.Free(buf)
		}
	}

	return len(b), nil
}

func (db *dynamicBuffer) ReadByte() (byte, error) {
	buf := make([]byte, 1)
	_, err := db.Read(buf)
	if err != nil {
		return 0, err
	} else {
		return buf[0], nil
	}
}

func (db *dynamicBuffer) Write(b []byte) (int, error) {
	if db.buffers.Len() == 0 {
		db.buffers.PushBack(pool.Alloc())
	}

	towrite := b[:]
	for len(towrite) > 0 {
		buf := db.buffers.Back().Value.(StaticBuffer)
		n, err := buf.Write(towrite)
		towrite = towrite[n:]
		if err == io.ErrShortWrite {
			db.buffers.PushBack(pool.Alloc())
		}
	}

	return len(b), nil
}

func (db *dynamicBuffer) WriteByte(b byte) error {
	buf := []byte{b}
	_, err := db.Write(buf)
	return err
}

func (db *dynamicBuffer) Close() error {
	for db.buffers.Len() > 0 {
		pool.Free(db.buffers.Remove(db.buffers.Front()).(StaticBuffer))
	}
	return nil
}

func (db *dynamicBuffer) Len() int {
	l := 0
	for e := db.buffers.Front(); e != nil; e = e.Next() {
		l += e.Value.(StaticBuffer).Len()
	}
	return l
}

func (db *dynamicBuffer) Clone() DynamicBuffer {
	clone := &dynamicBuffer{}
	for e := db.buffers.Front(); e != nil; e = e.Next() {
		clone.buffers.PushBack(e.Value.(StaticBuffer).Clone())
	}
	return clone
}

func (db *dynamicBuffer) ReadFrom(r io.Reader) (int64, error) {
	if db.buffers.Len() == 0 {
		db.buffers.PushBack(pool.Alloc())
	}

	e := db.buffers.Back()
	written := int64(0)
	for {
		n, err := e.Value.(StaticBuffer).ReadFrom(r)
		written += n

		if err == io.ErrShortWrite {
			db.buffers.PushBack(pool.Alloc())
			e = db.buffers.Back()
		} else if err == io.EOF {
			return written, nil
		} else {
			return written, err
		}
	}
}
