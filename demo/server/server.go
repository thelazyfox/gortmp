package main

import (
	"bufio"
	"flag"
	"fmt"
	"github.com/thelazyfox/gortmp"
	"github.com/thelazyfox/gortmp/log"
	"net"
	"time"
)

var (
	listenAddr = flag.String("listen", ":1935", "The address to bind to")
)

func main() {
	flag.Parse()
	log.SetLogLevel(log.TRACE)

	ln, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		fmt.Printf("Failed to bind socket: %s\n", err)
		return
	}

	fmt.Printf("Listening on: %s\n", ln.Addr().String())

	for {
		conn, err := ln.Accept()
		if err != nil {
			netErr, ok := err.(net.Error)
			if !ok || !netErr.Temporary() {
				fmt.Printf("Socket accept error: %s\n", err)
				return
			}
		}

		br := bufio.NewReader(conn)
		bw := bufio.NewWriter(conn)
		rw := bufio.NewReadWriter(br, bw)
		err = rtmp.SHandshake(conn, br, bw, 500*time.Millisecond)
		conn.SetDeadline(time.Time{})
		if err != nil {
			fmt.Printf("Handshake error: %s\n", err)
		} else {
			rtmp.NewConn(conn, rw, &handler{})
		}
	}
}

type handler struct {
}

func (h *handler) OnAccept(rtmp.Conn) {
	fmt.Printf("OnAccept\n")
}

func (h *handler) OnConnect(rtmp.Conn) {
	fmt.Printf("OnConnect\n")
}

func (h *handler) OnCreateStream(rtmp.Stream) {
	fmt.Printf("OnCreateStream\n")
}

func (h *handler) OnPlay(rtmp.Stream) {
	fmt.Printf("OnPlay\n")
}

func (h *handler) OnPublish(rtmp.Stream) {
	fmt.Printf("OnPublish\n")
}

func (h *handler) OnClose(rtmp.Conn) {
	fmt.Printf("OnClose\n")
}

func (h *handler) OnReceive(rtmp.Conn, rtmp.Stream, *rtmp.Message) {
	fmt.Printf("OnReceive\n")
}

func (h *handler) Invoke(conn rtmp.Conn, stream rtmp.Stream, cmd *rtmp.Command, invoke func(*rtmp.Command) error) error {
	fmt.Printf("Invoke\n")
	return invoke(cmd)
}
