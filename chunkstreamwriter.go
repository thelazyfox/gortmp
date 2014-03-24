package rtmp

import (
	"encoding/binary"
	"fmt"
	"github.com/thelazyfox/gortmp/log"
	"io"
	"net"
	"sync"
)

type ChunkStreamPriority int

const (
	HighPriority   ChunkStreamPriority = iota
	MediumPriority                     = iota
	LowPriority                        = iota
)

type ChunkStreamWriter interface {
	WriteTo(w Writer) (int64, error)
	Write(*Message) error

	CreateChunkStream(ChunkStreamPriority) (uint32, error)
}

type messageWriter func(w Writer) (int64, error)

type chunkStreamWriter struct {
	chunkSize  uint32
	windowSize uint32

	chunkStreams      map[uint32]*outChunkStream
	chunkStreamsMutex sync.RWMutex

	highPriority   chan messageWriter
	mediumPriority chan messageWriter
	lowPriority    chan messageWriter

	done chan bool
}

type outChunkStream struct {
	lastHeader            *Header
	lastAbsoluteTimestamp uint32

	currentMessage *Message
	messageQueue   chan messageWriter
}

func NewChunkStreamWriter() ChunkStreamWriter {
	high := make(chan messageWriter)
	medium := make(chan messageWriter)
	low := make(chan messageWriter)

	csw := &chunkStreamWriter{
		chunkSize:  DEFAULT_CHUNK_SIZE,
		windowSize: DEFAULT_WINDOW_SIZE,
		chunkStreams: map[uint32]*outChunkStream{
			CS_ID_PROTOCOL_CONTROL: &outChunkStream{messageQueue: high},
			CS_ID_COMMAND:          &outChunkStream{messageQueue: high},
			CS_ID_USER_CONTROL:     &outChunkStream{messageQueue: medium},
		},
		highPriority:   high,
		mediumPriority: medium,
		lowPriority:    low,
		done:           make(chan bool),
	}

	return csw
}

func (csw *chunkStreamWriter) CreateChunkStream(p ChunkStreamPriority) (uint32, error) {
	// make sure the priority is valid
	var queue chan messageWriter
	switch p {
	case HighPriority:
		queue = csw.highPriority
	case MediumPriority:
		queue = csw.mediumPriority
	case LowPriority:
		queue = csw.lowPriority
	default:
		return 0, fmt.Errorf("invalid chunk stream priority")
	}

	csw.chunkStreamsMutex.Lock()
	defer csw.chunkStreamsMutex.Unlock()

	var i uint32 = CS_ID_BASE

	for i < CS_ID_MAX {
		_, found := csw.chunkStreams[i]
		if !found {
			csw.chunkStreams[i] = &outChunkStream{messageQueue: queue}
			return i, nil
		} else {
			i += 1
		}
	}

	return 0, fmt.Errorf("maximum number of chunk streams reached")
}

func (csw *chunkStreamWriter) WriteTo(w Writer) (int64, error) {
	var outBytes int64
	var writer messageWriter

	for {
		select {
		case writer = <-csw.highPriority:
		case writer = <-csw.mediumPriority:
		case writer = <-csw.lowPriority:
		}

		n, err := writer(w)
		outBytes += n

		if err != nil {
			return outBytes, err
		}
	}

	return outBytes, nil
}

func (csw *chunkStreamWriter) Write(msg *Message) error {
	log.Trace("Write: %+v", msg)
	csw.chunkStreamsMutex.RLock()
	cs, ok := csw.chunkStreams[msg.ChunkStreamID]
	csw.chunkStreamsMutex.RUnlock()

	if !ok {
		return fmt.Errorf("invalid chunk stream specified")
	}

	done := make(chan error)
	writer := func(w Writer) (int64, error) {
		_, err := csw.writeMessage(w, msg, cs)
		done <- err
		return 0, nil
	}

	select {
	case cs.messageQueue <- writer:
		select {
		case err := <-done:
			return err
		}
	}

}

func (csw *chunkStreamWriter) writeMessage(w Writer, msg *Message, cs *outChunkStream) (int64, error) {
	log.Trace("writeMessage: %x %#v", msg.Buf.Bytes(), *msg)
	var written int64

	cs.currentMessage = msg
	for msg.Buf.Len() > 0 {
		n, err := csw.writeChunk(w, cs)
		written += n
		if err != nil {
			return written, err
		}
	}

	return written, nil
}

func (csw *chunkStreamWriter) checkSetChunkSize(msg *Message) {
	if msg.ChunkStreamID == CS_ID_PROTOCOL_CONTROL && msg.Type == SET_CHUNK_SIZE {
		csw.chunkSize = binary.BigEndian.Uint32(msg.Buf.Bytes())
	}
}

func (csw *chunkStreamWriter) writeChunk(w Writer, cs *outChunkStream) (int64, error) {
	count := int64(0)

	var header *Header
	if uint32(cs.currentMessage.Buf.Len()) != cs.currentMessage.Size {
		// continuation
		header = &Header{
			ChunkStreamID: cs.currentMessage.ChunkStreamID,
			Fmt:           HEADER_FMT_CONTINUATION,
		}
	} else {
		header = cs.newHeader()
	}

	n, err := header.Write(w)
	count += int64(n)

	if err != nil {
		log.Debug("writeChunk error writing header: %s", err)
		return count, err
	}

	var remain uint32 = uint32(cs.currentMessage.Buf.Len())

	if remain > csw.chunkSize {
		remain = csw.chunkSize
	}

	log.Trace("writeChunk msg=%#v", *cs.currentMessage)
	log.Trace("writeChunk remain = %d", remain)

	var tmpChunkSize uint32

	if cs.currentMessage.Type == SET_CHUNK_SIZE {
		tmpChunkSize = binary.BigEndian.Uint32(cs.currentMessage.Buf.Bytes())
	}

	var copied uint32
	for copied < remain {
		n, err := io.CopyN(w, cs.currentMessage.Buf, int64(remain))
		copied += uint32(n)

		if err != nil {
			netErr, ok := err.(net.Error)
			if !ok || !netErr.Temporary() {
				return int64(copied) + count, err
			}
		}

		remain -= uint32(n)
	}

	if tmpChunkSize != 0 {
		csw.chunkSize = tmpChunkSize
	}

	log.Trace("writeChunk len=%d", int64(copied)+count)
	return int64(copied) + count, nil
}

func (cs *outChunkStream) newHeader() *Header {
	msg := cs.currentMessage
	header := &Header{
		ChunkStreamID:   msg.ChunkStreamID,
		MessageLength:   uint32(msg.Size),
		MessageTypeID:   msg.Type,
		MessageStreamID: msg.StreamID,
	}

	timestamp := msg.Timestamp
	deltaTimestamp := uint32(0)
	if cs.lastAbsoluteTimestamp < msg.Timestamp {
		deltaTimestamp = msg.Timestamp - cs.lastAbsoluteTimestamp
	}
	if cs.lastHeader == nil {
		header.Fmt = HEADER_FMT_FULL
		header.Timestamp = timestamp
	} else {

		if header.MessageStreamID == cs.lastHeader.MessageStreamID {
			if header.MessageTypeID == cs.lastHeader.MessageTypeID &&
				header.MessageLength == cs.lastHeader.MessageLength {
				switch cs.lastHeader.Fmt {
				case HEADER_FMT_FULL:
					header.Fmt = HEADER_FMT_SAME_LENGTH_AND_STREAM
					header.Timestamp = deltaTimestamp
				case HEADER_FMT_SAME_STREAM:
					fallthrough
				case HEADER_FMT_SAME_LENGTH_AND_STREAM:
					fallthrough
				case HEADER_FMT_CONTINUATION:
					if cs.lastHeader.Timestamp == deltaTimestamp {
						header.Fmt = HEADER_FMT_CONTINUATION
					} else {
						header.Fmt = HEADER_FMT_SAME_LENGTH_AND_STREAM
						header.Timestamp = deltaTimestamp
					}
				}
			} else {
				header.Fmt = HEADER_FMT_SAME_STREAM
				header.Timestamp = deltaTimestamp
			}
		} else {
			header.Fmt = HEADER_FMT_FULL
			header.Timestamp = timestamp
		}
	}
	// Check extended timestamp
	if header.Timestamp >= 0xffffff {
		header.ExtendedTimestamp = msg.Timestamp
		header.Timestamp = 0xffffff
	} else {
		header.ExtendedTimestamp = 0
	}

	log.Trace("header = %#v", *header)

	cs.lastHeader = header
	cs.lastAbsoluteTimestamp = timestamp
	return header
}
