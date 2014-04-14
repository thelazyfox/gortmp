package rtmp

import (
	"encoding/binary"
	"errors"
	"io"
)

var (
	ErrInvalidChunkHeader = errors.New("chunk error: invalid header")
)

type ChunkStreamReader interface {
	ReadFrom(r Reader) (int64, error)
}

type ChunkStreamReaderHandler interface {
	OnMessage(*Message)
	OnWindowFull(uint32)
}

type inChunkStream struct {
	id                    uint32
	lastHeader            *Header
	lastAbsoluteTimestamp uint32

	currentMessage *Message
}

type chunkStreamReader struct {
	chunkSize  uint32
	windowSize uint32

	messageQueue chan *Message
	chunkStreams map[uint32]*inChunkStream

	handler ChunkStreamReaderHandler
}

func NewChunkStreamReader(handler ChunkStreamReaderHandler) ChunkStreamReader {
	return &chunkStreamReader{
		chunkSize:    DEFAULT_CHUNK_SIZE,
		windowSize:   DEFAULT_WINDOW_SIZE,
		chunkStreams: make(map[uint32]*inChunkStream),
		handler:      handler,
	}
}

func (csr *chunkStreamReader) ReadFrom(r Reader) (int64, error) {
	var inBytes int64
	var inBytesPreWindow int64

	for {
		n, err := csr.readChunk(r)
		inBytes += n

		if err != nil {
			return inBytes, err
		}

		if inBytes >= (inBytesPreWindow + int64(csr.windowSize)) {
			csr.handler.OnWindowFull(uint32(inBytes))
			inBytesPreWindow = inBytes
		}
	}
}

func (csr *chunkStreamReader) handleMessage(msg *Message) {
	if msg.ChunkStreamID == CS_ID_PROTOCOL_CONTROL {
		switch msg.Type {
		case SET_CHUNK_SIZE:
			buf := make([]byte, 4)
			msg.Buf.Peek(buf)
			csr.chunkSize = binary.BigEndian.Uint32(buf)
		case WINDOW_ACKNOWLEDGEMENT_SIZE:
			buf := make([]byte, 4)
			msg.Buf.Peek(buf)
			csr.windowSize = binary.BigEndian.Uint32(buf)
		case ABORT_MESSAGE:
			// not supported
		}
	}

	csr.handler.OnMessage(msg)
}

func (csr *chunkStreamReader) readChunk(r Reader) (int64, error) {
	var inCount int64
	// Read base header
	n, vfmt, csi, err := ReadBaseHeader(r)
	inCount += int64(n)
	if err != nil {
		return inCount, err
	}

	// Get the chunk stream
	chunkstream, found := csr.chunkStreams[csi]
	if !found || chunkstream == nil {
		chunkstream = &inChunkStream{id: csi}
		csr.chunkStreams[csi] = chunkstream
	}

	// Read header
	header := &Header{}
	n, err = header.ReadHeader(r, vfmt, csi, chunkstream.lastHeader)
	if err != nil {
		return inCount, err
	}
	inCount += int64(n)

	var absoluteTimestamp uint32
	var message *Message
	switch vfmt {
	case HEADER_FMT_FULL:
		chunkstream.lastHeader = header
		absoluteTimestamp = header.Timestamp
	case HEADER_FMT_SAME_STREAM:
		if chunkstream.lastHeader == nil {
			return inCount, ErrInvalidChunkHeader
		}

		header.MessageStreamID = chunkstream.lastHeader.MessageStreamID
		chunkstream.lastHeader = header
		absoluteTimestamp = chunkstream.lastAbsoluteTimestamp + header.Timestamp
	case HEADER_FMT_SAME_LENGTH_AND_STREAM:
		if chunkstream.lastHeader == nil {
			return inCount, ErrInvalidChunkHeader
		}

		header.MessageStreamID = chunkstream.lastHeader.MessageStreamID
		header.MessageLength = chunkstream.lastHeader.MessageLength
		header.MessageTypeID = chunkstream.lastHeader.MessageTypeID
		chunkstream.lastHeader = header
		absoluteTimestamp = chunkstream.lastAbsoluteTimestamp + header.Timestamp
	case HEADER_FMT_CONTINUATION:
		if chunkstream.currentMessage != nil {
			// Continuation the previous unfinished message
			message = chunkstream.currentMessage
		}
		if chunkstream.lastHeader == nil {
			return inCount, ErrInvalidChunkHeader
		}
		header.MessageStreamID = chunkstream.lastHeader.MessageStreamID
		header.MessageLength = chunkstream.lastHeader.MessageLength
		header.MessageTypeID = chunkstream.lastHeader.MessageTypeID
		header.Timestamp = chunkstream.lastHeader.Timestamp

		chunkstream.lastHeader = header
		absoluteTimestamp = chunkstream.lastAbsoluteTimestamp
	}

	if message == nil {
		message = &Message{
			ChunkStreamID:     csi,
			Type:              header.MessageTypeID,
			Timestamp:         header.RealTimestamp(),
			Size:              header.MessageLength,
			StreamID:          header.MessageStreamID,
			Buf:               NewDynamicBuffer(),
			IsInbound:         true,
			AbsoluteTimestamp: absoluteTimestamp,
		}
	}

	chunkstream.lastAbsoluteTimestamp = absoluteTimestamp

	remain := message.Remain()
	lastChunk := true

	if remain > csr.chunkSize {
		lastChunk = false
		remain = csr.chunkSize
	}

	// TODO: read straight into the buffer
	n64, err := io.CopyN(message.Buf, r, int64(remain))
	inCount += n64

	if err != nil {
		return inCount, err
	}

	if lastChunk {
		csr.handleMessage(message)
		chunkstream.currentMessage = nil
	} else {
		chunkstream.currentMessage = message
	}

	return inCount, nil
}
