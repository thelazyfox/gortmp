package rtmp

import (
	"errors"
	"math"
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

// All Measurements in kbps
type MediaStreamStats struct {
	VideoBytes int64
	AudioBytes int64
	Bytes      int64

	VideoBytesRate int64
	AudioBytesRate int64
	BytesRate      int64
}

// MediaStream pubsub
type MediaStream interface {
	Publish(*FlvTag) error
	Subscribe() (chan *FlvTag, error)
	Unsubscribe(chan *FlvTag)
	Close()

	Stats() MediaStreamStats
}

type mediaStream struct {
	pub   chan *FlvTag
	sub   chan chan *FlvTag
	unsub chan chan *FlvTag

	videoBytes Counter
	audioBytes Counter
	bytes      Counter

	videoBps Counter
	audioBps Counter
	bps      Counter

	lastVideoBytes int64
	lastAudioBytes int64
	lastBytes      int64
	lastTs         int64

	done     chan bool
	doneOnce sync.Once
}

func NewMediaStream() MediaStream {
	ms := &mediaStream{
		pub:   make(chan *FlvTag),
		sub:   make(chan chan *FlvTag),
		unsub: make(chan chan *FlvTag),
		done:  make(chan bool),
	}

	go ms.loop()

	return ms
}

func (ms *mediaStream) toInt(f float64) int64 {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return 0
	} else {
		return int64(f)
	}
}

func (ms *mediaStream) updateBps(ts int64) {
	tsDelta := float64(ts - ms.lastTs)

	// fetch current values
	videoBytes := ms.videoBytes.Get()
	audioBytes := ms.audioBytes.Get()
	bytes := ms.bytes.Get()

	videoBps := float64(videoBytes-ms.lastVideoBytes) / tsDelta * 1000
	audioBps := float64(audioBytes-ms.lastAudioBytes) / tsDelta * 1000
	bps := float64(bytes-ms.lastBytes) / tsDelta * 1000

	ms.lastVideoBytes = videoBytes
	ms.lastAudioBytes = audioBytes
	ms.lastBytes = bytes
	ms.lastTs = ts

	ms.videoBps.Set(ms.toInt(videoBps))
	ms.audioBps.Set(ms.toInt(audioBps))
	ms.bps.Set(ms.toInt(bps))
}

func (ms *mediaStream) loop() {
	var dataHeader, audioHeader, videoHeader *FlvTag
	subs := make(map[chan *FlvTag]bool)

	// shut down defers
	defer func() {
		for sub, _ := range subs {
			close(sub)
		}
	}()

	for {
		select {
		case tag := <-ms.pub:

			ms.bytes.Add(int64(tag.Size))
			// store the sequence headers if necesary
			switch tag.Type {
			case AUDIO_TYPE:
				ms.audioBytes.Add(int64(tag.Size))
				if audioHeader != nil {
					break
				}
				header := tag.GetAudioHeader()
				if header.SoundFormat == 10 && header.AACPacketType == 0 {
					audioHeader = tag.Clone()
				}
			case VIDEO_TYPE:
				ms.videoBytes.Add(int64(tag.Size))
				header := tag.GetVideoHeader()

				if header.FrameType == 1 {
					if header.CodecID == 7 && header.AVCPacketType == 0 {
						videoHeader = tag
					}

					ms.updateBps(int64(tag.Timestamp))
				}
				if header.FrameType == 1 && header.CodecID == 7 && header.AVCPacketType == 0 {
					videoHeader = tag.Clone()
				}
			case DATA_AMF0:
				if dataHeader != nil {
					break
				}
				dataHeader = tag.Clone()
			}

			// send tags to streams
			var header *FlvVideoHeader
			for sub, started := range subs {
				if !started && tag.Type == VIDEO_TYPE {
					if header == nil {
						header = tag.GetVideoHeader()
					}
					if header.FrameType == 1 {
						sub <- tag.Clone()
						subs[sub] = true
					}
				} else if started {
					sub <- tag.Clone()
				}
			}
			tag.Buf.Close()
		case sub := <-ms.sub:
			subs[sub] = false
			if dataHeader != nil {
				sub <- dataHeader.Clone()
			}
			if videoHeader != nil {
				sub <- videoHeader.Clone()
			}
			if audioHeader != nil {
				sub <- audioHeader.Clone()
			}
		case sub := <-ms.unsub:
			if _, found := subs[sub]; found {
				delete(subs, sub)
				close(sub)
			}
		case <-ms.done:
			return // shutdown
		}
	}
}

func (ms *mediaStream) Publish(tag *FlvTag) error {
	// select to avoid blocking when the loop exits
	select {
	case ms.pub <- tag:
		return nil
	case <-ms.done:
		return MediaStreamClosed
	}
}

func (ms *mediaStream) Subscribe() (chan *FlvTag, error) {
	ch := make(chan *FlvTag)

	select {
	case ms.sub <- ch:
		return ch, nil
	case <-ms.done:
		return nil, MediaStreamClosed
	}
}

func (ms *mediaStream) Unsubscribe(ch chan *FlvTag) {
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

func (ms *mediaStream) Stats() MediaStreamStats {
	return MediaStreamStats{
		VideoBytes: ms.videoBytes.Get(),
		AudioBytes: ms.audioBytes.Get(),
		Bytes:      ms.bytes.Get(),

		VideoBytesRate: ms.videoBps.Get(),
		AudioBytesRate: ms.audioBps.Get(),
		BytesRate:      ms.bps.Get(),
	}
}
