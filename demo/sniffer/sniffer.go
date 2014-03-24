package main

import (
	"bufio"
	"encoding/binary"
	"flag"
	"fmt"
	"github.com/thelazyfox/gortmp/log"
	"github.com/zhangpeihao/goamf"
	"io"
	"io/ioutil"
	"net"

	"github.com/thelazyfox/gortmp"
)

var listen = flag.String("listen", "0.0.0.0:1935", "listen")
var remote = flag.String("remote", "live.justin.tv:1935", "remote")

func main() {
	flag.Parse()

	// log.SetLogLevel(log.TRACE)

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

func (rl RtmpLogger) OnMessage(msg *rtmp.Message) {
	logMessage(rl.tag, msg)
}

func (rl RtmpLogger) OnWindowFull(inCount uint32) {
	fmt.Printf("OnWindowFull: %d\n", inCount)
}

type Sniffer interface {
	io.ReadCloser
}

type sniffer struct {
	tr     io.Reader
	pa, pb net.Conn
}

func (s *sniffer) Read(b []byte) (int, error) {
	return s.tr.Read(b)
}

func (s *sniffer) Close() error {
	s.pa.Close()
	s.pb.Close()
	return nil
}

func NewSniffer(r io.Reader, tag string) Sniffer {
	a, b := net.Pipe()
	tr := io.TeeReader(r, a)

	go io.Copy(ioutil.Discard, a)

	log.Trace("Creating chunk stream reader")
	chunkStreamReader := rtmp.NewChunkStreamReader(RtmpLogger{tag})

	go func() {
		chunkStreamReader.ReadFrom(bufio.NewReader(b))
	}()

	return &sniffer{
		tr: tr,
		pa: a,
		pb: b,
	}
}

func logMessage(tag string, msg *rtmp.Message) {
	if msg.Type == rtmp.AUDIO_TYPE || msg.Type == rtmp.VIDEO_TYPE {
		return
	}
	fmt.Printf("%s - %#v\n", tag, *msg)

	cmd := &rtmp.Command{}
	switch msg.Type {
	case rtmp.SET_CHUNK_SIZE:
		fmt.Printf("SetChunkSize: %d\n", binary.BigEndian.Uint32(msg.Buf.Bytes()))
	case rtmp.ABORT_MESSAGE:
		fmt.Printf("AbortMessage: %d\n", binary.BigEndian.Uint32(msg.Buf.Bytes()))
	case rtmp.ACKNOWLEDGEMENT:
		fmt.Printf("Acknowledgement: %d\n", binary.BigEndian.Uint32(msg.Buf.Bytes()))
	case rtmp.WINDOW_ACKNOWLEDGEMENT_SIZE:
		fmt.Printf("WindowAcknowledgementSize: %d\n", binary.BigEndian.Uint32(msg.Buf.Bytes()))
	case rtmp.SET_PEER_BANDWIDTH:
		var bandwidth uint32
		var limit uint8
		binary.Read(msg.Buf, binary.BigEndian, &bandwidth)
		binary.Read(msg.Buf, binary.BigEndian, &limit)
		fmt.Printf("SetPeerBandwidth: %d %d\n", bandwidth, limit)
	case rtmp.COMMAND_AMF3:
		cmd.IsFlex = true
		_, err := msg.Buf.ReadByte()
		if err != nil {
			fmt.Printf("amf3 parse error\n")
			return
		}
		fallthrough
	case rtmp.COMMAND_AMF0:
		name, err := amf.ReadString(msg.Buf)
		if err != nil {
			fmt.Printf("amf0 parse error\n")
			return
		}
		cmd.Name = name

		txnid, err := amf.ReadDouble(msg.Buf)
		if err != nil {
			fmt.Printf("amf0 parse error\n")
			return
		}
		cmd.TransactionID = uint32(txnid)

		for msg.Buf.Len() > 0 {
			object, err := amf.ReadValue(msg.Buf)
			if err != nil {
				fmt.Printf("amf0 parse error\n")
				return
			}
			cmd.Objects = append(cmd.Objects, object)
		}

		cmd.StreamID = msg.StreamID
		fmt.Printf("%s - %#v\n", tag, *cmd)
	case rtmp.USER_CONTROL_MESSAGE:
		logUserControl(tag, msg)
	}
}

func logUserControl(tag string, msg *rtmp.Message) {
	err := func() error {
		var event uint16
		if err := binary.Read(msg.Buf, binary.BigEndian, &event); err != nil {
			return err
		}

		switch event {
		case rtmp.EVENT_STREAM_BEGIN:
			var streamId uint32
			if err := binary.Read(msg.Buf, binary.BigEndian, &streamId); err != nil {
				return err
			}
			fmt.Printf("StreamBegin: %d\n", streamId)
		case rtmp.EVENT_STREAM_EOF:
			var streamId uint32
			if err := binary.Read(msg.Buf, binary.BigEndian, &streamId); err != nil {
				return err
			}
			fmt.Printf("StreamEOF: %d\n", streamId)
		case rtmp.EVENT_STREAM_DRY:
			var streamId uint32
			if err := binary.Read(msg.Buf, binary.BigEndian, &streamId); err != nil {
				return err
			}
			fmt.Printf("StreamDry: %d\n", streamId)
		case rtmp.EVENT_SET_BUFFER_LENGTH:
			var streamId uint32
			var bufferLen uint32
			if err := binary.Read(msg.Buf, binary.BigEndian, &streamId); err != nil {
				return err
			}
			if err := binary.Read(msg.Buf, binary.BigEndian, &bufferLen); err != nil {
				return err
			}
			fmt.Printf("SetBufferLength: %d %d", streamId, bufferLen)
		case rtmp.EVENT_STREAM_IS_RECORDED:
			var streamId uint32
			if err := binary.Read(msg.Buf, binary.BigEndian, &streamId); err != nil {
				return err
			}
			fmt.Printf("StreamIsRecorded: %d", streamId)
		case rtmp.EVENT_PING_REQUEST:
			var timestamp uint32
			if err := binary.Read(msg.Buf, binary.BigEndian, &timestamp); err != nil {
				return err
			}
			fmt.Printf("PingRequest: %d\n", timestamp)
		case rtmp.EVENT_PING_RESPONSE:
			var timestamp uint32
			if err := binary.Read(msg.Buf, binary.BigEndian, &timestamp); err != nil {
				return err
			}
			fmt.Printf("PingResponse: %d\n", timestamp)
		default:
			fmt.Printf("unknown user control: %d\n", event)
		}
		return nil
	}()

	if err != nil {
		fmt.Printf("user control parse error: %s\n", err)
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
