package rtmp

import (
	"fmt"
)

type StreamInvoker struct {
	Stream  Stream
	Invoker Invoker
	Func    func(Stream, Command, Invoker) error
}

func (si *StreamInvoker) Invoke(cmd Command) error {
	return si.Func(si.Stream, cmd, si.Invoker)
}

type Stream interface {
	ID() uint32
	Conn() Conn
	Name() string
	Publishing() bool
	Playing() bool

	Send(*Message) error
	SendCommand(Command) error

	SetHandler(StreamHandler)
}

type StreamHandler interface {
	OnPlay(Stream)
	OnPublish(Stream)

	OnReceive(Stream, *Message)
	Invoke(Stream, Command, Invoker) error
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

func newStream(id uint32, conn Conn, csid uint32) *stream {
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

func (s *stream) Playing() bool {
	return s.playing
}

func (s *stream) Publishing() bool {
	return s.publishing
}

func (s *stream) Send(msg *Message) error {
	msg.ChunkStreamID = s.csid
	msg.StreamID = s.id

	return s.conn.Send(msg)
}

func (s *stream) SendCommand(cmd Command) error {
	msg, err := cmd.RawCommand().Message()
	if err != nil {
		return err
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

func (s *stream) OnReceive(msg *Message) {
	if s.handler != nil {
		s.handler.OnReceive(s, msg)
	}
}

func (s *stream) OnCommand(cmd Command) error {
	if s.handler != nil {
		return s.handler.Invoke(s, cmd, s)
	} else {
		return s.Invoke(cmd)
	}
}

func (s *stream) Invoke(cmd Command) error {
	switch cmd := cmd.(type) {
	case PublishCommand:
		return s.invokePublish(cmd)
	case PlayCommand:
		return s.invokePlay(cmd)
	default:
		return nil
	}
}

func (s *stream) invokePublish(cmd PublishCommand) error {
	if len(cmd.Name) == 0 {
		return ErrPublishBadName(fmt.Errorf("invalid publish stream name"))
	}

	err := s.conn.SendCommand(OnStatusCommand{
		StreamID:      cmd.StreamID,
		TransactionID: cmd.TransactionID,
		Info:          StatusPublishStart(fmt.Sprintf("Publishing %s.", cmd.Name)),
	})

	if err != nil {
		return err
	}

	s.name = cmd.Name
	s.publishing = true

	if s.handler != nil {
		s.handler.OnPublish(s)
	}
	return nil
}

func (s *stream) invokePlay(cmd PlayCommand) error {
	if len(cmd.Name) == 0 {
		return ErrPlayFailed(fmt.Errorf("invalid play stream name"))
	}

	err := s.SendCommand(OnStatusCommand{
		Info: NetStreamPlayInfo{
			Status:  StatusPlayReset("reset"),
			Details: cmd.Name,
		},
	})

	if err != nil {
		return err
	}

	err = s.SendCommand(OnStatusCommand{
		Info: NetStreamPlayInfo{
			Status:  StatusPlayStart("play"),
			Details: cmd.Name,
		},
	})

	if err != nil {
		return err
	}

	s.name = cmd.Name
	s.playing = true

	if s.handler != nil {
		s.handler.OnPlay(s)
	}
	return nil
}
