package main

import (
	"container/list"
	_ "expvar"
	"flag"
	"fmt"
	"github.com/thelazyfox/gortmp"
	"github.com/thelazyfox/gortmp/log"
	// "github.com/zhangpeihao/goflv"
	"net"
	"net/http"
	_ "net/http/pprof"
	"time"
)

var (
	listenAddr = flag.String("listen", ":1935", "The address to bind to")
)

func RecordStream(ms rtmp.MediaStream, filebase string, maxSize, maxLen uint32) {
	log.Info("RecordStream staring...")
	mb, err := rtmp.NewMediaBuffer(ms, 4*1024*1024)

	if err != nil {
		log.Error("RecordStream failed to create MediaBuffer: %s", err)
		return
	}

	defer mb.Close()

	filename := fmt.Sprintf("%s_%d.flv", filebase, time.Now().Unix())
	out, err := flv.CreateFile(filename)
	if err != nil {
		log.Error("RecordStream failed to create file")
		return
	}

	var videoHeader, audioHeader *rtmp.FlvTag
	var ts, offset, size uint32

	for {
		var isKeyframe bool
		tag, err := mb.Get()
		if err != nil {
			log.Error("RecordStream ending due to %s", err)
			out.Sync()
			out.Close()
			return
		}

		// save the sequence headers
		if tag.Type == rtmp.VIDEO_TYPE {
			header := tag.GetVideoHeader()
			if videoHeader == nil {
				if header.FrameType == 1 && header.CodecID == 7 && header.AVCPacketType == 0 {
					videoHeader = tag
				}
			}

			isKeyframe = header.FrameType == 1
		} else if tag.Type == rtmp.AUDIO_TYPE {
			header := tag.GetAudioHeader()
			if audioHeader == nil {
				if header.SoundFormat == 10 && header.AACPacketType == 0 {
					audioHeader = tag
				}
			}
		}

		if offset > tag.Timestamp {
			ts = 0
		} else {
			ts = tag.Timestamp - offset
		}

		// try to do a soft cut
		if isKeyframe {
			if size > maxSize/20*19 || ts > maxLen/20*19 {
				filename = fmt.Sprintf("%s_%d.flv", filebase, time.Now().Unix())
				log.Debug("Splitting new file %s, previous file size=%d,len=%d", filename, size, ts)
				out.Sync()
				out.Close()
				out, err = flv.CreateFile(filename)
				if err != nil {
					log.Error("RecordStream failed to create output file: %s", err)
					return
				}

				size = 0
				ts = 0
				offset = tag.Timestamp

				if videoHeader != nil {
					out.WriteTag(videoHeader.Bytes, videoHeader.Type, 0)
					size += videoHeader.Size
				}

				if audioHeader != nil {
					out.WriteTag(audioHeader.Bytes, audioHeader.Type, 0)
					size += audioHeader.Size
				}
			}
		}

		// hard cut
		if size > maxSize || ts > maxLen {
			filename = fmt.Sprintf("%s_%d.flv", filebase, time.Now().Unix())
			log.Debug("Splitting new file %s, previous file size=%d,len=%d", filename, size, ts)
			out.Sync()
			out.Close()
			out, err = flv.CreateFile(filename)
			if err != nil {
				log.Error("RecordStream failed to create output file: %s", err)
				return
			}

			size = 0
			ts = 0
			offset = tag.Timestamp

			if videoHeader != nil {
				out.WriteTag(videoHeader.Bytes, videoHeader.Type, 0)
				size += videoHeader.Size

			}

			if audioHeader != nil {
				out.WriteTag(audioHeader.Bytes, audioHeader.Type, 0)
				size += audioHeader.Size
			}
		}

		err = out.WriteTag(tag.Bytes, tag.Type, ts)
		if err != nil {
			log.Error("RecordStream write tag failed: %s", err)
			out.Sync()
			out.Close()
			return
		}

		size += tag.Size
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
				tag.Buf.Close()
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
	go RecordStream(h.server.GetMediaStream(stream.Name()), stream.Name(), 25*1024*1024, 60*1000)
	fmt.Printf("OnPublish\n")
}

func (h *handler) OnClose(rtmp.Conn) {
	fmt.Printf("OnClose\n")
}

func (h *handler) Invoke(conn rtmp.Conn, stream rtmp.Stream, cmd *rtmp.Command, invoke func(*rtmp.Command) error) error {
	fmt.Printf("Invoke %#v\n", *cmd)
	return invoke(cmd)
}
