package rtmp

import (
	"bytes"
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
	Buf       *bytes.Buffer
}

func (f *FlvTag) Clone() *FlvTag {
	return &FlvTag{
		Type:      f.Type,
		Timestamp: f.Timestamp,
		Size:      f.Size,
		Buf:       GlobalBufferPool.Clone(f.Buf),
	}
}

func (f *FlvTag) GetAudioHeader() *FlvAudioHeader {
	if f.Type != AUDIO_TYPE || f.Buf.Len() < 2 {
		return nil
	}

	header := &FlvAudioHeader{}

	var bits uint8 = f.Buf.Bytes()[0]
	header.SoundFormat = bits >> 4
	header.SoundRate = (bits >> 2) & 3
	header.SoundSize = (bits >> 1) & 1
	header.SoundType = bits & 1

	if header.SoundFormat == 10 {
		header.AACPacketType = f.Buf.Bytes()[1]
	}

	return header
}

func (f *FlvTag) GetVideoHeader() *FlvVideoHeader {
	if f.Type != VIDEO_TYPE || f.Buf.Len() < 2 {
		return nil
	}

	header := &FlvVideoHeader{}

	var bits uint8 = f.Buf.Bytes()[0]

	header.FrameType = bits >> 4
	header.CodecID = bits & 0xF

	if header.CodecID == 7 {
		header.AVCPacketType = f.Buf.Bytes()[1]
	}

	return header
}
