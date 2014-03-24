package rtmp

// import (
// 	"fmt"
// 	"github.com/thelazyfox/gortmp/log"
// 	"net"
// )

// type Server interface {
// 	Listen(net.Listener) error
// }

// type ServerConnection interface {
// 	Server() Server
// }

// type ServerStream interface {
// 	Connection() ServerConnection
// }

// type ServerHandler interface {
// 	ConnHandler
// }

// type server struct {
// 	handler ServerHandler
// }

// func NewServer(handler ServerHandler) Server {
// 	return &server{handler: handler}
// }

// func (s *server) Listen(ln net.Listener) error {
// 	for {
// 		conn, err := ln.Accept()

// 		if err != nil {
// 			netErr, ok := err.(net.Error)
// 			if !ok || !netErr.Temporary() {
// 				return err
// 			}
// 		}

// 		go s.handle(conn)
// 	}
// }

// func (s *server) Invoke(c Conn, cmd *Command) error {
// 	// somewhat confusing, all this does is start the callback chain
// 	return s.handler.Invoke(c, cmd, func(cmd *Command) error {
// 		return s.invoke(c, cmd)
// 	})
// }

// func (s *server) invoke(c Conn, cmd *Command) error {
// 	// if cmd.StreamID
// 	// switch cmd.Name {
// 	// 	case ""
// 	// }
// }

// func (s *server) OnMessage(c Conn, msg *Message) {

// }

// func (s *server) OnClose(c Conn) {

// }

// func (s *server) handle(conn net.Conn) {
// 	err := s.handshake(conn)
// 	if err != nil {
// 		log.Error("handshake failure")
// 	}

// 	rtmpConn := NewConn(conn, s)
// }

// func (s *server) handshake(conn net.Conn) error {
// 	return fmt.Errorf("handshake not implemented")
// }
