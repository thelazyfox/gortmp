package rtmp

import (
	"encoding/binary"
	"errors"
	"io"
	"sync"
)

const (
	HighPriority   = iota
	MediumPriority = iota
	LowPriority    = iota
)

var (
	ErrInvalidPriority      = errors.New("chunk error: invalid priority")
	ErrInvalidChunkStreamID = errors.New("chunk error: stream id")
	ErrInvalidChunk         = errors.New("chunk error: invalid chunk")
	ErrMaxChunkStreams      = errors.New("chunk error: maximum chunk streams reached")
	ErrClosedChunkStream    = errors.New("chunk error: all chunk streams closed")
)

type ChunkStreamWriter interface {
	WriteTo(w Writer) (int64, error)
	Send(*Message) error
	Close()

	CreateChunkStream(int) (uint32, error)
}

type messageWriter func(w Writer) (int64, error)

type chunkStreamWriter struct {
	chunkSize  uint32
	windowSize uint32

	chunkStreams map[uint32]*outChunkStream

	highPriority   chan messageWriter
	mediumPriority chan messageWriter
	lowPriority    chan messageWriter

	mu sync.RWMutex

	done     chan bool
	doneOnce sync.Once
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

func (csw *chunkStreamWriter) CreateChunkStream(p int) (uint32, error) {
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
		return 0, ErrInvalidPriority
	}

	csw.mu.Lock()
	defer csw.mu.Unlock()

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

	return 0, ErrMaxChunkStreams
}

func (csw *chunkStreamWriter) Close() {
	csw.doneOnce.Do(func() {
		close(csw.done)
	})
}

func (csw *chunkStreamWriter) WriteTo(w Writer) (int64, error) {
	var outBytes int64
	var writer messageWriter

	for {
		select {
		case writer = <-csw.highPriority:
		case writer = <-csw.mediumPriority:
		case writer = <-csw.lowPriority:
		case <-csw.done:
			return outBytes, nil
		}

		n, err := writer(w)
		outBytes += n

		if err != nil {
			return outBytes, err
		}
	}
}

func (csw *chunkStreamWriter) Send(msg *Message) error {
	csw.mu.RLock()
	cs, ok := csw.chunkStreams[msg.ChunkStreamID]
	csw.mu.RUnlock()

	if !ok {
		return ErrInvalidChunkStreamID
	}

	done := make(chan error)
	writer := func(w Writer) (int64, error) {
		_, err := csw.writeMessage(w, msg, cs)
		done <- err
		return 0, nil
	}

	select {
	case cs.messageQueue <- messageWriter(writer):
		select {
		case err := <-done:
			return err
		}
	case <-csw.done:
		return ErrClosedChunkStream
	}

}

func (csw *chunkStreamWriter) writeMessage(w Writer, msg *Message, cs *outChunkStream) (int64, error) {
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
		return count, err
	}

	var remain uint32 = uint32(cs.currentMessage.Buf.Len())

	if remain > csw.chunkSize {
		remain = csw.chunkSize
	}

	var tmpChunkSize uint32

	if cs.currentMessage.Type == SET_CHUNK_SIZE {
		tmpChunkSize = binary.BigEndian.Uint32(cs.currentMessage.Buf.Bytes())
	}

	n64, err := io.CopyN(w, cs.currentMessage.Buf, int64(remain))
	count += n64

	if err != nil {
		return count, err
	}

	if tmpChunkSize != 0 {
		csw.chunkSize = tmpChunkSize
	}

	return count, nil
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
	} else {
		// set lastHeader to nil to force a full header
		// because we can't have a negative delta
		cs.lastHeader = nil
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

	cs.lastHeader = header
	cs.lastAbsoluteTimestamp = timestamp
	return header
}
