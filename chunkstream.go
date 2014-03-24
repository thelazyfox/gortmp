// Copyright 2013, zhangpeihao All rights reserved.

package rtmp

import (
	"bytes"
	"encoding/binary"
	"github.com/thelazyfox/gortmp/log"
)

//	"github.com/thelazyfox/gortmp/log"

type ChunkStream interface {
	ChunkStreamReader
	ChunkStreamWriter

	Close()

	SendSetChunkSize(uint32) error
	SendSetWindowSize(uint32) error
	SendSetPeerBandwidth(uint32, uint8) error

	OnMessage(msg *Message)
	OnWindowFull(uint32)
}

type ChunkStreamHandler interface {
	OnMessage(msg *Message)
}

type chunkStream struct {
	ChunkStreamReader
	ChunkStreamWriter

	handler ChunkStreamHandler
}

func NewChunkStream(handler ChunkStreamHandler) ChunkStream {
	cs := &chunkStream{handler: handler}
	cs.ChunkStreamReader = NewChunkStreamReader(cs)
	cs.ChunkStreamWriter = NewChunkStreamWriter()
	return cs
}

func (cs *chunkStream) Close() {
	panic("ChunkStream.Close() not implemented")
}

func (cs *chunkStream) OnMessage(msg *Message) {
	if msg.ChunkStreamID == CS_ID_PROTOCOL_CONTROL {
		switch msg.Type {
		case SET_CHUNK_SIZE:
			return // handled in ChunkStreamReader
		case WINDOW_ACKNOWLEDGEMENT_SIZE:
			return // handled in ChunkStreamReader
		}
	}

	cs.handler.OnMessage(msg)
}

func (cs *chunkStream) OnWindowFull(count uint32) {
	go func() {
		err := cs.sendAcknowledgement(count)
		if err != nil {
			log.Debug("Error sending window ack: %s", err)
		}
	}()
}

func (cs *chunkStream) SendSetPeerBandwidth(bw uint32, limit uint8) error {
	buf := new(bytes.Buffer)

	err := binary.Write(buf, binary.BigEndian, bw)
	if err != nil {
		return err
	}

	err = binary.Write(buf, binary.BigEndian, limit)
	if err != nil {
		return err
	}

	msg := &Message{
		ChunkStreamID:     CS_ID_PROTOCOL_CONTROL,
		Type:              SET_PEER_BANDWIDTH,
		StreamID:          0,
		AbsoluteTimestamp: 0,
		Buf:               buf,
		Size:              uint32(buf.Len()),
	}

	return cs.Write(msg)
}

func (cs *chunkStream) SendSetWindowSize(size uint32) error {
	buf := new(bytes.Buffer)
	err := binary.Write(buf, binary.BigEndian, size)

	if err != nil {
		return err
	}

	msg := &Message{
		ChunkStreamID:     CS_ID_PROTOCOL_CONTROL,
		Type:              WINDOW_ACKNOWLEDGEMENT_SIZE,
		StreamID:          0,
		AbsoluteTimestamp: 0,
		Buf:               buf,
		Size:              uint32(buf.Len()),
	}

	log.Trace("SetChunkSize: %+v", msg)
	log.Trace("Buf: %#v len=%d", buf, buf.Len())

	return cs.Write(msg)
}

func (cs *chunkStream) SendSetChunkSize(chunkSize uint32) error {
	buf := new(bytes.Buffer)
	err := binary.Write(buf, binary.BigEndian, &chunkSize)

	if err != nil {
		return err
	}

	msg := &Message{
		ChunkStreamID:     CS_ID_PROTOCOL_CONTROL,
		Type:              SET_CHUNK_SIZE,
		StreamID:          0,
		AbsoluteTimestamp: 0,
		Buf:               buf,
		Size:              uint32(buf.Len()),
	}

	return cs.Write(msg)
}

func (cs *chunkStream) sendAcknowledgement(inCount uint32) error {
	buf := new(bytes.Buffer)
	err := binary.Write(buf, binary.BigEndian, inCount)

	if err != nil {
		return err
	}

	msg := &Message{
		ChunkStreamID:     CS_ID_PROTOCOL_CONTROL,
		Type:              ACKNOWLEDGEMENT,
		StreamID:          0,
		AbsoluteTimestamp: 0,
		Buf:               buf,
		Size:              uint32(buf.Len()),
	}

	return cs.Write(msg)
}

func (cs *chunkStream) sendWindowAcknowledgementSize(cnt uint32) error {
	buf := new(bytes.Buffer)
	err := binary.Write(buf, binary.BigEndian, cnt)

	if err != nil {
		return err
	}

	msg := &Message{
		ChunkStreamID:     CS_ID_PROTOCOL_CONTROL,
		Type:              ACKNOWLEDGEMENT,
		StreamID:          0,
		AbsoluteTimestamp: 0,
		Buf:               buf,
		Size:              uint32(buf.Len()),
	}

	return cs.Write(msg)
}

/*func (cs *chunkStream) handleMessage(msg *Message) {

}*/
