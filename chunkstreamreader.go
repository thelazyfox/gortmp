package rtmp

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/thelazyfox/gortmp/log"
	"io"
	"net"
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
	log.Trace("NewChunkStreamReader")
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
			log.Info("ReadFrom exited after %d bytes", inBytes)
			return inBytes, err
		}

		log.Trace("read %d bytes", inBytes)

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
			csr.chunkSize = binary.BigEndian.Uint32(msg.Buf.Bytes())
		case WINDOW_ACKNOWLEDGEMENT_SIZE:
			csr.windowSize = binary.BigEndian.Uint32(msg.Buf.Bytes())
		case ABORT_MESSAGE:
			log.Debug("ABORT_MESSAGE not supported")
		}
	}

	csr.handler.OnMessage(msg)
}

func (csr *chunkStreamReader) readChunk(r Reader) (int64, error) {
	log.Trace("readChunk")
	var inCount int64
	// Read base header
	n, vfmt, csi, err := ReadBaseHeader(r)
	inCount += int64(n)
	if err != nil {
		log.Debug("ReadBaseHeader error: %s", err)
		return inCount, err
	}
	log.Trace("ReadBaseHeader - n: %d, vfmt: %d, csi: %d", n, vfmt, csi)

	// Get the chunk stream
	chunkstream, found := csr.chunkStreams[csi]
	if !found || chunkstream == nil {
		log.Trace("New chunk stream - csi: %d, fmt: %d", csi, vfmt)
		chunkstream = &inChunkStream{id: csi}
		csr.chunkStreams[csi] = chunkstream
	}

	// Read header
	header := &Header{}
	n, err = header.ReadHeader(r, vfmt, csi, chunkstream.lastHeader)
	if err != nil {
		log.Debug("ReadHeader error: %s", err)
		return inCount, err
	}
	inCount += int64(n)

	log.Trace("ReadHeader: %#v", *header)

	var absoluteTimestamp uint32
	var message *Message
	switch vfmt {
	case HEADER_FMT_FULL:
		chunkstream.lastHeader = header
		absoluteTimestamp = header.Timestamp
	case HEADER_FMT_SAME_STREAM:
		if chunkstream.lastHeader == nil {
			log.Debug("invalid chunk header vfmt: %d, csi: %d, lastHeader: nil", vfmt, csi)
			return inCount, fmt.Errorf("Chunk stream error: invalid header")
		}

		header.MessageStreamID = chunkstream.lastHeader.MessageStreamID
		chunkstream.lastHeader = header
		absoluteTimestamp = chunkstream.lastAbsoluteTimestamp + header.Timestamp
	case HEADER_FMT_SAME_LENGTH_AND_STREAM:
		if chunkstream.lastHeader == nil {
			log.Debug("invalid chunk header vfmt: %d, csi: %d, lastHeader: nil", vfmt, csi)
			return inCount, fmt.Errorf("Chunk stream error: invalid header")
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
			log.Debug("invalid chunk header vfmt: %d, csi: %d, lastHeader: nil", vfmt, csi)
			return inCount, fmt.Errorf("Chunk stream error: invalid header")
		}
		header.MessageStreamID = chunkstream.lastHeader.MessageStreamID
		header.MessageLength = chunkstream.lastHeader.MessageLength
		header.MessageTypeID = chunkstream.lastHeader.MessageTypeID
		header.Timestamp = chunkstream.lastHeader.Timestamp

		chunkstream.lastHeader = header
		absoluteTimestamp = chunkstream.lastAbsoluteTimestamp
	}

	log.Trace("header = %#v", *header)

	if message == nil {
		log.Trace("starting a new message")
		message = &Message{
			ChunkStreamID:     csi,
			Type:              header.MessageTypeID,
			Timestamp:         header.RealTimestamp(),
			Size:              header.MessageLength,
			StreamID:          header.MessageStreamID,
			Buf:               new(bytes.Buffer),
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

	for remain > 0 {
		n, err := io.CopyN(message.Buf, r, int64(remain))
		inCount += n
		remain -= uint32(n)

		if err != nil {
			netErr, ok := err.(net.Error)
			if !ok || !netErr.Temporary() {
				log.Debug("readChunk io error: %s", err)
				return inCount, err
			} else {
				log.Debug("readChunk temporary: %s", err)
				log.Debug("msg = %#v", *message)
				log.Debug("len = %d", message.Buf.Len())
			}
		}
	}

	if lastChunk {
		csr.handleMessage(message)
		chunkstream.currentMessage = nil
	} else {
		chunkstream.currentMessage = message
	}

	log.Trace("read %d bytes", inCount)
	return inCount, nil
}
