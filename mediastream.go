package rtmp

import (
	"errors"
	"sync"
)

var (
	MediaStreamClosed = errors.New("media stream closed")
)

// Simple utility class
type MediaStreamMap interface {
	Get(string) MediaStream
	Set(string, MediaStream)
	Del(string)
}

type mediaStreamMap struct {
	m map[string]MediaStream
	l sync.RWMutex
}

func NewMediaStreamMap() MediaStreamMap {
	return &mediaStreamMap{
		m: make(map[string]MediaStream),
	}
}

func (m *mediaStreamMap) Get(name string) MediaStream {
	m.l.RLock()
	defer m.l.RUnlock()

	return m.m[name]
}

func (m *mediaStreamMap) Set(name string, stream MediaStream) {
	m.l.Lock()
	defer m.l.Unlock()

	m.m[name] = stream
}

func (m *mediaStreamMap) Del(name string) {
	m.l.Lock()
	defer m.l.Unlock()

	delete(m.m, name)
}

// MediaStream pubsub
type MediaStream interface {
	Publish(FlvTag) error
	Subscribe() (chan FlvTag, error)
	Unsubscribe(chan FlvTag)
	Close()
}

type mediaStream struct {
	pub   chan FlvTag
	sub   chan chan FlvTag
	unsub chan chan FlvTag

	done     chan bool
	doneOnce sync.Once
}

func NewMediaStream() MediaStream {
	ms := &mediaStream{
		pub:   make(chan FlvTag),
		sub:   make(chan chan FlvTag),
		unsub: make(chan chan FlvTag),
		done:  make(chan bool),
	}

	go ms.loop()

	return ms
}

func (ms *mediaStream) loop() {
	var dataHeader, audioHeader, videoHeader *FlvTag
	subs := make(map[chan FlvTag]bool)

	// shut down defers
	defer func() {
		for sub, _ := range subs {
			close(sub)
		}
	}()

	for {
		select {
		case tag := <-ms.pub:
			// store the sequence headers if necesary
			switch tag.Type {
			case AUDIO_TYPE:
				if audioHeader != nil {
					break
				}
				header := tag.GetAudioHeader()
				if header.SoundFormat == 10 && header.AACPacketType == 0 {
					audioHeader = &tag
				}
			case VIDEO_TYPE:
				if videoHeader != nil {
					break
				}
				header := tag.GetVideoHeader()
				if header.FrameType == 1 && header.CodecID == 7 && header.AVCPacketType == 0 {
					videoHeader = &tag
				}
			case DATA_AMF0:
				if dataHeader != nil {
					break
				}
				dataHeader = &tag
			}

			// send tags to streams
			var header *FlvVideoHeader
			for sub, started := range subs {
				if !started && tag.Type == VIDEO_TYPE {
					if header == nil {
						header = tag.GetVideoHeader()
					}
					if header.FrameType == 1 {
						sub <- tag
						subs[sub] = true
					}
				} else if started {
					sub <- tag
				}
			}
		case sub := <-ms.sub:
			subs[sub] = false
			if dataHeader != nil {
				sub <- *dataHeader
			}
			if videoHeader != nil {
				sub <- *videoHeader
			}
			if audioHeader != nil {
				sub <- *audioHeader
			}
		case sub := <-ms.unsub:
			delete(subs, sub)
			close(sub)
		case <-ms.done:
			return // shutdown
		}
	}
}

func (ms *mediaStream) Publish(tag FlvTag) error {
	// select to avoid blocking when the loop exits
	select {
	case ms.pub <- tag:
		return nil
	case <-ms.done:
		return MediaStreamClosed
	}
}

func (ms *mediaStream) Subscribe() (chan FlvTag, error) {
	ch := make(chan FlvTag)

	select {
	case ms.sub <- ch:
		return ch, nil
	case <-ms.done:
		return nil, MediaStreamClosed
	}
}

func (ms *mediaStream) Unsubscribe(ch chan FlvTag) {
	select {
	case ms.unsub <- ch:
	case <-ms.done:
	}
}

func (ms *mediaStream) Close() {
	ms.doneOnce.Do(func() {
		close(ms.done)
	})
}
