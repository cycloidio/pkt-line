// Copyright 2018 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package gitprotocolio

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
)

// SyntaxError is an error returned when the parser cannot parse the input.
type SyntaxError string

func (s SyntaxError) Error() string { return string(s) }

// Packet is the interface that wraps a packet line.
type Packet interface {
	EncodeToPktLine() []byte
}

// FlushPacket is the flush packet ("0000").
type FlushPacket struct{}

// EncodeToPktLine serializes the packet.
func (FlushPacket) EncodeToPktLine() []byte {
	return []byte("0000")
}

// DelimPacket is the delim packet ("0001").
type DelimPacket struct{}

// EncodeToPktLine serializes the packet.
func (DelimPacket) EncodeToPktLine() []byte {
	return []byte("0001")
}

// BytesPacket is a packet with a content.
type BytesPacket []byte

// EncodeToPktLine serializes the packet.
func (b BytesPacket) EncodeToPktLine() []byte {
	sz := len(b)
	if sz > 0xFFFF-4 {
		panic("content too large")
	}
	return append([]byte(fmt.Sprintf("%04x", sz+4)), b...)
}

// ErrorPacket is a packet that indicates an error.
type ErrorPacket string

func (e ErrorPacket) Error() string { return "error: " + string(e) }

// EncodeToPktLine serializes the packet.
func (e ErrorPacket) EncodeToPktLine() []byte {
	bs := []byte("ERR " + e)
	sz := len(bs)
	if sz > 0xFFFF {
		panic("content too large")
	}
	return append([]byte(fmt.Sprintf("%04X", sz+4)), bs...)
}

// PackFileIndicatorPacket is the indicator of the beginning of the pack file
// ("PACK").
type PackFileIndicatorPacket struct{}

// EncodeToPktLine serializes the packet.
func (PackFileIndicatorPacket) EncodeToPktLine() []byte {
	return []byte("PACK")
}

// PackFilePacket is a chunk of the pack file.
type PackFilePacket []byte

// EncodeToPktLine serializes the packet.
func (p PackFilePacket) EncodeToPktLine() []byte {
	return []byte(p)
}

// PacketScanner provides an interface for reading packet line data. The usage
// is same as bufio.Scanner.
type PacketScanner struct {
	err          error
	curr         Packet
	packFileMode bool
	scanner      *bufio.Scanner
}

// NewPacketScanner returns a new PacketScanner to read from r.
func NewPacketScanner(r io.Reader) *PacketScanner {
	s := &PacketScanner{scanner: bufio.NewScanner(r)}
	s.scanner.Split(s.packetSplitFunc)
	return s
}

// Err returns the first non-EOF error that was encountered by the
// PacketScanner.
func (s *PacketScanner) Err() error {
	return s.err
}

// Packet returns the most recent packet generated by a call to Scan.
func (s *PacketScanner) Packet() Packet {
	return s.curr
}

// Scan advances the scanner to the next packet. It returns false when the scan
// stops, either by reaching the end of the input or an error. After scan
// returns false, the Err method will return any error that occurred during
// scanning, except that if it was io.EOF, Err will return nil.
func (s *PacketScanner) Scan() bool {
	if s.err != nil {
		return false
	}
	if !s.scanner.Scan() {
		s.err = s.scanner.Err()
		return false
	}

	bs := s.scanner.Bytes()
	if s.packFileMode {
		if len(bs) == 0 {
			// EOF
			return false
		}
		s.curr = PackFilePacket(bs)
		return true
	}
	if bytes.Equal(bs, []byte("0000")) {
		s.curr = FlushPacket{}
		return true
	}
	if bytes.Equal(bs, []byte("0001")) {
		s.curr = DelimPacket{}
		return true
	}
	if bytes.Equal(bs, []byte("PACK")) {
		s.packFileMode = true
		s.curr = PackFileIndicatorPacket{}
		return true
	}
	if len(bs) == 4 {
		s.err = SyntaxError("unknown special packet: " + string(bs))
		return false
	}
	if bytes.Equal(bs[4:8], []byte("ERR ")) {
		s.err = ErrorPacket(string(bs[8:]))
		return false
	}
	s.curr = BytesPacket(bs[4:])
	return true
}

func (s *PacketScanner) packetSplitFunc(data []byte, atEOF bool) (int, []byte, error) {
	if s.packFileMode {
		return len(data), data, nil
	}
	if len(data) < 4 {
		return 0, nil, nil
	}
	if bytes.HasPrefix(data, []byte("PACK")) {
		return 4, data[:4], nil
	}
	sz, err := strconv.ParseUint(string(data[:4]), 16, 32)
	if err != nil {
		return 0, nil, err
	}
	if sz == 0 || sz == 1 {
		// Special packet.
		return 4, data[:4], nil
	}
	if len(data) < int(sz) {
		return 0, nil, nil
	}
	return int(sz), data[:int(sz)], nil
}
