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
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/cycloidio/pkt-line"
)

type RequestState int

const (
	RequestBegin RequestState = iota
	RequestScanCapabilities
	RequestScanArguments
	RequestEnd
)

// RequestChunk is a chunk of a protocol v2 request.
type RequestChunk struct {
	Command       string
	Capability    string
	EndCapability bool
	Argument      []byte
	EndArgument   bool
	EndRequest    bool
}

// EncodeToPktLine serializes the chunk.
func (c *RequestChunk) EncodeToPktLine() []byte {
	if c.Command != "" {
		return pkt.BytesPacket([]byte(fmt.Sprintf("command=%s\n", c.Command))).EncodeToPktLine()
	}
	if c.Capability != "" {
		return pkt.BytesPacket([]byte(c.Capability + "\n")).EncodeToPktLine()
	}
	if c.EndCapability {
		return pkt.DelimPacket{}.EncodeToPktLine()
	}
	if len(c.Argument) != 0 {
		return pkt.BytesPacket(c.Argument).EncodeToPktLine()
	}
	if c.EndArgument || c.EndRequest {
		return pkt.FlushPacket{}.EncodeToPktLine()
	}
	panic("impossible chunk")
}

// Request provides an interface for reading a protocol v2 request.
type Request struct {
	scanner *pkt.PacketScanner
	state   RequestState
	err     error
	curr    *RequestChunk
}

// NewRequest returns a new ProtocolV2Request to read from rd.
func NewRequest(rd io.Reader) *Request {
	return &Request{scanner: pkt.NewPacketScanner(rd)}
}

// Err returns the first non-EOF error that was encountered by the
// ProtocolV2Request.
func (r *Request) Err() error {
	return r.err
}

// Chunk returns the most recent request chunk generated by a call to Scan.
//
// The underlying array of Argument may point to data that will be overwritten
// by a subsequent call to Scan. It does no allocation.
func (r *Request) Chunk() *RequestChunk {
	return r.curr
}

// Scan advances the scanner to the next packet. It returns false when the scan
// stops, either by reaching the end of the input or an error. After scan
// returns false, the Err method will return any error that occurred during
// scanning, except that if it was io.EOF, Err will return nil.
func (r *Request) Scan() bool {
	if r.err != nil || r.state == RequestEnd {
		return false
	}
	if !r.scanner.Scan() {
		r.err = r.scanner.Err()
		if r.err == nil && r.state != RequestBegin {
			r.err = pkt.SyntaxError("early EOF")
		}
		return false
	}
	packet := r.scanner.Packet()

	switch r.state {
	case RequestBegin:
		switch p := packet.(type) {
		case pkt.FlushPacket:
			r.state = RequestEnd
			r.curr = &RequestChunk{
				EndRequest: true,
			}
			return true
		case pkt.BytesPacket:
			if !bytes.HasPrefix(p, []byte("command=")) {
				r.err = pkt.SyntaxError(fmt.Sprintf("unexpected packet: %#v", p))
				return false
			}
			r.state = RequestScanCapabilities
			r.curr = &RequestChunk{
				Command: strings.TrimSuffix(strings.TrimPrefix(string(p), "command="), "\n"),
			}
			return true
		default:
			r.err = pkt.SyntaxError(fmt.Sprintf("unexpected packet: %#v", p))
			return false
		}
	case RequestScanCapabilities:
		switch p := packet.(type) {
		case pkt.DelimPacket:
			r.state = RequestScanArguments
			r.curr = &RequestChunk{
				EndCapability: true,
			}
			return true
		case pkt.BytesPacket:
			r.curr = &RequestChunk{
				Capability: strings.TrimSuffix(string(p), "\n"),
			}
			return true
		default:
			r.err = pkt.SyntaxError(fmt.Sprintf("unexpected packet: %#v", p))
			return false
		}
	case RequestScanArguments:
		switch p := packet.(type) {
		case pkt.FlushPacket:
			r.state = RequestBegin
			r.curr = &RequestChunk{
				EndArgument: true,
			}
			return true
		case pkt.BytesPacket:
			r.curr = &RequestChunk{
				Argument: p,
			}
			return true
		default:
			r.err = pkt.SyntaxError(fmt.Sprintf("unexpected packet: %#v", p))
			return false
		}
	}
	panic("impossible state")
}
