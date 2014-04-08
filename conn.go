package rtmp

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/thelazyfox/gortmp/log"
	"github.com/zhangpeihao/goamf"
	"net"
)

// Stream

type Conn interface {
	// basics
	Send(msg *Message) error
	SendCommand(cmd *Command) error
	Error(err error)
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

	OnClose(Conn)

	OnReceive(Conn, *Message)
	Invoke(Conn, *Command, func(*Command) error) error
}

type conn struct {
	netConn     NetConn
	chunkStream ChunkStream
	handler     ConnHandler

	app string

	streams map[uint32]Stream

	readDone  chan bool
	writeDone chan bool
}

func NewConn(netConn NetConn, handler ConnHandler) Conn {
	c := &conn{
		netConn:   netConn,
		handler:   handler,
		streams:   make(map[uint32]Stream),
		readDone:  make(chan bool),
		writeDone: make(chan bool),
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

func (c *conn) Error(err error) {
	cmdErr, ok := err.(ErrorResponse)
	if ok {
		err = c.SendCommand(cmdErr.Command())
	}

	log.Error("Closing connection with error: %s", err)
	c.Close()
}

func (c *conn) Close() {
	c.netConn.Close()
}

func (c *conn) SendStreamBegin(streamId uint32) {
	buf := new(bytes.Buffer)

	err := binary.Write(buf, binary.BigEndian, EVENT_STREAM_BEGIN)
	if err != nil {
		c.Error(err)
		return
	}

	err = binary.Write(buf, binary.BigEndian, streamId)
	if err != nil {
		c.Error(err)
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
		c.Error(err)
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
		log.Error("SetChunkSize failed with error: %s", err)
		c.Error(err)
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
		log.Error("SetWindowSize failed with error: %s", err)
		c.Error(err)
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
		log.Error("SetPeerBandwidth failed with error: %s", err)
		c.Error(err)
	}
}

func (c *conn) OnMessage(msg *Message) {
	log.Trace("%#v", *msg)
	switch msg.Type {
	case COMMAND_AMF3:
		cmd, err := c.parseAmf3(msg)
		if err != nil {
			c.Error(fmt.Errorf("invalid COMMAND_AMF3 message"))
		}
		c.invoke(cmd)
	case COMMAND_AMF0:
		cmd, err := c.parseAmf0(msg, nil)
		if err != nil {
			c.Error(fmt.Errorf("invalid COMMAND_AMF0 message"))
		}
		c.invoke(cmd)
	default:
		c.onReceive(msg)
	}
}

func (c *conn) onReceive(msg *Message) {
	if msg.StreamID == 0 {
		c.handler.OnReceive(c, msg)
	} else {
		stream := c.streams[msg.StreamID]

		if stream != nil {
			stream.onReceive(msg)
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
	app, ok := func() (string, bool) {
		if cmd.Objects == nil || len(cmd.Objects) != 1 {
			log.Debug("invokeConnect: invalid parameters: %+v", cmd.Objects)
			return "", false
		}

		params, ok := cmd.Objects[0].(amf.Object)
		if !ok {
			log.Debug("invokeConnect: invalid command object: %+v", cmd.Objects[0])
			return "", false
		}

		app, ok := params["app"]
		if !ok {
			log.Debug("invokeConnect: missing app parameter")
			return "", false
		}

		s, ok := app.(string)
		if !ok {
			log.Debug("invokeConnect: invalid app parameter: %+v", app)
		}
		return s, ok
	}()

	if !ok {
		return NewErrorResponse(&Command{
			Name:          "_error",
			StreamID:      cmd.StreamID,
			TransactionID: cmd.TransactionID,
			Objects: []interface{}{
				nil,
				StatusConnectRejected,
			},
		})
	}

	c.app = app

	c.SetWindowSize(2500000)
	c.SetPeerBandwidth(2500000, BANDWIDTH_LIMIT_DYNAMIC)
	c.SendStreamBegin(0)
	c.SetChunkSize(4096)

	err := c.SendCommand(&Command{
		Name:          "_result",
		TransactionID: cmd.TransactionID,
		Objects: []interface{}{
			DefaultConnectProperties,
			DefaultConnectInformation,
		},
	})

	if err != nil {
		log.Debug("invokeConnect error: %s", err)
		return err
	}

	c.handler.OnConnect(c)
	return nil
}

func (c *conn) invokeCreateStream(cmd *Command) error {
	log.Trace("invokeCreateStream: %#v", *cmd)

	csid, err := c.chunkStream.CreateChunkStream(LowPriority)
	if err != nil {
		log.Debug("failed to create chunk stream: %s", err)
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
		log.Debug("invokeCreateStream error: %s", err)
		return err
	}

	c.SendStreamBegin(stream.ID())
	c.handler.OnCreateStream(stream)
	return nil
}

func (c *conn) invokeFCPublish(cmd *Command) error {
	streamName, ok := func() (string, bool) {
		if cmd.Objects == nil || len(cmd.Objects) != 2 {
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
			StreamID:      0,
			TransactionID: 0,
			Objects: []interface{}{
				nil,
				StatusPublishBadName,
			},
		})
	}

	status := StatusPublishStart
	status.Description = fmt.Sprintf("FCPublish to stream %s.", streamName)
	return c.SendCommand(&Command{
		Name:          "onFCPublish",
		StreamID:      0,
		TransactionID: 0,
		Objects: []interface{}{
			nil,
			status,
		},
	})
}

func (c *conn) allocateStream(csid uint32) Stream {
	for i := uint32(1); ; i += 1 {
		if s := c.streams[i]; s == nil {
			c.streams[i] = NewStream(i, c, csid)
			return c.streams[i]
		}
	}
}

func (c *conn) invoke(cmd *Command) {
	var err error

	if cmd.StreamID == 0 {
		err = c.handler.Invoke(c, cmd, c.Invoke)
	} else {
		stream := c.streams[cmd.StreamID]
		if stream != nil {
			err = stream.invoke(cmd)
		} else {
			err = fmt.Errorf("invalid stream id specified")
		}
	}

	if err != nil {
		c.Error(err)
	}
}

func (c *conn) parseAmf3(msg *Message) (*Command, error) {
	cmd := &Command{IsFlex: true}

	_, err := msg.Buf.ReadByte()
	if err != nil {
		log.Debug("parseAmf3 error")
		return nil, err
	}

	return c.parseAmf0(msg, cmd)
}

func (c *conn) parseAmf0(msg *Message, cmd *Command) (*Command, error) {
	if cmd == nil {
		cmd = &Command{}
	}

	name, err := amf.ReadString(msg.Buf)
	if err != nil {
		log.Debug("parseAmf0 failed to read name")
		return nil, err
	}
	cmd.Name = name

	txnid, err := amf.ReadDouble(msg.Buf)
	if err != nil {
		log.Debug("parseAmf0 failed to read transaction id")
		return nil, err
	}
	cmd.TransactionID = uint32(txnid)

	for msg.Buf.Len() > 0 {
		object, err := amf.ReadValue(msg.Buf)
		if err != nil {
			log.Debug("parseAmf0 failed to read amf object")
			return nil, err
		}
		cmd.Objects = append(cmd.Objects, object)
	}

	cmd.StreamID = msg.StreamID

	return cmd, nil
}

func (c *conn) waitClose() {
	select {
	case <-c.readDone:
	case <-c.writeDone:
	}

	// shut down when read or write ends
	c.netConn.Close()
	c.chunkStream.Close()

	select {
	case <-c.readDone:
	case <-c.writeDone:
	}

	c.handler.OnClose(c)
}

func (c *conn) readFrom() {
	n, err := c.chunkStream.ReadFrom(c.netConn)
	log.Debug("readLoop() ended after %d bytes with err %s", n, err)
	c.readDone <- true
}

func (c *conn) writeTo() {
	n, err := c.chunkStream.WriteTo(c.netConn)
	log.Debug("writeLoop() ended after %d bytes with err %s", n, err)
	c.writeDone <- true
}
