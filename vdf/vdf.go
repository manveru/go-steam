package vdf

import (
	"bytes"
	"encoding/binary"
)

const (
	None       = 0
	String     = 1
	Int32      = 2
	Float32    = 3
	Pointer    = 4
	WideString = 5
	Color      = 6
	UInt64     = 7
	End        = 8
)

func ParseBytes(b []byte) (kv map[string]interface{}, err error) {
	return Parse(bytes.NewBuffer(b))
}

func Parse(buffer *bytes.Buffer) (kv map[string]interface{}, err error) {
	kv = map[string]interface{}{}

	var dataType uint8
	var name string
	for {
		if dataType, err = readUInt8LE(buffer); err != nil {
			return
		}
		if dataType == End {
			break
		}

		if name, err = readCString(buffer); err != nil {
			return
		}

		switch dataType {
		case None:
			kv[name], err = Parse(buffer)
		case String:
			kv[name], err = readCString(buffer)
		case Int32:
		case Color:
		case Pointer:
			kv[name], err = readInt32LE(buffer)
		case UInt64:
			kv[name], err = readUInt64LE(buffer)
		case Float32:
			kv[name], err = readFloat32LE(buffer)
		}
		if err != nil {
			return
		}
	}

	return kv, err
}

func readCString(buffer *bytes.Buffer) (string, error) {
	name, err := buffer.ReadString('\000')
	if err == nil {
		name = name[:len(name)-1]
	}
	return name, err
}

func readUInt8LE(buffer *bytes.Buffer) (uint8, error) {
	var v uint8
	err := binary.Read(buffer, binary.LittleEndian, &v)
	return v, err
}

func readInt32LE(buffer *bytes.Buffer) (int32, error) {
	var v int32
	err := binary.Read(buffer, binary.LittleEndian, &v)
	return v, err
}

func readUInt64LE(buffer *bytes.Buffer) (uint64, error) {
	var v uint64
	err := binary.Read(buffer, binary.LittleEndian, &v)
	return v, err
}

func readFloat32LE(buffer *bytes.Buffer) (float32, error) {
	var v float32
	err := binary.Read(buffer, binary.LittleEndian, &v)
	return v, err
}
