// Modified by Giacomo Tartari
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

package pkt

import (
	"fmt"
	"io"
	"strings"
)

type ReceiveResponseState int

const (
	ReceiveResponseBegin ReceiveResponseState = iota
	ReceiveResponseScanResult
	ReceiveResponseEnd
)

// ReceiveResponseChunk is a chunk of a protocol v1
// git-receive-pack response.
type ReceiveResponseChunk struct {
	UnpackStatus         string
	RefUpdateStatus      string
	RefName              string
	RefUpdateFailMessage string
	EndOfResponse        bool
}

// EncodeToPktLine serializes the chunk.
func (c *ReceiveResponseChunk) EncodeToPktLine() []byte {
	if c.UnpackStatus != "" {
		return BytesPacket([]byte(fmt.Sprintf("unpack %s\n", c.UnpackStatus))).EncodeToPktLine()
	}
	if c.RefUpdateStatus != "" {
		if c.RefUpdateFailMessage == "" {
			return BytesPacket([]byte(fmt.Sprintf("%s %s\n", c.RefUpdateStatus, c.RefName))).EncodeToPktLine()
		}
		return BytesPacket([]byte(fmt.Sprintf("%s %s %s\n", c.RefUpdateStatus, c.RefName, c.RefUpdateFailMessage))).EncodeToPktLine()
	}
	if c.EndOfResponse {
		return FlushPacket{}.EncodeToPktLine()
	}
	panic("impossible chunk")
}

// ReceiveResponse provides an interface for reading a protocol v1
// git-receive-pack response.
type ReceiveResponse struct {
	scanner *PacketScanner
	state   ReceiveResponseState
	err     error
	curr    *ReceiveResponseChunk
}

// NewReceiveResponse returns a new ReceiveResponse
// to read from rd.
func NewReceiveResponse(rd io.Reader) *ReceiveResponse {
	return &ReceiveResponse{scanner: NewPacketScanner(rd)}
}

// Err returns the first non-EOF error that was encountered by the
// ProtocolV1ReceivePackResponse.
func (r *ReceiveResponse) Err() error {
	return r.err
}

// Chunk returns the most recent response chunk generated by a call to Scan.
func (r *ReceiveResponse) Chunk() *ReceiveResponseChunk {
	return r.curr
}

// Scan advances the scanner to the next packet. It returns false when the scan
// stops, either by reaching the end of the input or an error. After scan
// returns false, the Err method will return any error that occurred during
// scanning, except that if it was io.EOF, Err will return nil.
func (r *ReceiveResponse) Scan() bool {
	if r.err != nil || r.state == ReceiveResponseEnd {
		return false
	}
	if !r.scanner.Scan() {
		r.err = r.scanner.Err()
		if r.err == nil && r.state != ReceiveResponseBegin {
			r.err = SyntaxError("early EOF")
		}
		return false
	}
	pkt := r.scanner.Packet()
	switch r.state {
	case ReceiveResponseBegin:
		bp, ok := pkt.(BytesPacket)
		if !ok {
			r.err = SyntaxError(fmt.Sprintf("unexpected packet: %#v", pkt))
			return false
		}
		s := strings.TrimSuffix(string(bp), "\n")
		if !strings.HasPrefix(s, "unpack ") {
			r.err = SyntaxError(fmt.Sprintf("unexpected packet: %#v", s))
			return false
		}
		r.state = ReceiveResponseScanResult
		r.curr = &ReceiveResponseChunk{
			UnpackStatus: strings.SplitN(s, " ", 2)[1],
		}
		return true
	case ReceiveResponseScanResult:
		switch p := pkt.(type) {
		case FlushPacket:
			r.state = ReceiveResponseEnd
			r.curr = &ReceiveResponseChunk{
				EndOfResponse: true,
			}
			return true
		case BytesPacket:
			s := strings.TrimSuffix(string(p), "\n")
			if strings.HasPrefix(s, "ok ") {
				ss := strings.SplitN(s, " ", 2)
				r.curr = &ReceiveResponseChunk{
					RefUpdateStatus: ss[0],
					RefName:         ss[1],
				}
				return true
			}
			if strings.HasPrefix(s, "ng ") {
				ss := strings.SplitN(s, " ", 3)
				if len(ss) != 3 {
					r.err = SyntaxError("cannot split into three: " + s)
					return false
				}
				r.curr = &ReceiveResponseChunk{
					RefUpdateStatus:      ss[0],
					RefName:              ss[1],
					RefUpdateFailMessage: ss[2],
				}
				return true
			}
			r.err = SyntaxError(fmt.Sprintf("unexpected packet: %#v", p))
			return false
		default:
			r.err = SyntaxError(fmt.Sprintf("unexpected packet: %#v", p))
			return false
		}
	}
	panic("impossible state")
}
