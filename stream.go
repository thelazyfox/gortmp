package rtmp

import (
	"bytes"
	"fmt"
	"github.com/zhangpeihao/goamf"
)

type Stream interface {
	ID() uint32
	Conn() Conn
	Name() string

	Send(*Message) error
	SendCommand(*Command) error

	SetHandler(StreamHandler)

	onReceive(*Message)
	invoke(*Command) error
}

type StreamHandler interface {
	OnPlay(Stream)
	OnPublish(Stream)

	OnReceive(Stream, *Message)
	Invoke(Stream, *Command, func(*Command) error) error
}

type stream struct {
	id   uint32
	csid uint32
	conn Conn

	name       string
	publishing bool
	playing    bool

	handler StreamHandler
}

func NewStream(id uint32, conn Conn, csid uint32) Stream {
	return &stream{
		id:   id,
		conn: conn,
		csid: csid,
	}
}

func (s *stream) ID() uint32 {
	return s.id
}

func (s *stream) Conn() Conn {
	return s.conn
}

func (s *stream) Name() string {
	return s.name
}

func (s *stream) Send(msg *Message) error {
	msg.ChunkStreamID = s.csid
	msg.StreamID = s.id

	return s.conn.Send(msg)
}

func (s *stream) SendCommand(cmd *Command) error {
	buf := new(bytes.Buffer)
	err := cmd.Write(buf)
	if err != nil {
		return err
	}

	msg := &Message{
		StreamID:      cmd.StreamID,
		ChunkStreamID: CS_ID_COMMAND,
		Type:          COMMAND_AMF0,
		Size:          uint32(buf.Len()),
		Buf:           buf,
	}

	err = s.Send(msg)
	if err != nil {
		return err
	}

	return s.conn.Flush()
}

func (s *stream) SetHandler(handler StreamHandler) {
	s.handler = handler
}

func (s *stream) onReceive(msg *Message) {
	if s.handler != nil {
		s.handler.OnReceive(s, msg)
	}
}

func (s *stream) invoke(cmd *Command) error {
	if s.handler != nil {
		return s.handler.Invoke(s, cmd, s.Invoke)
	} else {
		return s.Invoke(cmd)
	}
}

func (s *stream) Invoke(cmd *Command) error {
	switch cmd.Name {
	case "publish":
		return s.invokePublish(cmd)
	case "play":
		return s.invokePlay(cmd)
	}

	return nil
}

func (s *stream) invokePublish(cmd *Command) error {
	streamName, ok := func() (string, bool) {
		if cmd.Objects == nil || len(cmd.Objects) != 3 {
			return "", false
		}

		name, ok := cmd.Objects[1].(string)
		if !ok || len(name) == 0 {
			return "", false
		}

		return name, true
	}()

	if !ok {
		return NewErrorResponse(&Command{
			Name:          "onStatus",
			StreamID:      cmd.StreamID,
			TransactionID: cmd.TransactionID,
			Objects: []interface{}{
				nil,
				amf.Object{
					"level":       "error",
					"code":        NETSTREAM_PUBLISH_BADNAME,
					"description": fmt.Sprintf("invalid stream name"),
				},
			},
		})
	}

	s.name = streamName
	s.publishing = true

	err := s.conn.SendCommand(&Command{
		Name:          "onStatus",
		StreamID:      0,
		TransactionID: cmd.TransactionID,
		Objects: []interface{}{
			nil,
			amf.Object{
				"level":       "status",
				"code":        "NetStream.Publish.Start",
				"description": fmt.Sprintf("Publishing %s.", streamName),
			},
		},
	})

	if err != nil {
		return fmt.Errorf("no play stream specified")
	}

	if s.handler != nil {
		s.handler.OnPublish(s)
	}
	return nil
}

func (s *stream) invokePlay(cmd *Command) error {
	streamName, ok := func() (string, bool) {
		if cmd.Objects == nil || len(cmd.Objects) < 2 {
			return "", false
		}

		name, ok := cmd.Objects[1].(string)
		return name, ok
	}()

	if !ok {
		return fmt.Errorf("no play stream specified")
	}

	var err error
	err = s.conn.SendCommand(&Command{
		Name:          "onStatus",
		TransactionID: 0,
		Objects: []interface{}{
			nil,
			amf.Object{
				"level":       "status",
				"code":        NETSTREAM_PLAY_RESET,
				"description": fmt.Sprintf("resetting stream %s", streamName),
				"details":     streamName,
			},
		},
	})

	if err != nil {
		return err
	}

	err = s.conn.SendCommand(&Command{
		Name:          "onStatus",
		TransactionID: 0,
		Objects: []interface{}{
			nil,
			amf.Object{
				"level":       "status",
				"code":        NETSTREAM_PLAY_RESET,
				"description": fmt.Sprintf("playing stream %s", streamName),
				"details":     streamName,
			},
		},
	})

	if err != nil {
		return err
	}

	s.name = streamName

	s.handler.OnPlay(s)
	return nil
}
