package rtmp

import (
	"container/list"
	"github.com/thelazyfox/gortmp/log"
)

type MediaBuffer interface {
	Get() (*FlvTag, error)
	Close()
	MediaStream() MediaStream
}

type mediaBuffer struct {
	ms      MediaStream
	sub     chan *FlvTag
	out     chan *FlvTag
	tags    list.List
	size    uint32
	maxSize uint32
}

func NewMediaBuffer(ms MediaStream, maxSize uint32) (MediaBuffer, error) {
	sub, err := ms.Subscribe()
	if err != nil {
		return nil, err
	}

	mb := &mediaBuffer{
		ms:      ms,
		sub:     sub,
		out:     make(chan *FlvTag),
		maxSize: maxSize,
	}

	go mb.loop()

	return mb, nil
}

func (mb *mediaBuffer) MediaStream() MediaStream {
	return mb.ms
}

func (mb *mediaBuffer) loop() {
	defer func() {
		close(mb.out)
	}()

	for {
		if mb.tags.Len() == 0 {
			select {
			case tag, ok := <-mb.sub:
				if ok {
					mb.push(tag)
				} else {
					return // shutdown
				}
			}
		} else {
			out := mb.front()
			select {
			case tag, ok := <-mb.sub:
				if ok {
					mb.push(tag)
				} else {
					return //shutdown
				}
			case mb.out <- out.Value.(*FlvTag):
				mb.remove(out)
			}

		}
	}
}

func (mb *mediaBuffer) push(tag *FlvTag) {
	if mb.size+tag.Size > mb.maxSize {
		GlobalBufferPool.Free(tag.Buf)
	} else {
		mb.size += tag.Size
		mb.tags.PushBack(tag)
	}
}

func (mb *mediaBuffer) front() *list.Element {
	return mb.tags.Front()
}

func (mb *mediaBuffer) remove(e *list.Element) {
	tag := mb.tags.Remove(e).(*FlvTag)
	mb.size -= tag.Size
}

func (mb *mediaBuffer) Get() (*FlvTag, error) {
	if tag, ok := <-mb.out; ok {
		return tag, nil
	} else {
		return nil, MediaStreamClosed
	}
}

func (mb *mediaBuffer) Close() {
	mb.ms.Unsubscribe(mb.sub)
}
