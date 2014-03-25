package rtmp

type Stream interface {
	ID() uint32
	Conn() Conn

	Send(*Message) error
}

type stream struct {
	id   uint32
	csid uint32
	conn Conn
}

func NewStream(id uint32, conn Conn) Stream {
	return &stream{
		id:   id,
		conn: conn,
	}
}

func (s *stream) ID() uint32 {
	return s.id
}

func (s *stream) Conn() Conn {
	return s.conn
}

func (s *stream) Send(msg *Message) error {
	msg.ChunkStreamID = s.csid
	msg.StreamID = s.id

	return s.conn.Send(msg)
}
