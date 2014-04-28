package rtmp

import (
	"bytes"
	"fmt"
	"sync"
)

type MediaPlayer interface {
	Stream() Stream
	Start()
	Close()
	Wait()
}

type mediaPlayer struct {
	mb MediaBuffer
	ms MediaStream
	s  Stream

	done chan bool

	loopOnce sync.Once

	log Logger
}

func NewMediaPlayer(mediaStream MediaStream, stream Stream) (MediaPlayer, error) {
	// default to a 4mb buffer
	mb, err := NewMediaBuffer(mediaStream, 4*1024*1024)
	if err != nil {
		return nil, err
	}

	logTag := fmt.Sprintf("MediaPlayer(%s, %s)", mediaStream.Stream().Name(), stream.Conn().Addr())
	mp := &mediaPlayer{
		mb:   mb,
		ms:   mediaStream,
		s:    stream,
		done: make(chan bool),
		log:  NewLogger(logTag),
	}

	return mp, nil
}

func (mp *mediaPlayer) Stream() Stream {
	return mp.s
}

func (mp *mediaPlayer) loop() {
	defer mp.log.Debugf("start")
	defer mp.log.Debugf("stop")
	defer mp.Close()
	defer func() {
		close(mp.done)
	}()

	for {
		tag, err := mp.mb.Get()
		if err != nil {
			if err != MediaStreamClosed {
				mp.log.Errorf("error getting next tag: %s", err)
			}
			return
		}

		err = mp.writeTag(tag)
		if err != nil {
			mp.log.Errorf("error writing tag: %s", err)
			return
		}
	}
}

func (mp *mediaPlayer) writeTag(tag *FlvTag) error {
	return mp.s.Send(&Message{
		Type:              tag.Type,
		AbsoluteTimestamp: tag.Timestamp,
		Timestamp:         tag.Timestamp,
		Size:              tag.Size,
		Buf:               bytes.NewBuffer(tag.Bytes),
	})
}

func (mp *mediaPlayer) Start() {
	mp.loopOnce.Do(func() {
		go mp.loop()
	})
}

func (mp *mediaPlayer) Close() {
	mp.mb.Close()
}

func (mp *mediaPlayer) Wait() {
	<-mp.done
}
