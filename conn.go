package rtmp

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
)

// Conn

type Invoker interface {
	Invoke(Command) error
}

type ConnInvoker struct {
	Conn    Conn
	Invoker Invoker
	Func    func(Conn, Command, Invoker) error
}

func (ci *ConnInvoker) Invoke(cmd Command) error {
	return ci.Func(ci.Conn, cmd, ci.Invoker)
}

type Conn interface {
	// basics
	Send(msg *Message) error
	SendCommand(cmd Command) error
	Fatal(err error)
	Error(err ConnError)
	Close()

	// probably should be private
	SendStreamBegin(uint32)

	// net ops
	Flush() error
	Addr() net.Addr

	// useful properties
	App() string

	SetChunkSize(uint32)
	SetWindowSize(uint32)
	SetPeerBandwidth(uint32, uint8)
}

type ConnHandler interface {
	OnConnect(Conn)

	OnCreateStream(Stream)
	OnDestroyStream(Stream)

	OnClose(Conn, error)

	OnReceive(Conn, *Message)
	Invoke(Conn, Command, Invoker) error
}

type conn struct {
	netConn     NetConn
	chunkStream ChunkStream
	handler     ConnHandler

	app string

	streams map[uint32]*stream

	readDone  chan error
	writeDone chan error

	log Logger
}

func NewConn(netConn NetConn, handler ConnHandler) Conn {
	logTag := fmt.Sprintf("Conn(%s)", netConn.Conn().RemoteAddr())
	c := &conn{
		netConn:   netConn,
		handler:   handler,
		streams:   make(map[uint32]*stream),
		readDone:  make(chan error),
		writeDone: make(chan error),
		log:       NewLogger(logTag),
	}

	c.chunkStream = NewChunkStream(c)

	go c.readFrom()
	go c.writeTo()
	go c.waitClose()

	return c
}

func (c *conn) Send(msg *Message) error {
	//TODO implement better
	return c.chunkStream.Send(msg)
}

func (c *conn) SendCommand(cmd Command) error {
	c.log.Tracef("SendCommand(%+v)", cmd)

	msg, err := cmd.RawCommand().Message()
	if err != nil {
		return err
	}

	c.log.Tracef("SendCommand msg=%+v", *msg)

	err = c.Send(msg)
	if err != nil {
		return err
	}

	return c.netConn.Flush()
}

func (c *conn) Addr() net.Addr {
	return c.netConn.Conn().RemoteAddr()
}

func (c *conn) Flush() error {
	return c.netConn.Flush()
}

func (c *conn) App() string {
	return c.app
}

func (c *conn) Fatal(err error) {
	c.Error(NewConnErrorStatus(err, StatusConnectClosed(err.Error()), true))
}

// all errors are fatal unless otherwise specified
func (c *conn) Error(err ConnError) {
	c.log.Errorf("Error(%s)", err)

	c.SendCommand(err.Command())

	if err.IsFatal() {
		c.log.Errorf("fatal connection error: %s", err)
		c.Close()
	} else {
		c.log.Errorf("connection error: %s", err)
	}
}

func (c *conn) Close() {
	c.netConn.Close()
}

func (c *conn) SendStreamBegin(streamId uint32) {
	buf := new(bytes.Buffer)

	err := binary.Write(buf, binary.BigEndian, EVENT_STREAM_BEGIN)
	if err != nil {
		c.Fatal(err)
		return
	}

	err = binary.Write(buf, binary.BigEndian, streamId)
	if err != nil {
		c.Fatal(err)
		return
	}

	msg := &Message{
		ChunkStreamID:     CS_ID_PROTOCOL_CONTROL,
		Timestamp:         0,
		Size:              uint32(buf.Len()),
		Type:              USER_CONTROL_MESSAGE,
		StreamID:          0,
		Buf:               buf,
		AbsoluteTimestamp: 0,
	}

	err = c.chunkStream.Send(msg)
	if err != nil {
		c.Fatal(err)
	}
}

func (c *conn) SetChunkSize(size uint32) {
	err := func() error {
		err := c.chunkStream.SendSetChunkSize(size)
		if err != nil {
			return err
		}

		return c.netConn.Flush()

	}()

	if err != nil {
		c.log.Errorf("SetChunkSize failed with error: %s", err)
		c.Fatal(err)
	}
}

func (c *conn) SetWindowSize(size uint32) {
	err := func() error {
		err := c.chunkStream.SendSetWindowSize(size)
		if err != nil {
			return err
		}

		return c.netConn.Flush()
	}()

	if err != nil {
		c.Fatal(fmt.Errorf("SetWindowSize failed: %s", err))
	}
}

func (c *conn) SetPeerBandwidth(size uint32, limit uint8) {
	err := func() error {
		err := c.chunkStream.SendSetPeerBandwidth(size, limit)
		if err != nil {
			return err
		}

		return c.netConn.Flush()
	}()

	if err != nil {
		c.Fatal(fmt.Errorf("SetPeerBandwidthFailed: %s", err))
	}
}

func (c *conn) OnMessage(msg *Message) {
	c.log.Tracef("OnMessage(%+v)", msg)
	switch msg.Type {
	case COMMAND_AMF3:
		fallthrough
	case COMMAND_AMF0:
		cmd, err := ParseCommand(msg)
		if err != nil {
			objs := dumpAmf(msg)
			c.log.Warnf("invalid command message: %s -- %+v", err, objs)
			c.OnReceive(msg)
		} else {
			c.OnCommand(cmd)
		}
	default:
		c.OnReceive(msg)
	}
}

func (c *conn) OnWindowFull(count uint32) {
	// pop this in a goroutine to avoid blocking the send loop
	go func() {
		err := c.chunkStream.SendAcknowledgement(count)
		if err != nil {
			c.Fatal(fmt.Errorf("OnWindowFull error: %s", err))
		}

		err = c.Flush()
		if err != nil {
			c.Fatal(fmt.Errorf("OnWindowFull flush error: %s", err))
		}
	}()
}

func (c *conn) OnReceive(msg *Message) {
	if msg.StreamID == 0 {
		c.handler.OnReceive(c, msg)
	} else {
		stream := c.streams[msg.StreamID]

		if stream != nil {
			stream.OnReceive(msg)
		} else {
			c.Fatal(fmt.Errorf("invalid message stream id"))
		}
	}
}

func (c *conn) Invoke(cmd Command) error {
	switch cmd := cmd.(type) {
	case ConnectCommand:
		return c.invokeConnect(cmd)
	case CreateStreamCommand:
		return c.invokeCreateStream(cmd)
	case DeleteStreamCommand:
		return c.invokeDeleteStream(cmd)
	case FCPublishCommand:
		return c.invokeFCPublish(cmd)
	}

	return nil
}

func (c *conn) invokeConnect(cmd ConnectCommand) error {
	app, ok := cmd.Properties["app"].(string)
	if !ok {
		return ErrConnectRejected(fmt.Errorf("invalid app: %#v", cmd.Properties["app"]))
	}

	c.app = app

	c.SetWindowSize(2500000)
	c.SetPeerBandwidth(2500000, BANDWIDTH_LIMIT_DYNAMIC)
	c.SendStreamBegin(0)
	c.SetChunkSize(4096)

	err := c.SendCommand(ResultCommand{
		TransactionID: cmd.TransactionID,
		Properties:    DefaultConnectProperties,
		Info:          DefaultConnectInformation,
	})

	if err != nil {
		return err
	}

	c.handler.OnConnect(c)
	return nil
}

func (c *conn) invokeCreateStream(cmd CreateStreamCommand) error {
	c.log.Tracef("invokeCreateStream(%#v)", cmd)

	csid, err := c.chunkStream.CreateChunkStream(LowPriority)
	if err != nil {
		return err
	}

	stream := c.allocateStream(csid)

	err = c.SendCommand(ResultCommand{
		TransactionID: cmd.TransactionID,
		Info:          stream.ID(),
	})

	if err != nil {
		return err
	}

	c.SendStreamBegin(stream.ID())
	c.handler.OnCreateStream(stream)
	return nil
}

func (c *conn) invokeDeleteStream(cmd DeleteStreamCommand) error {
	c.log.Tracef("invokeDeleteStream(%#v)", cmd)

	stream, ok := c.deleteStream(cmd.DeleteStreamID)
	if !ok {
		return ErrCallFailed(fmt.Errorf("no such stream id: %d", cmd.DeleteStreamID), true)
	}

	c.handler.OnDestroyStream(stream)
	return nil
}

func (c *conn) invokeFCPublish(cmd FCPublishCommand) error {
	if len(cmd.Name) == 0 {
		return ErrPublishBadName(fmt.Errorf("invalid stream name"))
	}

	return c.SendCommand(OnFCPublishCommand{
		Status: StatusPublishStart(fmt.Sprintf("FCPublish to stream %s", cmd.Name)),
	})
}

func (c *conn) allocateStream(csid uint32) Stream {
	for i := uint32(1); ; i += 1 {
		if s := c.streams[i]; s == nil {
			c.streams[i] = newStream(i, c, csid)
			return c.streams[i]
		}
	}
}

func (c *conn) deleteStream(streamid uint32) (Stream, bool) {
	stream, ok := c.streams[streamid]
	if !ok {
		return nil, false
	}

	delete(c.streams, streamid)
	return stream, true
}

func (c *conn) OnCommand(cmd Command) {
	var err error

	raw := cmd.RawCommand()

	if raw.StreamID == 0 {
		err = c.handler.Invoke(c, cmd, c)
	} else {
		stream := c.streams[raw.StreamID]
		if stream != nil {
			err = stream.OnCommand(cmd)
		} else {
			err = fmt.Errorf("invalid stream id specified")
		}
	}

	if err != nil {
		c.Fatal(err)
	}
}

func (c *conn) waitClose() {
	var err error

	select {
	case err = <-c.readDone:
	case err = <-c.writeDone:
	}

	// shut down when read or write ends
	c.netConn.Close()
	c.chunkStream.Close()

	select {
	case <-c.readDone:
	case <-c.writeDone:
	}

	for _, stream := range c.streams {
		c.handler.OnDestroyStream(stream)
	}

	c.handler.OnClose(c, err)
}

func (c *conn) readFrom() {
	_, err := c.chunkStream.ReadFrom(c.netConn)
	c.readDone <- err
}

func (c *conn) writeTo() {
	_, err := c.chunkStream.WriteTo(c.netConn)
	c.writeDone <- err
}
