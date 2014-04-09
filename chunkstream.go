package rtmp

import (
	"bytes"
	"encoding/binary"
)

type ChunkStream interface {
	ChunkStreamReader
	ChunkStreamWriter

	SendSetChunkSize(uint32) error
	SendSetWindowSize(uint32) error
	SendSetPeerBandwidth(uint32, uint8) error
	SendAcknowledgement(uint32) error
}

type ChunkStreamHandler interface {
	OnMessage(msg *Message)
	OnWindowFull(uint32)
}

type chunkStream struct {
	ChunkStreamReader
	ChunkStreamWriter
}

func NewChunkStream(handler ChunkStreamHandler) ChunkStream {
	return &chunkStream{
		ChunkStreamReader: NewChunkStreamReader(handler),
		ChunkStreamWriter: NewChunkStreamWriter(),
	}
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

	return cs.Send(msg)
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

	return cs.Send(msg)
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

	return cs.Send(msg)
}

func (cs *chunkStream) SendAcknowledgement(inCount uint32) error {
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

	return cs.Send(msg)
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

	return cs.Send(msg)
}
