package rtmp

import (
	"fmt"
	"github.com/thelazyfox/goamf"
	"github.com/thelazyfox/gortmp/log"
	"net"
	"sync"
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

	// access to the stream and player maps
	MediaStream(string) MediaStream
	MediaPlayer(Stream) MediaPlayer

	Stats() ServerStats
}

type server struct {
	handler ServerHandler

	streams map[string]MediaStream
	players map[Stream]MediaPlayer

	streamsMu sync.Mutex
	playersMu sync.Mutex

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
	OnDestroyStream(Stream)

	OnPlay(Stream, MediaPlayer)
	OnPublish(Stream, MediaStream)

	OnClose(Conn)

	Invoke(Conn, Stream, *Command, func(*Command) error) error
}

func NewServer(handler ServerHandler) Server {
	s := &server{
		handler: handler,
		streams: make(map[string]MediaStream),
		players: make(map[Stream]MediaPlayer),
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

func (s *server) MediaStream(name string) MediaStream {
	return s.getStream(name)
}

func (s *server) MediaPlayer(stream Stream) MediaPlayer {
	return s.getPlayer(stream)
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

func (s *server) getStream(name string) MediaStream {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()

	return s.streams[name]
}
func (s *server) putStream(name string, stream MediaStream) bool {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()

	_, found := s.streams[name]
	if !found {
		s.streams[name] = stream
		return true
	} else {
		return false
	}
}

func (s *server) delStream(name string) {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()

	delete(s.streams, name)
}

func (s *server) getPlayer(stream Stream) MediaPlayer {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()

	return s.players[stream]
}

func (s *server) putPlayer(stream Stream, player MediaPlayer) bool {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()

	_, found := s.players[stream]
	if !found {
		s.players[stream] = player
		return true
	} else {
		return false
	}
}

func (s *server) delPlayer(stream Stream) {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()

	delete(s.players, stream)
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
	if cmd.Objects == nil || len(cmd.Objects) != 3 {
		return ErrPublishBadName(fmt.Errorf("bad publish arguments: %v", cmd.Objects))
	}

	name, ok := cmd.Objects[1].(string)
	if !ok || len(name) == 0 {
		return ErrPublishBadName(fmt.Errorf("invalid stream name: %v", cmd.Objects[1]))
	}

	ms := NewMediaStream(stream)
	ok = s.putStream(name, ms)
	if !ok {
		ms.Close()
		return ErrPublishBadName(fmt.Errorf("invalid stream name: %v", name))
	}

	err := invoke(cmd)
	if err != nil {
		s.delStream(name)
		ms.Close()
		return err
	}

	return nil
}

func (s *server) invokePlay(stream Stream, cmd *Command, invoke func(*Command) error) error {
	if cmd.Objects == nil || len(cmd.Objects) < 2 {
		return ErrPlayFailed(fmt.Errorf("bad play arguments: %v", cmd.Objects))
	}

	name, ok := cmd.Objects[1].(string)
	if !ok || len(name) == 0 {
		return ErrPlayFailed(fmt.Errorf("invalid stream name: %v", cmd.Objects))
	}

	ms := s.getStream(name)
	if ms != nil {
		return ErrPlayFailed(fmt.Errorf("invalid stream name: %v", name))
	}

	mp, err := NewMediaPlayer(ms, stream)
	if err != nil {
		return ErrPlayFailed(err)
	}

	ok = s.putPlayer(stream, mp)
	if !ok {
		mp.Close()
		return ErrPlayFailed(fmt.Errorf("multiple play requests on the same stream"))
	}

	err = invoke(cmd)
	if err != nil {
		s.delPlayer(stream)
		mp.Close()
		return err
	}

	mp.Start()
	return nil
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

func (sc *serverConnHandler) OnDestroyStream(stream Stream) {
	if stream.Publishing() {
		mp := sc.server.getPlayer(stream)
		if mp != nil {
			mp.Close()
		}
	}

	if stream.Playing() {
		ms := sc.server.getStream(stream.Name())
		if ms != nil {
			ms.Close()
		}
	}

	sc.server.handler.OnDestroyStream(stream)
}

func (sc *serverConnHandler) OnClose(conn Conn) {
	// should always run
	defer sc.server.connections.Add(-1)
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
	ss.server.handler.OnPublish(stream, ss.server.getStream(stream.Name()))
}

func (ss *serverStreamHandler) OnPlay(stream Stream) {
	ss.server.handler.OnPlay(stream, ss.server.getPlayer(stream))
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
