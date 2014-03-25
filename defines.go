// Copyright 2013, zhangpeihao All rights reserved.

// RTMP protocol golang implementation
package rtmp

import (
	"io"
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
	io.Writer
	io.ByteWriter
}

type Reader interface {
	io.Reader
	io.ByteReader
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
