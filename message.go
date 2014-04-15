// Copyright 2013, zhangpeihao All rights reserved.

package rtmp

import (
	"bytes"
)

// Message
//
// The different types of messages that are exchanged between the server
// and the client include audio messages for sending the audio data,
// video messages for sending video data, data messages for sending any
// user data, shared object messages, and command messages.
type Message struct {
	ChunkStreamID     uint32
	Timestamp         uint32
	Size              uint32
	Type              uint8
	StreamID          uint32
	Buf               *bytes.Buffer
	IsInbound         bool
	AbsoluteTimestamp uint32
}

// The length of remain data to read
func (message *Message) Remain() uint32 {
	if message.Buf == nil {
		return message.Size
	}
	return message.Size - uint32(message.Buf.Len())
}
