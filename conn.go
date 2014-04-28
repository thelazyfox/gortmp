package rtmp

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/thelazyfox/goamf"
	"net"
)

// Conn

type Invoker interface {
	Invoke(*Command) error
}

type ConnInvoker struct {
	Conn    Conn
	Invoker Invoker
	Func    func(Conn, *Command, Invoker) error
}

func (ci *ConnInvoker) Invoke(cmd *Command) error {
	return ci.Func(ci.Conn, cmd, ci.Invoker)
}

type Conn interface {
	// basics
	Send(msg *Message) error
	SendCommand(cmd *Command) error
	Error(err error)
	Fatal(err error)
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
	Invoke(Conn, *Command, Invoker) error
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

func (c *conn) SendCommand(cmd *Command) error {
	c.log.Tracef("SendCommand(%+v)", cmd)

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
	c.Error(err)
	c.log.Errorf("Fatal")
	c.Close()
}

func (c *conn) Error(err error) {
	c.log.Errorf("Error(%s)", err)
	var cmd *Command

	if errStatus, ok := err.(ErrorStatus); ok {
		cmd = &Command{
			Name: "onStatus",
			Objects: []interface{}{
				nil,
				errStatus.Status(),
			},
		}
	} else if errCmd, ok := err.(ErrorCommand); ok {
		cmd = errCmd.Command()
	} else {
		cmd = &Command{
			Name: "onStatus",
			Objects: []interface{}{
				nil,
				StatusConnectClosed(err.Error()),
			},
		}
	}

	c.SendCommand(cmd)
	c.log.Errorf("Closing connection with error: %s", err)
	c.Close()
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
		cmd, err := c.parseAmf3(bytes.NewBuffer(msg.Buf.Bytes()), msg.StreamID)
		if err != nil {
			objs := dumpAmf3(bytes.NewBuffer(msg.Buf.Bytes()))
			c.log.Warnf("Invalid COMMAND_AMF3 message: %+v", objs)
			c.OnReceive(msg)
		} else {
			c.OnCommand(cmd)
		}
	case COMMAND_AMF0:
		cmd, err := c.parseAmf0(bytes.NewBuffer(msg.Buf.Bytes()), msg.StreamID, nil)
		if err != nil {
			objs := dumpAmf0(bytes.NewBuffer(msg.Buf.Bytes()))
			c.log.Warnf("Invalid COMMAND_AMF0 message: %+v", objs)
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
			c.Error(fmt.Errorf("invalid message stream id"))
		}
	}
}

func (c *conn) Invoke(cmd *Command) error {
	switch cmd.Name {
	case "connect":
		return c.invokeConnect(cmd)
	case "createStream":
		return c.invokeCreateStream(cmd)
	case "FCPublish":
		return c.invokeFCPublish(cmd)
	}

	return nil
}

func (c *conn) invokeConnect(cmd *Command) error {
	app, err := func() (string, error) {
		if cmd.Objects == nil || len(cmd.Objects) < 1 {
			return "", fmt.Errorf("connect failed: invalid args %v", cmd.Objects)
		}

		params, ok := cmd.Objects[0].(amf.Object)
		if !ok {
			return "", fmt.Errorf("connect failed: invalid args %v", cmd.Objects)
		}

		app, ok := params["app"].(string)
		if !ok {
			return "", fmt.Errorf("connect failed: invalid app %v", params["app"])
		}
		return app, nil
	}()

	if err != nil {
		return ErrConnectRejected(err)
	}

	c.app = app

	c.SetWindowSize(2500000)
	c.SetPeerBandwidth(2500000, BANDWIDTH_LIMIT_DYNAMIC)
	c.SendStreamBegin(0)
	c.SetChunkSize(4096)

	err = c.SendCommand(&Command{
		Name:          "_result",
		TransactionID: cmd.TransactionID,
		Objects: []interface{}{
			DefaultConnectProperties,
			DefaultConnectInformation,
		},
	})

	if err != nil {
		return err
	}

	c.handler.OnConnect(c)
	return nil
}

func (c *conn) invokeCreateStream(cmd *Command) error {
	c.log.Tracef("invokeCreateStream(%#v)", *cmd)

	csid, err := c.chunkStream.CreateChunkStream(LowPriority)
	if err != nil {
		return err
	}

	stream := c.allocateStream(csid)

	err = c.SendCommand(&Command{
		Name:          "_result",
		StreamID:      cmd.StreamID,
		TransactionID: cmd.TransactionID,
		Objects:       []interface{}{nil, stream.ID()},
	})

	if err != nil {
		return err
	}

	c.SendStreamBegin(stream.ID())
	c.handler.OnCreateStream(stream)
	return nil
}

func (c *conn) invokeFCPublish(cmd *Command) error {
	streamName, err := func() (string, error) {
		if cmd.Objects == nil || len(cmd.Objects) != 2 {
			return "", fmt.Errorf("publish failed: invalid args %v", cmd.Objects)
		}

		name, ok := cmd.Objects[1].(string)
		if !ok || len(name) == 0 {
			return "", fmt.Errorf("publish failed: invalid name %v", cmd.Objects)
		}

		return name, nil
	}()

	if err != nil {
		return ErrPublishBadName(err)
	}

	return c.SendCommand(&Command{
		Name:          "onFCPublish",
		StreamID:      0,
		TransactionID: 0,
		Objects: []interface{}{
			nil,
			StatusPublishStart(fmt.Sprintf("FCPublish to stream %s.", streamName)),
		},
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

func (c *conn) OnCommand(cmd *Command) {
	var err error

	if cmd.StreamID == 0 {
		err = c.handler.Invoke(c, cmd, c)
	} else {
		stream := c.streams[cmd.StreamID]
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

func (c *conn) parseAmf3(buf *bytes.Buffer, streamid uint32) (*Command, error) {
	cmd := &Command{IsFlex: true}

	_, err := buf.ReadByte()
	if err != nil {
		return nil, err
	}

	return c.parseAmf0(buf, streamid, cmd)
}

func (c *conn) parseAmf0(buf *bytes.Buffer, streamid uint32, cmd *Command) (*Command, error) {
	if cmd == nil {
		cmd = &Command{}
	}

	name, err := amf.ReadString(buf)
	if err != nil {
		return nil, err
	}
	cmd.Name = name

	txnid, err := amf.ReadDouble(buf)
	if err != nil {
		return nil, err
	}
	cmd.TransactionID = uint32(txnid)

	for buf.Len() > 0 {
		object, err := amf.ReadValue(buf)
		if err != nil {
			return nil, err
		}
		cmd.Objects = append(cmd.Objects, object)
	}

	cmd.StreamID = streamid

	return cmd, nil
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
