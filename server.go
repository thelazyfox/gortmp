package rtmp

import (
	"fmt"
	"github.com/thelazyfox/goamf"
	"github.com/thelazyfox/gortmp/log"
	"net"
	"time"
)

type ServerStats struct {
	Connections int64

	HandshakeFailures int64

	BytesIn  int64
	BytesOut int64

	BytesInRate  int64
	BytesOutRate int64
}

type Server interface {
	Serve(net.Listener) error
	GetMediaStream(string) MediaStream

	Stats() ServerStats
}

type server struct {
	handler ServerHandler
	streams MediaStreamMap

	connections    Counter
	handshakeFails Counter

	bytesIn  Counter
	bytesOut Counter

	bpsIn  Counter
	bpsOut Counter
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
	// Calculates bpsIn/bpsOut once per second
	ticker := time.NewTicker(time.Second)
	tickerDone := make(chan bool)

	defer ticker.Stop()
	defer close(tickerDone)

	// not perfect, but probably close enough
	go func() {
		var lastIn int64
		var lastOut int64
		var lastTick time.Time

		for {
			select {
			case tick := <-ticker.C:
				if lastTick.IsZero() {
					lastTick = tick
					continue
				}

				delta := tick.Sub(lastTick).Seconds()

				s.bpsIn.Set(int64(float64(s.bytesIn.Get()-lastIn) / delta))
				lastIn = s.bytesIn.Get()

				s.bpsOut.Set(int64(float64(s.bytesOut.Get()-lastOut) / delta))
				lastOut = s.bytesOut.Get()

				lastTick = tick
			case <-tickerDone:
				return
			}
		}
	}()

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

func (s *server) Stats() ServerStats {
	return ServerStats{
		Connections:       s.connections.Get(),
		HandshakeFailures: s.handshakeFails.Get(),
		BytesIn:           s.bytesIn.Get(),
		BytesOut:          s.bytesOut.Get(),
		BytesInRate:       s.bpsIn.Get(),
		BytesOutRate:      s.bpsOut.Get(),
	}
}

func (s *server) handle(conn net.Conn) {
	s.connections.Add(1)
	netConn := NewNetConn(conn, &s.bytesIn, &s.bytesOut)

	err := SHandshake(netConn)
	if err != nil {
		s.handshakeFails.Add(1)
		s.connections.Add(-1)
		log.Error("Connection from %s failed handshake: %s", conn.RemoteAddr().String(), err)
		netConn.Conn().Close()
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
	err := func() error {
		if cmd.Objects == nil || len(cmd.Objects) == 0 {
			return fmt.Errorf("connect error: invalid args %v", cmd.Objects)
		}

		obj, ok := cmd.Objects[0].(amf.Object)
		if !ok {
			return fmt.Errorf("connect error: invalid args %v", cmd.Objects)
		}

		app, ok := obj["app"].(string)
		if !ok || app != "app" {
			return fmt.Errorf("connect error: invalid app %v", cmd.Objects)
		}

		return nil
	}()

	if err != nil {
		return ErrConnectRejected(err)
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
		return ErrPublishBadName(fmt.Errorf("rejected"))
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
	// should always run
	defer sc.server.connections.Add(-1)

	for stream, handler := range sc.streams {
		if handler.mediaPlayer != nil {
			handler.mediaPlayer.Close()
		}

		if handler.mediaStream != nil {
			sc.server.streams.Del(stream.Name())
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
	ms := ss.server.streams.Get(stream.Name())

	if ss.mediaPlayer == nil && ms != nil {
		log.Debug("creating new media player")
		mp, err := NewMediaPlayer(ms, stream)

		if err != nil {
			log.Error("Failed to create MediaPlayer: %s", err)
			stream.Conn().Error(err)
			return
		} else {
			ss.mediaPlayer = mp
			go func() {
				ss.mediaPlayer.Wait()
				stream.Conn().Close()
			}()
		}
	} else {
		stream.Conn().Error(fmt.Errorf("play failed"))
		return
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
			tag := &FlvTag{
				Type:      msg.Type,
				Timestamp: msg.AbsoluteTimestamp,
				Size:      msg.Size,
				Bytes:     msg.Buf.Bytes(),
			}

			ss.mediaStream.Publish(tag)
		}
	}
}
