package main

import (
	"container/list"
	"flag"
	"fmt"
	"github.com/thelazyfox/gortmp"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"time"
)

var (
	listenAddr = flag.String("listen", ":1935", "The address to bind to")
)

/*
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
}*/

func TrackStarvation(ms rtmp.MediaStream) {
	log := rtmp.NewLogger(fmt.Sprintf("TrackStarvation(%s)", ms.Stream().Name()))
	log.Infof("start")
	defer log.Infof("end")

	ch, err := ms.Subscribe()
	if err != nil {
		log.Errorf("stream subscribe failed")
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
					log.Tracef("tag.Timestamp=%d", tag.Timestamp)
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
				log.Debugf("streamDelta=%d,clockDelta=%d", streamDelta, clockDelta)
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
				log.Infof("sum = %d, len = %d", sum, history.Len())
				log.Infof("starved = %t", float64(sum)/float64(clockDelta)/float64(history.Len()) > 0.05)
				log.Infof("starvation = %d", sum/int64(history.Len()))
			}
		}
	}
}

func main() {
	flag.Parse()
	log.SetOutput(os.Stdout)
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	rtmp.SetLogLevel(rtmp.LogTrace)

	go func() {
		log := rtmp.NewLogger("http.ListenAndServe(:6060)")
		err := http.ListenAndServe(":6060", nil)
		if err != nil {
			log.Errorf("error: %s", err)
		}
	}()

	ln, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		fmt.Printf("Failed to bind socket: %s\n", err)
		return
	}

	fmt.Printf("Listening on: %s\n", ln.Addr())

	logTag := fmt.Sprintf("DemoServer(%s)", ln.Addr())
	h := &handler{log: rtmp.NewLogger(logTag)}
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
	log    rtmp.Logger
}

func (h *handler) OnAccept(rtmp.Conn) {
	h.log.Infof("OnAccept")
}

func (h *handler) OnConnect(conn rtmp.Conn) {
	h.log.Infof("OnConnect(%s)", conn.Addr())
}

func (h *handler) OnCreateStream(rtmp.Stream) {
	h.log.Infof("OnCreateStream")
}

func (h *handler) OnDestroyStream(rtmp.Stream) {
	h.log.Infof("OnDestroyStream")
}

func (h *handler) OnPlay(rtmp.Stream, rtmp.MediaPlayer) {
	h.log.Infof("OnPlay")
}

func (h *handler) OnPublish(stream rtmp.Stream, publisher rtmp.MediaStream) {
	// go RecordStream(h.server.GetMediaStream(stream.Name()), "out.flv")
	go TrackStarvation(publisher)
	// go RecordStream(h.server.GetMediaStream(stream.Name()), stream.Name(), 25*1024*1024, 15*1000)
	h.log.Infof("OnPublish")
}

func (h *handler) OnClose(conn rtmp.Conn, err error) {
	h.log.Infof("OnClose(%s, %s)", conn.Addr(), err)
}

func (h *handler) Invoke(conn rtmp.Conn, stream rtmp.Stream, cmd rtmp.Command, callback rtmp.Invoker) error {
	h.log.Infof("Invoke(%+v)", cmd)
	return callback.Invoke(cmd)
}
