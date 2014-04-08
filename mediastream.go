package rtmp

import (
	"bytes"
	"encoding/binary"
	"github.com/thelazyfox/goamf"
	"github.com/thelazyfox/gortmp/log"
	"sync"
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

// Actually the media stream

type MediaStream interface {
	Publish(FlvTag)
	Subscribe() chan FlvTag
	Unsubscribe(chan FlvTag)
	Close()
}

type MediaStreamSubscriber interface {
	OnTag(FlvTag)
	OnClose()
}

type mediaStream struct {
	pub   chan FlvTag
	sub   chan chan FlvTag
	unsub chan chan FlvTag
	done  chan bool
}

type FlvAudioHeader struct {
	SoundFormat   uint8
	SoundRate     uint8
	SoundSize     uint8
	SoundType     uint8
	AACPacketType uint8
}

type FlvVideoHeader struct {
	FrameType     uint8
	CodecID       uint8
	AVCPacketType uint8
}

type FlvTag struct {
	Type      uint8
	Timestamp uint32
	Size      uint32
	Bytes     []byte
}

func (f *FlvTag) GetAudioHeader() *FlvAudioHeader {
	if f.Type != AUDIO_TYPE {
		return nil
	}

	header := &FlvAudioHeader{}
	buf := bytes.NewBuffer(f.Bytes)

	var bits uint8
	binary.Read(buf, binary.BigEndian, &bits)
	header.SoundFormat = bits >> 4
	header.SoundRate = (bits >> 2) & 3
	header.SoundSize = (bits >> 1) & 1
	header.SoundType = bits & 1

	if header.SoundFormat == 10 {
		binary.Read(buf, binary.BigEndian, &header.AACPacketType)
	}

	return header
}

func (f *FlvTag) GetVideoHeader() *FlvVideoHeader {
	if f.Type != VIDEO_TYPE {
		return nil
	}

	header := &FlvVideoHeader{}
	buf := bytes.NewBuffer(f.Bytes)

	var bits uint8
	binary.Read(buf, binary.BigEndian, &bits)

	header.FrameType = bits >> 4
	header.CodecID = bits & 0xF

	if header.CodecID == 7 {
		binary.Read(buf, binary.BigEndian, &header.AVCPacketType)
	}

	return header
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
	done := false

	for !done {
		select {
		case tag, ok := <-ms.pub:
			if !ok {
				done = true
				continue
			}

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
		}
	}

	for sub, _ := range subs {
		close(sub)
	}

	close(ms.done)
}

func logTag(tag FlvTag) {
	switch tag.Type {
	case VIDEO_TYPE:
		header := tag.GetVideoHeader()
		log.Debug("%#v", *header)
	case AUDIO_TYPE:
		header := tag.GetAudioHeader()
		log.Debug("%#v", *header)
	case DATA_AMF0:
		buf := bytes.NewBuffer(tag.Bytes)
		for buf.Len() > 0 {
			object, err := amf.ReadValue(buf)
			if err != nil {
				log.Debug("logTag parse amf0 error")
			} else {
				log.Debug("Amf Object: %+v", object)
			}
		}
	}
}

func (ms *mediaStream) Publish(tag FlvTag) {
	// select to avoid blocking when the loop exits
	select {
	case ms.pub <- tag:
	case <-ms.done:
	}
}

func (ms *mediaStream) Subscribe() chan FlvTag {

	ch := make(chan FlvTag)

	select {
	case ms.sub <- ch:
		return ch
	case <-ms.done:
		return nil
	}
}

func (ms *mediaStream) Unsubscribe(ch chan FlvTag) {
	select {
	case ms.unsub <- ch:
	case <-ms.done:
	}
}

func (ms *mediaStream) Close() {
	close(ms.pub)
}

type MediaPlayer interface {
	Close()
	Wait()
}

type mediaPlayer struct {
	done chan bool
	once sync.Once
}

func NewMediaPlayer(mediaStream MediaStream, stream Stream) MediaPlayer {
	mp := &mediaPlayer{
		done: make(chan bool),
	}

	go mp.loop(mediaStream, stream)

	return mp
}

func (mp *mediaPlayer) loop(mediaStream MediaStream, stream Stream) {
	defer mp.Close()

	ch := mediaStream.Subscribe()

	if ch == nil {
		// trying to play a closed stream
		return
	}

	for {
		select {
		case tag, ok := <-ch:
			if ok {
				mp.writeTag(tag, stream)
			} else {
				return
			}
		case <-mp.done:
			mediaStream.Unsubscribe(ch)
			return
		}
	}
}

func (mp *mediaPlayer) Wait() {
	<-mp.done
}

func (mp *mediaPlayer) writeTag(tag FlvTag, stream Stream) {
	log.Debug("mediaPlayer.writeTag")
	// logTag(tag)
	stream.Send(&Message{
		Type:              tag.Type,
		AbsoluteTimestamp: tag.Timestamp,
		Timestamp:         tag.Timestamp,
		Size:              tag.Size,
		Buf:               bytes.NewBuffer(tag.Bytes),
	})
}

func (mp *mediaPlayer) Close() {
	mp.once.Do(func() {
		close(mp.done)
	})
}
