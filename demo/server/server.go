package main

import (
	"flag"
	"fmt"
	"github.com/thelazyfox/gortmp"
	"github.com/thelazyfox/gortmp/log"
	"github.com/zhangpeihao/goflv"
	"net"
)

var (
	listenAddr = flag.String("listen", ":1935", "The address to bind to")
)

func RecordStream(ms rtmp.MediaStream, filename string) {
	log.Info("RecordStream staring...")
	mb, err := rtmp.NewMediaBuffer(ms, 4*1024*1024)

	if err != nil {
		log.Error("RecordStream failed to create MediaBuffer: %s", err)
		return
	}

	defer mb.Close()

	out, err := flv.CreateFile(filename)
	if err != nil {
		log.Error("RecordStream failed to create file")
		return
	}

	defer out.Close()
	defer out.Sync()

	for {
		tag, err := mb.Get()
		if err != nil {
			log.Error("RecordStream failed to get tag: %s", err)
			return
		}

		switch tag.Type {
		case rtmp.VIDEO_TYPE:
			fallthrough
		case rtmp.AUDIO_TYPE:
			err = out.WriteTag(tag.Bytes, tag.Type, tag.Timestamp)
			if err != nil {
				log.Error("RecordStream failed to write tag: %s", err)
				return
			}
		}
	}
}

func main() {
	flag.Parse()
	log.SetLogLevel(log.DEBUG)

	ln, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		fmt.Printf("Failed to bind socket: %s\n", err)
		return
	}

	fmt.Printf("Listening on: %s\n", ln.Addr().String())

	h := &handler{}
	server := rtmp.NewServer(h)
	h.server = server
	err = server.Serve(ln)

	if err != nil {
		fmt.Printf("Server error: %s\n", err)
	} else {
		fmt.Printf("Server exited cleanly: %s\n", err)
	}
}

type handler struct {
	server rtmp.Server
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

func (h *handler) OnPublish(stream rtmp.Stream) {
	// go RecordStream(h.server.GetMediaStream(stream.Name()), "out.flv")
	fmt.Printf("OnPublish\n")
}

func (h *handler) OnClose(rtmp.Conn) {
	fmt.Printf("OnClose\n")
}

func (h *handler) Invoke(conn rtmp.Conn, stream rtmp.Stream, cmd *rtmp.Command, invoke func(*rtmp.Command) error) error {
	fmt.Printf("Invoke %#v\n", *cmd)
	return invoke(cmd)
}
