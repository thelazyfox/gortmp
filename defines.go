// Copyright 2013, zhangpeihao All rights reserved.

// RTMP protocol golang implementation
package rtmp

import (
	"bufio"
	"errors"
	"fmt"
	"github.com/thelazyfox/gortmp/log"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// Chunk Message Header - "fmt" field values
const (
	HEADER_FMT_FULL                   = 0x00
	HEADER_FMT_SAME_STREAM            = 0x01
	HEADER_FMT_SAME_LENGTH_AND_STREAM = 0x02
	HEADER_FMT_CONTINUATION           = 0x03
)

// Result codes
const (
	RESULT_CONNECT_OK            = "NetConnection.Connect.Success"
	RESULT_CONNECT_REJECTED      = "NetConnection.Connect.Rejected"
	RESULT_CONNECT_OK_DESC       = "Connection successed."
	RESULT_CONNECT_REJECTED_DESC = "[ AccessManager.Reject ] : [ code=400 ] : "
	NETSTREAM_PLAY_START         = "NetStream.Play.Start"
	NETSTREAM_PLAY_RESET         = "NetStream.Play.Reset"
	NETSTREAM_PUBLISH_START      = "NetStream.Publish.Start"
	NETSTREAM_PUBLISH_BADNAME    = "NetStream.Publish.BadName"
)

// Chunk stream ID
const (
	CS_ID_BASE             = uint32(2)
	CS_ID_PROTOCOL_CONTROL = uint32(2)
	CS_ID_COMMAND          = uint32(3)
	CS_ID_USER_CONTROL     = uint32(4)
	CS_ID_MAX              = uint32(100)
)

// Message type
const (
	SET_CHUNK_SIZE              = uint8(1)
	ABORT_MESSAGE               = uint8(2)
	ACKNOWLEDGEMENT             = uint8(3)
	USER_CONTROL_MESSAGE        = uint8(4)
	WINDOW_ACKNOWLEDGEMENT_SIZE = uint8(5)
	SET_PEER_BANDWIDTH          = uint8(6)
	AUDIO_TYPE                  = uint8(8)
	VIDEO_TYPE                  = uint8(9)
	AGGREGATE_MESSAGE_TYPE      = uint8(22)
	SHARED_OBJECT_AMF0          = uint8(19)
	SHARED_OBJECT_AMF3          = uint8(16)
	DATA_AMF0                   = uint8(18)
	DATA_AMF3                   = uint8(15)
	COMMAND_AMF0                = uint8(20)
	COMMAND_AMF3                = uint8(17)
)

// User control events
const (
	EVENT_STREAM_BEGIN       = uint16(0)
	EVENT_STREAM_EOF         = uint16(1)
	EVENT_STREAM_DRY         = uint16(2)
	EVENT_SET_BUFFER_LENGTH  = uint16(3)
	EVENT_STREAM_IS_RECORDED = uint16(4)
	EVENT_PING_REQUEST       = uint16(6)
	EVENT_PING_RESPONSE      = uint16(7)
	EVENT_REQUEST_VERIFY     = uint16(0x1a)
	EVENT_RESPOND_VERIFY     = uint16(0x1b)
	EVENT_BUFFER_EMPTY       = uint16(0x1f)
	EVENT_BUFFER_READY       = uint16(0x20)
)

// Set peer bandwidth args
const (
	BANDWIDTH_LIMIT_HARD    = uint8(0)
	BANDWIDTH_LIMIT_SOFT    = uint8(1)
	BANDWIDTH_LIMIT_DYNAMIC = uint8(2)
)

var (
	//	FLASH_PLAYER_VERSION = []byte{0x0A, 0x00, 0x2D, 0x02}
	FLASH_PLAYER_VERSION = []byte{0x09, 0x00, 0x7C, 0x02}
	//FLASH_PLAYER_VERSION = []byte{0x80, 0x00, 0x07, 0x02}
	//FLASH_PLAYER_VERSION_STRING = "LNX 10,0,32,18"
	FLASH_PLAYER_VERSION_STRING = "LNX 9,0,124,2"
	//FLASH_PLAYER_VERSION_STRING = "WIN 11,5,502,146"
	SWF_URL_STRING     = "http://localhost/1.swf"
	PAGE_URL_STRING    = "http://localhost/1.html"
	MIN_BUFFER_LENGTH  = uint32(256)
	FMS_VERSION        = []byte{0x04, 0x05, 0x00, 0x01}
	FMS_VERSION_STRING = "4,5,0,297"
)

const (
	MAX_TIMESTAMP                       = uint32(2000000000)
	AUTO_TIMESTAMP                      = uint32(0XFFFFFFFF)
	DEFAULT_HIGH_PRIORITY_BUFFER_SIZE   = 2048
	DEFAULT_MIDDLE_PRIORITY_BUFFER_SIZE = 128
	DEFAULT_LOW_PRIORITY_BUFFER_SIZE    = 64
	DEFAULT_CHUNK_SIZE                  = uint32(128)
	DEFAULT_WINDOW_SIZE                 = 131072
	DEFAULT_CAPABILITIES                = float64(15)
	DEFAULT_AUDIO_CODECS                = float64(4071)
	DEFAULT_VIDEO_CODECS                = float64(252)
	FMS_CAPBILITIES                     = uint32(255)
	FMS_MODE                            = uint32(2)
)

type Writer interface {
	Write(p []byte) (nn int, err error)
	WriteByte(c byte) error
}

type Reader interface {
	Read(p []byte) (n int, err error)
	ReadByte() (c byte, err error)
}

type ReadWriter interface {
	Reader
	Writer
}

type RtmpURL struct {
	protocol     string
	host         string
	port         uint16
	app          string
	instanceName string
}

// Check error
//
// If error panic
func CheckError(err error, name string) {
	if err != nil {
		panic(errors.New(fmt.Sprintf("%s: %s", name, err.Error())))
	}
}

// Parse url
//
// To connect to Flash Media Server, pass the URI of the application on the server.
// Use the following syntax (items in brackets are optional):
//
// protocol://host[:port]/[appname[/instanceName]]
func ParseURL(url string) (rtmpURL RtmpURL, err error) {
	s1 := strings.SplitN(url, "://", 2)
	if len(s1) != 2 {
		err = errors.New(fmt.Sprintf("Parse url %s error. url invalid.", url))
		return
	}
	rtmpURL.protocol = strings.ToLower(s1[0])
	s1 = strings.SplitN(s1[1], "/", 2)
	if len(s1) != 2 {
		err = errors.New(fmt.Sprintf("Parse url %s error. no app!", url))
		return
	}
	s2 := strings.SplitN(s1[0], ":", 2)
	if len(s2) == 2 {
		var port int
		port, err = strconv.Atoi(s2[1])
		if err != nil {
			err = errors.New(fmt.Sprintf("Parse url %s error. port error: %s.", url, err.Error()))
			return
		}
		if port > 65535 || port <= 0 {
			err = errors.New(fmt.Sprintf("Parse url %s error. port error: %d.", url, port))
			return
		}
		rtmpURL.port = uint16(port)
	} else {
		rtmpURL.port = 1935
	}
	if len(s2[0]) == 0 {
		err = errors.New(fmt.Sprintf("Parse url %s error. host is empty.", url))
		return
	}
	rtmpURL.host = s2[0]

	s2 = strings.SplitN(s1[1], "/", 2)
	rtmpURL.app = s2[0]
	if len(s2) == 2 {
		rtmpURL.instanceName = s2[1]
	}
	return
	/*
		if len(s1) == 3 {
			if strings.HasPrefix(s1[1], "//") && len(s1[1]) > 2 {
				rtmpURL.host = s1[1][2:]
				if len(rtmpURL.host) == 0 {
					err = errors.New(fmt.Sprintf("Parse url %s error. Host is empty.", url))
					return
				}
			} else {
				err = errors.New(fmt.Sprintf("Parse url %s error. Host invalid.", url))
				return
			}
			fmt.Printf("s1: %v\n", s1)
			s2 := strings.SplitN(s1[2], "/", 3)
			var port int
			port, err = strconv.Atoi(s2[0])
			if err != nil {
				err = errors.New(fmt.Sprintf("Parse url %s error. port error: %s.", url, err.Error()))
				return
			}
			if port > 65535 || port <= 0 {
				err = errors.New(fmt.Sprintf("Parse url %s error. port error: %d.", url, port))
				return
			}
			rtmpURL.port = uint16(port)
			if len(s2) > 1 {
				rtmpURL.app = s2[1]
			}
			if len(s2) > 2 {
				rtmpURL.instanceName = s2[2]
			}
		} else {
			if len(s1) < 2 {
				err = errors.New(fmt.Sprintf("Parse url %s error. url invalid.", url))
				return
			}
			// Default port
			rtmpURL.port = 1935
			if strings.HasPrefix(s1[1], "//") && len(s1[1]) > 2 {
				s2 := strings.SplitN(s1[1][2:], "/", 3)
				rtmpURL.host = s2[0]
				if len(rtmpURL.host) == 0 {
					err = errors.New(fmt.Sprintf("Parse url %s error. Host is empty.", url))
					return
				}
				if len(s2) > 1 {
					rtmpURL.app = s2[1]
				}
				if len(s2) > 2 {
					rtmpURL.instanceName = s2[2]
				}
			} else {
				err = errors.New(fmt.Sprintf("Parse url %s error. Host invalid.", url))
				return
			}
		}
		return
	*/
}

func (rtmpUrl *RtmpURL) App() string {
	if len(rtmpUrl.instanceName) == 0 {
		return rtmpUrl.app
	}
	return rtmpUrl.app + "/" + rtmpUrl.instanceName
}

// Get timestamp
func GetTimestamp() uint32 {
	//return uint32(0)
	return uint32(time.Now().UnixNano()/int64(1000000)) % MAX_TIMESTAMP
}

// Read byte from network
func ReadByteFromNetwork(r Reader) (b byte, err error) {
	retry := 1
	for {
		b, err = r.ReadByte()
		if err == nil {
			return
		}
		netErr, ok := err.(net.Error)
		if !ok {
			return
		}
		if !netErr.Temporary() {
			return
		}
		log.Debug("ReadByteFromNetwork temporary %s", err)
		if retry < 16 {
			retry = retry * 2
		}
		time.Sleep(time.Duration(retry*100) * time.Millisecond)
	}
	return
}

// Read bytes from network
func ReadAtLeastFromNetwork(r Reader, buf []byte, min int) (n int, err error) {
	retry := 1
	for {
		n, err = io.ReadAtLeast(r, buf, min)
		if err == nil {
			return
		}
		netErr, ok := err.(net.Error)
		if !ok {
			return
		}
		if !netErr.Temporary() {
			return
		}
		log.Debug("ReadAtLeastFromNetwork temporary %s", err)
		if retry < 16 {
			retry = retry * 2
		}
		time.Sleep(time.Duration(retry*100) * time.Millisecond)
	}
	return
}

// Copy bytes from network
func CopyNFromNetwork(dst Writer, src Reader, n int64) (written int64, err error) {
	// return io.CopyN(dst, src, n)

	buf := make([]byte, 4096)
	for written < n {
		l := len(buf)
		if d := n - written; d < int64(l) {
			l = int(d)
		}
		nr, er := ReadAtLeastFromNetwork(src, buf[0:l], l)
		if er != nil {
			err = er
			break
		}
		if nr == l {
			nw, ew := dst.Write(buf[0:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = io.ErrShortWrite
				break
			}
		} else {
			err = io.ErrShortBuffer
		}
	}
	return
}

func WriteToNetwork(w Writer, data []byte) (written int, err error) {
	length := len(data)
	var n int
	retry := 1
	for written < length {
		n, err = w.Write(data[written:])
		if err == nil {
			written += int(n)
			continue
		}
		netErr, ok := err.(net.Error)
		if !ok {
			return
		}
		if !netErr.Temporary() {
			return
		}
		log.Debug("WriteToNetwork temporary %s", err)
		if retry < 16 {
			retry = retry * 2
		}
		time.Sleep(time.Duration(retry*500) * time.Millisecond)
	}
	return

}

// Copy bytes to network
func CopyNToNetwork(dst Writer, src Reader, n int64) (written int64, err error) {
	// return io.CopyN(dst, src, n)

	buf := make([]byte, 4096)
	for written < n {
		l := len(buf)
		if d := n - written; d < int64(l) {
			l = int(d)
		}
		nr, er := io.ReadAtLeast(src, buf[0:l], l)
		if nr > 0 {
			nw, ew := WriteToNetwork(dst, buf[0:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = io.ErrShortWrite
				break
			}
		}
		if er != nil {
			err = er
			break
		}
	}
	return
}

func FlushToNetwork(w *bufio.Writer) (err error) {
	retry := 1
	for {
		err = w.Flush()
		if err == nil {
			return
		}
		netErr, ok := err.(net.Error)
		if !ok {
			return
		}
		if !netErr.Temporary() {
			return
		}
		log.Debug("FlushToNetwork temporary %s", err)
		if retry < 16 {
			retry = retry * 2
		}
		time.Sleep(time.Duration(retry*500) * time.Millisecond)
	}
	return
}
