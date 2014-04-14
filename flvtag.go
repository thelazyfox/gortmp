package rtmp

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
	Buf       DynamicBuffer
}

func (f *FlvTag) GetAudioHeader() *FlvAudioHeader {
	if f.Type != AUDIO_TYPE {
		return nil
	}

	header := &FlvAudioHeader{}
	buf := make([]byte, 2)
	f.Buf.Peek(buf)

	var bits uint8 = buf[0]
	header.SoundFormat = bits >> 4
	header.SoundRate = (bits >> 2) & 3
	header.SoundSize = (bits >> 1) & 1
	header.SoundType = bits & 1

	if header.SoundFormat == 10 {
		header.AACPacketType = buf[1]
	}

	return header
}

func (f *FlvTag) GetVideoHeader() *FlvVideoHeader {
	if f.Type != VIDEO_TYPE {
		return nil
	}

	header := &FlvVideoHeader{}
	buf := make([]byte, 2)

	var bits uint8 = buf[0]

	header.FrameType = bits >> 4
	header.CodecID = bits & 0xF

	if header.CodecID == 7 {
		header.AVCPacketType = buf[1]
	}

	return header
}
