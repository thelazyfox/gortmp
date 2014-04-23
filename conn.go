package rtmp

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/thelazyfox/goamf"
	"github.com/thelazyfox/gortmp/log"
	"io"
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
	buf := GlobalBufferPool.Alloc()
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
	return c.netConn.RemoteAddr()
}

func (c *conn) Flush() error {
	return c.netConn.Flush()
}

func (c *conn) App() string {
	return c.app
}

func (c *conn) Error(err error) {
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
	log.Error("Closing connection with error: %s", err)
	c.Close()
}

func (c *conn) Close() {
	c.netConn.Close()
}

func (c *conn) SendStreamBegin(streamId uint32) {
	buf := GlobalBufferPool.Alloc()

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
		c.Error(fmt.Errorf("SetWindowSize failed: %s", err))
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
		c.Error(fmt.Errorf("SetPeerBandwidthFailed: %s", err))
	}
}

func (c *conn) OnMessage(msg *Message) {
	switch msg.Type {
	case COMMAND_AMF3:
		cmd, err := c.parseAmf3(bytes.NewBuffer(msg.Buf.Bytes()), msg.StreamID)
		if err != nil {
			log.Warning("Invalid COMMAND_AMF3 message")
			buf := bytes.NewBuffer(msg.Buf.Bytes())
			buf.ReadByte()
			c.amfDump(buf)

			c.onReceive(msg)
		} else {
			c.invoke(cmd)
		}
	case COMMAND_AMF0:
		cmd, err := c.parseAmf0(bytes.NewBuffer(msg.Buf.Bytes()), msg.StreamID, nil)
		if err != nil {
			log.Warning("Invalid COMMAND_AMF0 message")
			c.amfDump(bytes.NewBuffer(msg.Buf.Bytes()))
			c.onReceive(msg)
		} else {
			c.invoke(cmd)
		}
	default:
		c.onReceive(msg)
	}
}

func (c *conn) OnWindowFull(count uint32) {
	// pop this in a goroutine to avoid blocking the send loop
	go func() {
		err := c.chunkStream.SendAcknowledgement(count)
		if err != nil {
			c.Error(fmt.Errorf("OnWindowFull error: %s", err))
		}
	}()
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
	csid, err := c.chunkStream.CreateChunkStream(LowPriority)
	if err != nil {
		log.Error("failed to create chunk stream: %s", err)
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
		log.Error("invokeCreateStream error: %s", err)
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

func (c *conn) parseAmf3(buf *bytes.Buffer, streamid uint32) (*Command, error) {
	cmd := &Command{IsFlex: true}

	_, err := buf.ReadByte()
	if err != nil {
		log.Debug("parseAmf3 error")
		return nil, err
	}

	return c.parseAmf0(buf, streamid, cmd)
}

func (c *conn) amfDump(buf *bytes.Buffer) {
	i := 0
	for buf.Len() > 0 {
		object, err := amf.ReadValue(buf)
		if err != nil {
			log.Debug("amfDump failed to read amf object")
			return
		}
		log.Debug("conn.amfDump: %d - %#v", i, object)
		i += 1
	}
}

func (c *conn) parseAmf0(buf *bytes.Buffer, streamid uint32, cmd *Command) (*Command, error) {
	if cmd == nil {
		cmd = &Command{}
	}

	name, err := amf.ReadString(buf)
	if err != nil {
		log.Debug("parseAmf0 failed to read name")
		return nil, err
	}
	cmd.Name = name

	txnid, err := amf.ReadDouble(buf)
	if err != nil {
		log.Debug("parseAmf0 failed to read transaction id")
		return nil, err
	}
	cmd.TransactionID = uint32(txnid)

	for buf.Len() > 0 {
		object, err := amf.ReadValue(buf)
		if err != nil {
			log.Debug("parseAmf0 failed to read amf object")
			return nil, err
		}
		cmd.Objects = append(cmd.Objects, object)
	}

	cmd.StreamID = streamid

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
	if err == nil {
		log.Info("readLoop ended after %d bytes", n)
	} else {
		log.Info("readLoop ended after %d bytes with err %s", n, err)
	}
	c.readDone <- true
}

func (c *conn) writeTo() {
	n, err := c.chunkStream.WriteTo(c.netConn)
	if err == nil {
		log.Info("writeLoop ended after %d bytes", n)
	} else {
		log.Info("writeLoop ended after %d bytes with err %s", n, err)
	}
	c.writeDone <- true
}
