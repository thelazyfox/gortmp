package rtmp

import (
	"fmt"
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

	log Logger
}

type ServerHandler interface {
	OnConnect(Conn)
	OnCreateStream(Stream)
	OnDestroyStream(Stream)

	OnPlay(Stream, MediaPlayer)
	OnPublish(Stream, MediaStream)

	OnClose(Conn, error)

	Invoke(Conn, Stream, Command, Invoker) error
}

func NewServer(handler ServerHandler) Server {
	return &server{
		handler: handler,
		streams: make(map[string]MediaStream),
		players: make(map[Stream]MediaPlayer),
		log:     NewLogger("Server(nil)"),
	}
}

func (s *server) Serve(ln net.Listener) error {
	s.log.SetTag(fmt.Sprintf("Server(%s)", ln.Addr()))

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
				s.log.Tracef("Stats: %+v", s.Stats())
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
		s.log.Errorf("SHandshake(%s) failed: %s", conn.RemoteAddr(), err)
		netConn.Close()
		return
	}

	NewConn(netConn, &serverConnHandler{server: s, streams: make(map[Stream]*serverStreamHandler)})
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
		sc.server.log.Debugf("closing media stream")
		ms := sc.server.getStream(stream.Name())
		if ms != nil {
			ms.Close()
			sc.server.delStream(stream.Name())
		}
	}

	if stream.Playing() {
		sc.server.log.Debugf("closing media player")
		mp := sc.server.getPlayer(stream)
		if mp != nil {
			mp.Close()
			sc.server.delPlayer(stream)
		}
	}

	sc.server.handler.OnDestroyStream(stream)
}

func (sc *serverConnHandler) OnClose(conn Conn, err error) {
	// should always run
	defer sc.server.connections.Add(-1)
	sc.server.handler.OnClose(conn, err)
}

func (sc *serverConnHandler) OnReceive(conn Conn, msg *Message) {
	// nothing to do here, should not get messages
}

func (sc *serverConnHandler) DoInvoke(conn Conn, cmd Command, callback Invoker) error {
	switch cmd := cmd.(type) {
	case ConnectCommand:
		return sc.invokeConnect(conn, cmd, callback)
	default:
		return callback.Invoke(cmd)
	}
}

func (sc *serverConnHandler) Invoke(conn Conn, cmd Command, callback Invoker) error {
	return sc.server.handler.Invoke(conn, nil, cmd, &ConnInvoker{
		Conn:    conn,
		Invoker: callback,
		Func:    sc.DoInvoke,
	})
}

func (sc *serverConnHandler) invokeConnect(conn Conn, cmd ConnectCommand, callback Invoker) error {
	app, ok := cmd.Properties["app"].(string)
	if !ok || app != "app" {
		return ErrConnectRejected(fmt.Errorf("invalid app: %v", cmd.Properties["app"]))
	}

	return callback.Invoke(cmd)
}

// handle stream callbacks
type serverStreamHandler struct {
	server      *server
	mediaStream MediaStream
	mediaPlayer MediaPlayer
}

func (ss *serverStreamHandler) OnPublish(stream Stream) {
	ms := ss.server.getStream(stream.Name())
	ss.mediaStream = ms
	ss.server.handler.OnPublish(stream, ms)
}

func (ss *serverStreamHandler) OnPlay(stream Stream) {
	mp := ss.server.getPlayer(stream)
	ss.mediaPlayer = mp
	ss.server.handler.OnPlay(stream, mp)
}

func (ss *serverStreamHandler) DoInvoke(stream Stream, cmd Command, callback Invoker) error {
	switch cmd := cmd.(type) {
	case PublishCommand:
		return ss.invokePublish(stream, cmd, callback)
	case PlayCommand:
		return ss.invokePlay(stream, cmd, callback)
	default:
		return callback.Invoke(cmd)
	}
}

func (ss *serverStreamHandler) Invoke(stream Stream, cmd Command, callback Invoker) error {
	return ss.server.handler.Invoke(stream.Conn(), stream, cmd, &StreamInvoker{
		Stream:  stream,
		Invoker: callback,
		Func:    ss.DoInvoke,
	})
}

func (ss *serverStreamHandler) OnReceive(stream Stream, msg *Message) {
	ss.server.log.Tracef("OnReceive(%#v)", *msg)
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
			ss.server.log.Tracef("Publishing tag: %#v", tag)
			ss.mediaStream.Publish(tag)
		}
	}
}

func (ss *serverStreamHandler) invokePublish(stream Stream, cmd PublishCommand, callback Invoker) error {
	ms := NewMediaStream(cmd.Name, stream)

	if ok := ss.server.putStream(cmd.Name, ms); !ok {
		ms.Close()
		return ErrPublishBadName(fmt.Errorf("invalid stream name: %v", cmd.Name))
	}

	if err := callback.Invoke(cmd); err != nil {
		ss.server.delStream(cmd.Name)
		ms.Close()
		return err
	}

	return nil
}

func (ss *serverStreamHandler) invokePlay(stream Stream, cmd PlayCommand, callback Invoker) error {
	ms := ss.server.getStream(cmd.Name)
	if ms == nil {
		return ErrPlayFailed(fmt.Errorf("invalid stream name: %v", cmd.Name))
	}

	mp, err := NewMediaPlayer(ms, stream)
	if err != nil {
		return ErrPlayFailed(err)
	}

	if ok := ss.server.putPlayer(stream, mp); !ok {
		mp.Close()
		ss.server.delPlayer(stream)
		ss.server.log.Warnf("ignoring play on already playing stream")
	}

	if err = callback.Invoke(cmd); err != nil {
		ss.server.delPlayer(stream)
		mp.Close()
		return err
	}

	mp.Start()
	return nil
}
