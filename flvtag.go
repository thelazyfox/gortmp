package rtmp

import (
	"bytes"
	"encoding/binary"
)

type FlvAudioHeader struct {
	SoundFormat   uint8
	SoundRate     uint8
	SoundSize     uint8
	SoundType     uint8
	AACPacketType uint8
}

type FlvVideoHeader struct {
	FrameType     uint8
	CodecID       uint8
	AVCPacketType uint8
}

type FlvTag struct {
	Type      uint8
	Timestamp uint32
	Size      uint32
	Bytes     []byte
}

func (f *FlvTag) GetAudioHeader() *FlvAudioHeader {
	if f.Type != AUDIO_TYPE {
		return nil
	}

	header := &FlvAudioHeader{}
	buf := bytes.NewBuffer(f.Bytes)

	var bits uint8
	binary.Read(buf, binary.BigEndian, &bits)
	header.SoundFormat = bits >> 4
	header.SoundRate = (bits >> 2) & 3
	header.SoundSize = (bits >> 1) & 1
	header.SoundType = bits & 1

	if header.SoundFormat == 10 {
		binary.Read(buf, binary.BigEndian, &header.AACPacketType)
	}

	return header
}

func (f *FlvTag) GetVideoHeader() *FlvVideoHeader {
	if f.Type != VIDEO_TYPE {
		return nil
	}

	header := &FlvVideoHeader{}
	buf := bytes.NewBuffer(f.Bytes)

	var bits uint8
	binary.Read(buf, binary.BigEndian, &bits)

	header.FrameType = bits >> 4
	header.CodecID = bits & 0xF

	if header.CodecID == 7 {
		binary.Read(buf, binary.BigEndian, &header.AVCPacketType)
	}

	return header
}
