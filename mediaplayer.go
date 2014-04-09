package rtmp

import (
	"bytes"
	"github.com/thelazyfox/gortmp/log"
)

type MediaPlayer interface {
	Close()
	Wait()
}

type mediaPlayer struct {
	mb MediaBuffer
	ms MediaStream
	s  Stream

	done chan bool
}

func NewMediaPlayer(mediaStream MediaStream, stream Stream) (MediaPlayer, error) {
	// default to a 4mb buffer
	mb, err := NewMediaBuffer(mediaStream, 4*1024*1024)
	if err != nil {
		return nil, err
	}

	mp := &mediaPlayer{
		mb:   mb,
		ms:   mediaStream,
		s:    stream,
		done: make(chan bool),
	}

	go mp.loop()

	return mp, nil
}

func (mp *mediaPlayer) loop() {
	defer mp.Close()

	for {
		tag, err := mp.mb.Get()
		if err != nil {
			mp.writeTag(tag)
		} else {
			log.Info("MediaPlayer end: %s", err)
			return // buffer end
		}
	}

	close(mp.done)
}

func (mp *mediaPlayer) Wait() {
	<-mp.done
}

func (mp *mediaPlayer) writeTag(tag FlvTag) {
	mp.s.Send(&Message{
		Type:              tag.Type,
		AbsoluteTimestamp: tag.Timestamp,
		Timestamp:         tag.Timestamp,
		Size:              tag.Size,
		Buf:               bytes.NewBuffer(tag.Bytes),
	})
}

func (mp *mediaPlayer) Close() {
	mp.mb.Close()
}
