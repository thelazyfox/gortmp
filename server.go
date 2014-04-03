package rtmp

import (
	"fmt"
	"github.com/thelazyfox/gortmp/log"
	"github.com/zhangpeihao/goamf"
	"net"
)

type Server interface {
	Serve(net.Listener) error
	GetMediaStream(string) MediaStream
}

type server struct {
	handler ServerHandler
	streams MediaStreamMap
}

type ServerHandler interface {
	OnConnect(Conn)
	OnCreateStream(Stream)

	OnPlay(Stream)
	OnPublish(Stream)

	OnClose(Conn)

	Invoke(Conn, Stream, *Command, func(*Command) error) error
}

func NewServer(handler ServerHandler) Server {
	s := &server{
		handler: handler,
		streams: NewMediaStreamMap(),
	}

	return s
}

func (s *server) Serve(ln net.Listener) error {
	for {
		conn, err := ln.Accept()

		if err != nil {
			return err
		}

		go s.handle(conn)
	}
}

func (s *server) GetMediaStream(name string) MediaStream {
	return s.streams.Get(name)
}

func (s *server) handle(conn net.Conn) {
	netConn := NewNetConn(conn)

	err := SHandshake(netConn)
	if err != nil {
		log.Error("Connection from %s failed handshake.", conn.RemoteAddr().String())
		return
	}

	NewConn(netConn, &serverConnHandler{server: s, streams: make(map[Stream]*serverStreamHandler)})
}

func (s *server) invoke(conn Conn, stream Stream, cmd *Command, invoke func(*Command) error) error {
	switch cmd.Name {
	case "connect":
		return s.invokeConnect(conn, cmd, invoke)
	case "publish":
		return s.invokePublish(stream, cmd, invoke)
	default:
		return invoke(cmd)
	}
}

func (s *server) invokeConnect(conn Conn, cmd *Command, invoke func(*Command) error) error {
	appName, ok := func() (string, bool) {
		if cmd.Objects == nil || len(cmd.Objects) == 0 {
			return "", false
		}

		obj, ok := cmd.Objects[0].(amf.Object)
		if !ok {
			return "", false
		}

		if _, found := obj["app"]; !found {
			return "", false
		}

		app, ok := obj["app"].(string)
		if !ok {
			return "", false
		}

		return app, true
	}()

	if appName != "app" || !ok {
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

	return invoke(cmd)
}

func (s *server) invokePublish(stream Stream, cmd *Command, invoke func(*Command) error) error {
	mediaStream := func() MediaStream {
		if cmd.Objects == nil || len(cmd.Objects) != 3 {
			return nil
		}

		name, ok := cmd.Objects[1].(string)
		if !ok || len(name) == 0 {
			return nil
		}

		return s.streams.Get(name)
	}()

	if mediaStream != nil {
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

	return invoke(cmd)
}

// handle connection callbacks
type serverConnHandler struct {
	server  *server
	streams map[Stream]*serverStreamHandler
}

func (sc *serverConnHandler) OnConnect(conn Conn) {
	sc.server.handler.OnConnect(conn)
}

func (sc *serverConnHandler) OnCreateStream(stream Stream) {
	sh := &serverStreamHandler{
		server: sc.server,
	}
	stream.SetHandler(sh)

	sc.streams[stream] = sh
	sc.server.handler.OnCreateStream(stream)
}

func (sc *serverConnHandler) OnClose(conn Conn) {
	for stream, handler := range sc.streams {
		sc.server.streams.Del(stream.Name())

		if handler.mediaPlayer != nil {
			handler.mediaPlayer.Close()
		}

		if handler.mediaStream != nil {
			handler.mediaStream.Close()
		}
	}
	sc.server.handler.OnClose(conn)
}

func (sc *serverConnHandler) OnReceive(conn Conn, msg *Message) {
	// nothing to do here, should not get messages
}

func (sc *serverConnHandler) Invoke(conn Conn, cmd *Command, invoke func(*Command) error) error {
	doInvoke := func(*Command) error {
		return sc.server.invoke(conn, nil, cmd, invoke)
	}

	return sc.server.handler.Invoke(conn, nil, cmd, doInvoke)
}

// handle stream callbacks
type serverStreamHandler struct {
	server      *server
	mediaStream MediaStream
	mediaPlayer MediaPlayer
}

func (ss *serverStreamHandler) OnPublish(stream Stream) {
	log.Debug("Server.OnPublish")
	if ss.mediaStream == nil {
		log.Debug("Creating new media stream")
		ss.mediaStream = NewMediaStream()
		ss.server.streams.Set(stream.Name(), ss.mediaStream)
	}
	ss.server.handler.OnPublish(stream)
}

func (ss *serverStreamHandler) OnPlay(stream Stream) {
	log.Debug("Server.OnPlay")
	if ss.mediaPlayer == nil {
		ms := ss.server.streams.Get(stream.Name())

		if ms != nil {
			log.Debug("creating new media player")
			ss.mediaPlayer = NewMediaPlayer(ms, stream)

			go func() {
				ss.mediaPlayer.Wait()
				stream.Conn().Close()
			}()
		} else {
			log.Debug("failed to find media stream")
		}
	} else {
		log.Debug("%+v", ss.mediaPlayer)
	}
	ss.server.handler.OnPlay(stream)
}

func (ss *serverStreamHandler) Invoke(stream Stream, cmd *Command, invoke func(*Command) error) error {
	doInvoke := func(*Command) error {
		return ss.server.invoke(stream.Conn(), stream, cmd, invoke)
	}

	return ss.server.handler.Invoke(stream.Conn(), stream, cmd, doInvoke)
}

func (ss *serverStreamHandler) OnReceive(stream Stream, msg *Message) {
	if ss.mediaStream != nil {
		switch msg.Type {
		case VIDEO_TYPE:
			fallthrough
		case AUDIO_TYPE:
			fallthrough
		case DATA_AMF0:
			tag := FlvTag{
				Type:      msg.Type,
				Timestamp: msg.AbsoluteTimestamp,
				Size:      msg.Size,
				Bytes:     msg.Buf.Bytes(),
			}

			if msg.Type == DATA_AMF0 {
				logTag(tag)
			}

			ss.mediaStream.Publish(tag)
		}
	}
}
