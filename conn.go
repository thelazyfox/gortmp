package rtmp

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/thelazyfox/gortmp/log"
	"github.com/zhangpeihao/goamf"
	"net"
	"sync"
)

// Stream

type ConnCounter struct {
	net.Conn
	counter int
	sync.Mutex
}

type NetReadWriter struct {
	net.Conn
}

func (n *NetReadWriter) ReadByte() (byte, error) {
	b := []byte{0}

	_, err := n.Read(b)
	return b[0], err
}

func (n *NetReadWriter) WriteByte(c byte) error {
	b := []byte{c}
	_, err := n.Write(b)
	return err
}

func (c *ConnCounter) Read(buf []byte) (int, error) {
	n, err := c.Conn.Read(buf)

	c.Lock()
	c.counter += n
	fmt.Printf("ConnCounter: %d\n", c.counter)
	c.Unlock()

	return n, err
}

type Stream interface {
	ID() uint32
	Conn() Conn
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

type Conn interface {
	Send(msg *Message) error
	SendCommand(cmd *Command) error

	SendStreamBegin(uint32)

	App() string

	Error(err error)
	Close()

	SetChunkSize(uint32)
	SetWindowSize(uint32)
	SetPeerBandwidth(uint32, uint8)
}

type ConnHandler interface {
	OnAccept(Conn)

	OnConnect(Conn)
	OnCreateStream(Stream)
	OnPlay(Stream)
	OnPublish(Stream)

	OnClose(Conn)

	OnReceive(Conn, Stream, *Message)
	Invoke(Conn, Stream, *Command, func(*Command) error) error
}

type conn struct {
	netConn     net.Conn
	chunkStream ChunkStream
	handler     ConnHandler

	rw *bufio.ReadWriter

	app string

	streams      map[uint32]*stream
	streamsMutex sync.RWMutex

	readDone  chan bool
	writeDone chan bool
}

func NewConn(netConn net.Conn, rw *bufio.ReadWriter, handler ConnHandler) Conn {
	c := &conn{
		netConn:   netConn,
		handler:   handler,
		rw:        rw,
		streams:   make(map[uint32]*stream),
		readDone:  make(chan bool),
		writeDone: make(chan bool),
	}

	c.chunkStream = NewChunkStream(c)

	go c.readFrom()
	go c.writeTo()
	go c.waitClose()

	// go func() {
	// 	for {
	// 		select {
	// 		case <-time.After(250 * time.Millisecond):
	// 			log.Trace("flushing")
	// 			// if err := rw.Flush(); err != nil {
	// 				// return
	// 			// }
	// 		}
	// 	}
	// }()

	return c
}

func (c *conn) Send(msg *Message) error {
	//TODO implement better
	return c.chunkStream.Write(msg)
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

	return c.rw.Flush()
}

func (c *conn) App() string {
	return c.app
}

func (c *conn) Error(err error) {
	//TODO implement better
	log.Debug("Closing connection: %s", err)
	c.Close()
}

func (c *conn) Close() {
	//TODO implement better
	c.netConn.Close()
	c.chunkStream.Close()
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

	err = c.chunkStream.Write(msg)
	if err != nil {
		c.Error(err)
	}
}

func (c *conn) SetChunkSize(size uint32) {
	log.Trace("SetChunkSize: %d", size)
	err := func() error {
		err := c.chunkStream.SendSetChunkSize(size)
		if err != nil {
			return err
		}

		return c.rw.Flush()

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

		return c.rw.Flush()
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

		return c.rw.Flush()
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
		c.handler.OnReceive(c, nil, msg)
	} else {
		c.streamsMutex.RLock()
		stream := c.streams[msg.StreamID]
		c.streamsMutex.RUnlock()

		if stream != nil {
			c.handler.OnReceive(c, stream, msg)
		} else {
			c.Error(fmt.Errorf("invalid message stream id"))
		}
	}
}

func (c *conn) Invoke(cmd *Command) error {
	if cmd.StreamID == 0 {
		switch cmd.Name {
		case "connect":
			return c.invokeConnect(cmd)
		case "createStream":
			return c.invokeCreateStream(cmd)
		case "releaseStream":
			log.Debug("releaseStream not supported")
		case "FCPublish":
			return c.invokeFCPublish(cmd)
		}
	} else {
		c.streamsMutex.RLock()
		stream := c.streams[cmd.StreamID]
		c.streamsMutex.RUnlock()

		if stream == nil {
			return fmt.Errorf("invalid message stream id")
		}

		switch cmd.Name {
		case "publish":
			return c.invokePublish(stream, cmd)
		case "play":
			return c.invokePlay(stream, cmd)
		}
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
				amf.Object{
					"level":       "error",
					"code":        RESULT_CONNECT_REJECTED,
					"description": RESULT_CONNECT_REJECTED_DESC,
				},
			},
		})
	}

	c.app = app

	err := c.SendCommand(&Command{
		Name:          "_result",
		TransactionID: cmd.TransactionID,
		Objects: []interface{}{
			amf.Object{
				"fmsVer":       "FMS/3,5,7,7009",
				"capabilities": float64(31),
			},
			amf.Object{
				"level":       "status",
				"code":        RESULT_CONNECT_OK,
				"description": RESULT_CONNECT_OK_DESC,
			},
		},
	})

	c.SetWindowSize(2500000)
	c.SetPeerBandwidth(2500000, BANDWIDTH_LIMIT_DYNAMIC)
	c.SendStreamBegin(0)
	c.SetChunkSize(4096)

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

	stream := c.allocateStream()
	stream.csid = csid

	err = c.SendCommand(&Command{
		Name:          "_result",
		StreamID:      cmd.StreamID,
		TransactionID: cmd.TransactionID,
		Objects:       []interface{}{nil, stream.id},
	})

	if err != nil {
		log.Debug("invokeCreateStream error: %s", err)
		return err
	}

	c.SendStreamBegin(stream.id)
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
				amf.Object{
					"level":       "error",
					"code":        NETSTREAM_PUBLISH_BADNAME,
					"description": fmt.Sprintf("invalid stream name"),
				},
			},
		})
	}

	return c.SendCommand(&Command{
		Name:          "onFCPublish",
		StreamID:      0,
		TransactionID: cmd.TransactionID,
		Objects: []interface{}{
			nil,
			amf.Object{
				"level":       "status",
				"code":        "NetStream.Publish.Start",
				"description": fmt.Sprintf("FCPublish to stream %s.", streamName),
			},
		},
	})
}

func (c *conn) invokePublish(stream Stream, cmd *Command) error {
	log.Trace("invokePublish")
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

	log.Trace("got streamName")

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

	log.Trace("sending onStatus publish start success")
	err := c.SendCommand(&Command{
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
		log.Debug("invokePublish error: %s", err)
		return err
	}

	c.handler.OnPublish(stream)
	return nil
}

func (c *conn) invokePlay(stream Stream, cmd *Command) error {
	return nil
}

func (c *conn) allocateStream() *stream {
	c.streamsMutex.Lock()
	defer c.streamsMutex.Unlock()

	for i := uint32(1); ; i += 1 {
		if s := c.streams[i]; s == nil {
			c.streams[i] = &stream{
				id:   i,
				conn: c,
			}
			return c.streams[i]
		}
	}
}

func (c *conn) invoke(cmd *Command) {
	var err error

	if cmd.StreamID == 0 {
		err = c.handler.Invoke(c, nil, cmd, c.Invoke)
	} else {
		c.streamsMutex.RLock()
		stream := c.streams[cmd.StreamID]
		c.streamsMutex.RUnlock()

		if stream != nil {
			c.handler.Invoke(c, stream, cmd, c.Invoke)
		} else {
			err = fmt.Errorf("invalid message stream id")
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
	<-c.readDone
	<-c.writeDone

	c.handler.OnClose(c)
}

func (c *conn) readFrom() {
	n, err := c.chunkStream.ReadFrom(c.rw)
	log.Debug("readLoop() ended after %d bytes with err %s", n, err)
	c.readDone <- true
}

func (c *conn) writeTo() {
	n, err := c.chunkStream.WriteTo(c.rw)
	log.Debug("writeLoop() ended after %d bytes with err %s", n, err)
	c.writeDone <- true
}
