package main

import (
	"bufio"
	"flag"
	"fmt"
	"github.com/zhangpeihao/goamf"
	"github.com/zhangpeihao/log"
	"io"
	"io/ioutil"
	"net"

	"github.com/thelazyfox/gortmp"
)

var listen = flag.String("listen", "0.0.0.0:1935", "listen")
var remote = flag.String("remote", "live.justin.tv:1935", "remote")

func main() {
	flag.Parse()

	l := log.NewLogger(".", "sniffer", nil, 60, 3600*24, false)
	l.SetMainLevel(log.LOG_LEVEL_DEBUG)
	rtmp.InitLogger(l)

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Printf("Failed to open listen socket: %s\n", err)
		return
	}

	fmt.Printf("Listening - %s\n", *listen)
	for {
		client, err := ln.Accept()

		if err != nil {
			netErr, ok := err.(net.Error)

			if ok && netErr.Temporary() {
				continue
			}

			fmt.Printf("Listen socket error: %s\n", err)
			return
		}

		remote, err := net.Dial("tcp", *remote)
		if err != nil {
			fmt.Printf("Failed to open remote conenction: %s\n", err)
			remote.Close()
			continue
		}

		go proxy(client, remote)
	}
}

type RtmpLogger struct {
	tag string
}

func (rl RtmpLogger) OnReceived(conn rtmp.Conn, message *rtmp.Message) {
	fmt.Printf("%s - %#v\n", rl.tag, message)

	switch message.Type {
	case rtmp.COMMAND_AMF0:
		var err error
		cmd := &rtmp.Command{}
		cmd.Name, err = amf.ReadString(message.Buf)
		if err != nil {
			fmt.Printf("%s - ERROR PARSING COMMAND_AMF0\n")
			return
		}
		transactionID, err := amf.ReadDouble(message.Buf)
		if err != nil {
			fmt.Printf("%s - ERROR PARSING COMMAND_AMF0\n")
			return
		}
		cmd.TransactionID = uint32(transactionID)
		for message.Buf.Len() > 0 {
			object, err := amf.ReadValue(message.Buf)
			if err != nil {
				fmt.Printf("%s - ERROR PARSING COMMAND\n")
				return
			}
			cmd.Objects = append(cmd.Objects, object)
		}
		rl.OnReceivedCommand(conn, message, cmd)
	}
}

func (rl RtmpLogger) OnReceivedCommand(conn rtmp.Conn, message *rtmp.Message, command *rtmp.Command) {
	fmt.Printf("%s - %#v\n", rl.tag, command)
}

func (rl RtmpLogger) OnClosed(conn rtmp.Conn) {
	fmt.Printf("%s - OnClosed\n", rl.tag)
}

type Sniffer interface {
	io.ReadCloser
}

type sniffer struct {
	tr     io.Reader
	pa, pb net.Conn
	conn   rtmp.Conn
}

func (s *sniffer) Read(b []byte) (int, error) {
	return s.tr.Read(b)
}

func (s *sniffer) Close() error {
	s.pa.Close()
	s.pb.Close()
	s.conn.Close()
	return nil
}

func NewSniffer(r io.Reader, tag string) Sniffer {
	a, b := net.Pipe()
	tr := io.TeeReader(r, a)

	go io.Copy(ioutil.Discard, a)

	conn := rtmp.NewConn(b, bufio.NewReader(b), bufio.NewWriter(b), RtmpLogger{tag}, 20)

	return &sniffer{
		conn: conn,
		tr:   tr,
		pa:   a,
		pb:   b,
	}
}

func handshake(client net.Conn, remote net.Conn) error {
	done := make(chan error)

	go func() {
		_, err := io.CopyN(client, remote, 3073)
		done <- err
	}()

	go func() {
		_, err := io.CopyN(remote, client, 3073)
		done <- err
	}()

	if err := <-done; err != nil {
		<-done
		return err
	} else {
		return <-done
	}
}

func proxy(client net.Conn, remote net.Conn) {
	fmt.Printf("Beginning proxy...\n")

	err := handshake(client, remote)

	if err != nil {
		fmt.Printf("Handshake error: %s\n", err)
		client.Close()
		remote.Close()
		return
	}

	done := make(chan net.Conn)

	clientSniffer := NewSniffer(client, "CLIENT")
	remoteSniffer := NewSniffer(remote, "REMOTE")
	defer clientSniffer.Close()
	defer remoteSniffer.Close()

	go func() {
		io.Copy(client, remoteSniffer)
		done <- client
	}()

	go func() {
		io.Copy(remote, clientSniffer)
		done <- remote
	}()

	(<-done).Close()
	(<-done).Close()

	fmt.Printf("Finishing proxy...\n")
}
