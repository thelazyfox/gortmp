// Copyright 2013, zhangpeihao All rights reserved.

package rtmp

import (
	"bytes"
	"fmt"
	"github.com/thelazyfox/goamf"
	"math"
)

type OptionalFloat struct {
	Val float64
	Set bool
}

type OptionalString struct {
	Val string
	Set bool
}

type Command interface {
	RawCommand() RawCommand
}

func ParseCommand(msg *Message) (Command, error) {
	rawCmd, err := ParseRawCommand(msg)

	if err != nil {
		return nil, err
	}

	switch rawCmd.Name {
	case "publish":
		return ParsePublishCommand(rawCmd)
	case "play":
		return ParsePlayCommand(rawCmd)
	case "connect":
		return ParseConnectCommand(rawCmd)
	case "createStream":
		return ParseCreateStreamCommand(rawCmd)
	case "deleteStream":
		return ParseDeleteStreamCommand(rawCmd)
	case "FCPublish":
		return ParseFCPublishCommand(rawCmd)
	case "_result":
		return ParseResultCommand(rawCmd)
	case "_error":
		return ParseErrorCommand(rawCmd)
	default:
		return rawCmd, nil
	}
}

type RawCommand struct {
	IsFlex        bool
	StreamID      uint32
	TransactionID uint32
	Name          string
	Objects       []interface{}
}

func ParseRawCommand(msg *Message) (RawCommand, error) {
	var cmd RawCommand
	var err error

	buf := bytes.NewBuffer(msg.Buf.Bytes())

	switch msg.Type {
	case COMMAND_AMF3:
		cmd.IsFlex = true
		_, err := buf.ReadByte()
		if err != nil {
			return cmd, err
		}
	case COMMAND_AMF0:
	default:
		return cmd, fmt.Errorf("invalid message type, cannot parse command")
	}

	name, err := amf.ReadString(buf)
	if err != nil {
		return cmd, err
	}
	cmd.Name = name

	txnid, err := amf.ReadDouble(buf)
	if err != nil {
		return cmd, err
	}
	cmd.TransactionID = uint32(txnid)

	for buf.Len() > 0 {
		obj, err := amf.ReadValue(buf)
		if err != nil {
			return cmd, err
		}
		cmd.Objects = append(cmd.Objects, obj)
	}

	cmd.StreamID = msg.StreamID

	return cmd, nil
}

func (cmd RawCommand) RawCommand() RawCommand {
	return cmd
}

func (cmd RawCommand) Message() (*Message, error) {
	buf := new(bytes.Buffer)
	err := cmd.write(buf)
	if err != nil {
		return nil, err
	}

	if buf.Len() > math.MaxUint32 {
		return nil, fmt.Errorf("message larger than uint32 max")
	}

	return &Message{
		StreamID:      cmd.StreamID,
		ChunkStreamID: CS_ID_COMMAND,
		Type:          COMMAND_AMF0,
		Buf:           buf,
		Size:          uint32(buf.Len()),
	}, nil
}

func (cmd RawCommand) write(w Writer) (err error) {
	if cmd.IsFlex {
		err = w.WriteByte(0x00)
		if err != nil {
			return
		}
	}
	_, err = amf.WriteString(w, cmd.Name)
	if err != nil {
		return
	}
	_, err = amf.WriteDouble(w, float64(cmd.TransactionID))
	if err != nil {
		return
	}
	for _, object := range cmd.Objects {
		_, err = amf.WriteValue(w, object)
		if err != nil {
			return
		}
	}
	return
}

type PlayCommand struct {
	StreamID      uint32
	TransactionID uint32

	Name  string
	Start OptionalFloat
}

func ParsePlayCommand(raw RawCommand) (PlayCommand, error) {
	if raw.Name != "play" {
		return PlayCommand{}, fmt.Errorf("play command name must be play, got: %s", raw.Name)
	}

	if len(raw.Objects) < 2 {
		return PlayCommand{}, fmt.Errorf("insufficient play arguments: len(%+v) < 2", raw.Objects)
	}

	name, ok := raw.Objects[1].(string)
	if !ok {
		return PlayCommand{}, fmt.Errorf("invalid play stream name: %#v", raw.Objects[1])
	}

	start, startSet := raw.Objects[2].(float64)

	return PlayCommand{
		StreamID:      raw.StreamID,
		TransactionID: raw.TransactionID,
		Name:          name,
		Start:         OptionalFloat{Val: start, Set: startSet},
	}, nil
}

func (cmd PlayCommand) RawCommand() RawCommand {
	raw := RawCommand{
		Name:          "play",
		StreamID:      cmd.StreamID,
		TransactionID: cmd.TransactionID,
	}

	if cmd.Start.Set {
		raw.Objects = []interface{}{nil, cmd.Name, cmd.Start.Val}
	} else {
		raw.Objects = []interface{}{nil, cmd.Name}
	}

	return raw
}

type PublishCommand struct {
	StreamID      uint32
	TransactionID uint32

	Name string
	Type OptionalString
}

func ParsePublishCommand(raw RawCommand) (PublishCommand, error) {
	if raw.Name != "publish" {
		return PublishCommand{}, fmt.Errorf("publish command name must be publish, got: %s", raw.Name)
	}

	if len(raw.Objects) < 2 {
		return PublishCommand{}, fmt.Errorf("insufficient publish arguments: len(%+v) < 2", raw.Objects)
	}

	name, ok := raw.Objects[1].(string)
	if !ok {
		return PublishCommand{}, fmt.Errorf("invalid publish stream name: %#v", raw.Objects[1])
	}

	typ, typSet := raw.Objects[2].(string)

	return PublishCommand{
		StreamID:      raw.StreamID,
		TransactionID: raw.TransactionID,
		Name:          name,
		Type:          OptionalString{Val: typ, Set: typSet},
	}, nil
}

func (cmd PublishCommand) RawCommand() RawCommand {
	raw := RawCommand{
		Name:          "publish",
		StreamID:      cmd.StreamID,
		TransactionID: cmd.TransactionID,
	}

	if cmd.Type.Set {
		raw.Objects = []interface{}{nil, cmd.Name, cmd.Type.Val}
	} else {
		raw.Objects = []interface{}{nil, cmd.Name}
	}
	return raw
}

type ConnectCommand struct {
	TransactionID uint32

	Properties amf.Object
}

func ParseConnectCommand(raw RawCommand) (ConnectCommand, error) {
	if raw.Name != "connect" {
		return ConnectCommand{}, fmt.Errorf("connect command name must be connect, got: %s", raw.Name)
	}

	if raw.StreamID != 0 {
		return ConnectCommand{}, fmt.Errorf("non-zero stream id invalid for connect")
	}

	if len(raw.Objects) < 1 {
		return ConnectCommand{}, fmt.Errorf("insufficient connect arguments: %+v", raw.Objects)
	}

	props, ok := raw.Objects[0].(amf.Object)
	if !ok {
		props = make(amf.Object)
	}

	return ConnectCommand{
		TransactionID: raw.TransactionID,
		Properties:    props,
	}, nil
}

func (cmd ConnectCommand) RawCommand() RawCommand {
	return RawCommand{
		Name:          "connect",
		TransactionID: cmd.TransactionID,
		Objects:       []interface{}{cmd.Properties},
	}
}

type OnStatusCommand struct {
	StreamID      uint32
	TransactionID uint32

	Properties interface{}
	Info       interface{}
}

func (cmd OnStatusCommand) RawCommand() RawCommand {
	return RawCommand{
		Name:          "onStatus",
		StreamID:      cmd.StreamID,
		TransactionID: cmd.TransactionID,
		Objects:       []interface{}{cmd.Properties, cmd.Info},
	}
}

type CreateStreamCommand struct {
	TransactionID uint32
}

func (cmd CreateStreamCommand) RawCommand() RawCommand {
	return RawCommand{
		Name:          "createStream",
		TransactionID: cmd.TransactionID,
	}
}

func ParseCreateStreamCommand(raw RawCommand) (CreateStreamCommand, error) {
	if raw.Name != "createStream" {
		return CreateStreamCommand{}, fmt.Errorf("invalid cmd name for createStream: %s", raw.Name)
	}

	if raw.StreamID != 0 {
		return CreateStreamCommand{}, fmt.Errorf("createStream can only be invoked on stream 0")
	}

	return CreateStreamCommand{
		TransactionID: raw.TransactionID,
	}, nil
}

type DeleteStreamCommand struct {
	TransactionID uint32

	DeleteStreamID uint32
}

func (cmd DeleteStreamCommand) RawCommand() RawCommand {
	return RawCommand{
		TransactionID: cmd.TransactionID,
		Objects:       []interface{}{nil, cmd.DeleteStreamID},
	}
}

func ParseDeleteStreamCommand(raw RawCommand) (DeleteStreamCommand, error) {
	if raw.Name != "deleteStream" {
		return DeleteStreamCommand{}, fmt.Errorf("invalid cmd name for deleteStream: %s", raw.Name)
	}

	if raw.StreamID != 0 {
		return DeleteStreamCommand{}, fmt.Errorf("deleteStream can only be invoked on stream 0")
	}

	if len(raw.Objects) < 2 {
		return DeleteStreamCommand{}, fmt.Errorf("insufficient deleteStream arguments: %+v", raw.Objects)
	}

	deleteId, ok := raw.Objects[1].(float64)
	if !ok {
		return DeleteStreamCommand{}, fmt.Errorf("invalid deleteStream stream id: %#v", raw.Objects[1])
	}

	return DeleteStreamCommand{
		TransactionID:  raw.TransactionID,
		DeleteStreamID: uint32(deleteId),
	}, nil
}

type FCPublishCommand struct {
	Name string
}

func (cmd FCPublishCommand) RawCommand() RawCommand {
	return RawCommand{
		Name:    "FCPublish",
		Objects: []interface{}{nil, cmd.Name},
	}
}

func ParseFCPublishCommand(raw RawCommand) (FCPublishCommand, error) {
	if raw.Name != "FCPublish" {
		return FCPublishCommand{}, fmt.Errorf("invalid cmd name for FCPublish: %s", raw.Name)
	}

	if raw.StreamID != 0 {
		return FCPublishCommand{}, fmt.Errorf("FCPublish can only be invoked on stream 0")
	}

	if len(raw.Objects) < 2 {
		return FCPublishCommand{}, fmt.Errorf("insufficient FCPublish arguments: %+v", raw.Objects)
	}

	name, ok := raw.Objects[1].(string)
	if !ok {
		return FCPublishCommand{}, fmt.Errorf("invalid FCPublish stream name: %#v", raw.Objects[1])
	}

	return FCPublishCommand{
		Name: name,
	}, nil
}

type ResultCommand struct {
	StreamID      uint32
	TransactionID uint32

	Properties interface{}
	Info       interface{}
}

func (cmd ResultCommand) RawCommand() RawCommand {
	return RawCommand{
		Name:          "_result",
		StreamID:      cmd.StreamID,
		TransactionID: cmd.TransactionID,
		Objects:       []interface{}{cmd.Properties, cmd.Info},
	}
}

func ParseResultCommand(raw RawCommand) (ResultCommand, error) {
	if raw.Name != "_result" {
		return ResultCommand{}, fmt.Errorf("invalid cmd name for _result: %s", raw.Name)
	}

	if len(raw.Objects) < 2 {
		return ResultCommand{}, fmt.Errorf("insufficient _result arguments: %+v", raw.Objects)
	}

	return ResultCommand{
		StreamID:      raw.StreamID,
		TransactionID: raw.TransactionID,
		Properties:    raw.Objects[0],
		Info:          raw.Objects[1],
	}, nil
}

type ErrorCommand struct {
}

func (cmd ErrorCommand) RawCommand() RawCommand {
	panic("not implemented")
}

func ParseErrorCommand(raw RawCommand) (ErrorCommand, error) {
	panic("not implemented")
}

type OnFCPublishCommand struct {
	Status Status
}

func (cmd OnFCPublishCommand) RawCommand() RawCommand {
	return RawCommand{
		Name:    "onFCPublish",
		Objects: []interface{}{nil, cmd.Status},
	}
}

func ParseOnFCPublishCommand(raw RawCommand) (OnFCPublishCommand, error) {
	panic("not implemented")
}
