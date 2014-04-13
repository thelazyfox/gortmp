package main

import (
	"container/list"
	"flag"
	"fmt"
	"github.com/thelazyfox/gortmp"
	"github.com/thelazyfox/gortmp/log"
	"github.com/zhangpeihao/goflv"
	"net"
	"net/http"
	_ "net/http/pprof"
	"time"
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

func TrackStarvation(streamName string, ms rtmp.MediaStream) {
	ch, err := ms.Subscribe()
	if err != nil {
		log.Error("Starvation tracker failed to subscribe to stream: %s", streamName)
		return
	}

	defer ms.Unsubscribe(ch)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	var curTs, lastTs int64
	var lastClock time.Time
	history := list.New()

	for {
		select {
		case tag, ok := <-ch:
			if ok {
				if tag.Type == rtmp.VIDEO_TYPE {
					curTs = int64(tag.Timestamp)
				}
			} else {
				return
			}
		case <-ticker.C:
			if lastClock.IsZero() {
				lastTs = curTs
				lastClock = time.Now()
			} else {
				streamDelta := (curTs - lastTs)
				clockDelta := time.Since(lastClock).Nanoseconds() / 1000000
				lastTs = curTs
				lastClock = time.Now()
				history.PushFront(clockDelta - streamDelta)

				if history.Len() > 25 {
					history.Remove(history.Back())
				}

				var sum int64

				for e := history.Front(); e != nil; e = e.Next() {
					sum += e.Value.(int64)
				}

				// this mimics the behavior of the wowza starvation tracker
				fmt.Printf("TrackStarvation starved = %t\n", float64(sum)/float64(clockDelta)/float64(history.Len()) > 0.05)
				fmt.Printf("TrackStarvation for stream %s - %d\n", streamName, sum/int64(history.Len()))
			}
		}
	}
}

func main() {
	flag.Parse()
	log.SetLogLevel(log.DEBUG)

	go func() {
		fmt.Println(http.ListenAndServe(":6060", nil))
	}()

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
	go TrackStarvation(stream.Name(), h.server.GetMediaStream(stream.Name()))
	fmt.Printf("OnPublish\n")
}

func (h *handler) OnClose(rtmp.Conn) {
	fmt.Printf("OnClose\n")
}

func (h *handler) Invoke(conn rtmp.Conn, stream rtmp.Stream, cmd *rtmp.Command, invoke func(*rtmp.Command) error) error {
	fmt.Printf("Invoke %#v\n", *cmd)
	return invoke(cmd)
}
