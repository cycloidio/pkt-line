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
)

type UploadResponseState int

const (
	UploadResponseBegin UploadResponseState = iota
	UploadResponseScanShallows
	UploadResponseScanUnshallows
	UploadResponseBeginAcknowledgements
	UploadResponseScanAcknowledgements
	UploadResponseScanPacks
	UploadResponseEnd
)

// UploadResponseChunk is a chunk of a protocol v1 git-upload-pack
// response.
type UploadResponseChunk struct {
	ShallowObjectID   string
	UnshallowObjectID string
	EndOfShallows     bool
	AckObjectID       string
	AckDetail         string
	Nak               bool
	PackStream        []byte
	PackRepo          any
	EndOfRequest      bool
}

// EncodeToPktLine serializes the chunk.
func (c *UploadResponseChunk) EncodeToPktLine() []byte {
	if c.ShallowObjectID != "" {
		return BytesPacket([]byte(fmt.Sprintf("shallow %s\n", c.ShallowObjectID))).EncodeToPktLine()
	}
	if c.UnshallowObjectID != "" {
		return BytesPacket([]byte(fmt.Sprintf("unshallow %s\n", c.UnshallowObjectID))).EncodeToPktLine()
	}
	if c.EndOfShallows {
		return FlushPacket{}.EncodeToPktLine()
	}
	if c.AckObjectID != "" {
		if c.AckDetail != "" {
			return BytesPacket([]byte(fmt.Sprintf("ACK %s %s\n", c.AckObjectID, c.AckDetail))).EncodeToPktLine()
		}
		return BytesPacket([]byte(fmt.Sprintf("ACK %s\n", c.AckObjectID))).EncodeToPktLine()
	}
	if c.Nak {
		return BytesPacket([]byte("NAK\n")).EncodeToPktLine()
	}
	if len(c.PackStream) != 0 {
		return BytesPacket(c.PackStream).EncodeToPktLine()
	}
	if c.EndOfRequest {
		return FlushPacket{}.EncodeToPktLine()
	}
	panic("impossible chunk")
}

// UploadResponse provides an interface for reading a protocol v1
// git-upload-pack response.
type UploadResponse struct {
	scanner *PacketScanner
	state   UploadResponseState
	err     error
	curr    *UploadResponseChunk
}

// NewUploadResponse returns a new ProtocolV1UploadPackResponse to
// read from rd.
func NewUploadResponse(rd io.Reader) *UploadResponse {
	return &UploadResponse{scanner: NewPacketScanner(rd)}
}

// Err returns the first non-EOF error that was encountered by the
// ProtocolV1UploadPackResponse.
func (r *UploadResponse) Err() error {
	return r.err
}

// Chunk returns the most recent chunk generated by a call to Scan.
func (r *UploadResponse) Chunk() *UploadResponseChunk {
	return r.curr
}

// Scan advances the scanner to the next packet. It returns false when the scan
// stops, either by reaching the end of the input or an error. After scan
// returns false, the Err method will return any error that occurred during
// scanning, except that if it was io.EOF, Err will return nil.
func (r *UploadResponse) Scan() bool {
	if r.err != nil || r.state == UploadResponseEnd {
		return false
	}
	if !r.scanner.Scan() {
		if r.scanner.Err() == nil {
			switch r.state {
			case UploadResponseBeginAcknowledgements, UploadResponseScanPacks:
			default:
				r.err = SyntaxError("early EOF")
			}
		}
		return false
	}
	pkt := r.scanner.Packet()

	switch r.state {
	case UploadResponseBegin, UploadResponseScanShallows:
		if bp, ok := pkt.(BytesPacket); ok {
			if bytes.HasPrefix(bp, []byte("shallow ")) {
				ss := strings.SplitN(strings.TrimSuffix(string(bp), "\n"), " ", 2)
				if len(ss) < 2 {
					r.err = SyntaxError("cannot split shallow: " + string(bp))
					return false
				}
				r.state = UploadResponseScanShallows
				r.curr = &UploadResponseChunk{
					ShallowObjectID: ss[1],
				}
				return true
			}
		}
		fallthrough
	case UploadResponseScanUnshallows:
		if bp, ok := pkt.(BytesPacket); ok {
			if bytes.HasPrefix(bp, []byte("unshallow ")) {
				ss := strings.SplitN(strings.TrimSuffix(string(bp), "\n"), " ", 2)
				if len(ss) < 2 {
					r.err = SyntaxError("cannot split unshallow: " + string(bp))
					return false
				}
				r.state = UploadResponseScanUnshallows
				r.curr = &UploadResponseChunk{
					UnshallowObjectID: ss[1],
				}
				return true
			}
		}
		if _, ok := pkt.(FlushPacket); ok {
			r.state = UploadResponseBeginAcknowledgements
			r.curr = &UploadResponseChunk{
				EndOfShallows: true,
			}
			return true
		}
		fallthrough
	case UploadResponseBeginAcknowledgements, UploadResponseScanAcknowledgements:
		if bp, ok := pkt.(BytesPacket); ok {
			if bytes.HasPrefix(bp, []byte("ACK ")) {
				ss := strings.SplitN(strings.TrimSuffix(string(bp), "\n"), " ", 3)
				if len(ss) < 2 {
					r.err = SyntaxError("cannot split ACK: " + string(bp))
					return false
				}
				detail := ""
				if len(ss) == 3 {
					detail = ss[2]
				}
				r.state = UploadResponseScanAcknowledgements
				r.curr = &UploadResponseChunk{
					AckObjectID: ss[1],
					AckDetail:   detail,
				}
				return true
			}
			if bytes.Equal(bp, []byte("NAK\n")) {
				r.state = UploadResponseScanPacks
				r.curr = &UploadResponseChunk{
					Nak: true,
				}
				return true
			}
		}
		if r.state == UploadResponseBegin {
			r.err = SyntaxError(fmt.Sprintf("unexpected packet: %#v", pkt))
			return false
		}
		fallthrough
	case UploadResponseScanPacks:
		switch p := pkt.(type) {
		case FlushPacket:
			r.state = UploadResponseEnd
			r.curr = &UploadResponseChunk{
				EndOfRequest: true,
			}
			return true
		case BytesPacket:
			r.state = UploadResponseScanPacks
			r.curr = &UploadResponseChunk{
				PackStream: p,
			}
			return true
		case PackFilePacket:
			r.state = UploadResponseScanPacks
			r.curr = &UploadResponseChunk{
				PackStream: p,
			}
			return true
		case PackFileIndicatorPacket:
			r.state = UploadResponseScanPacks
			return true
		default:
			r.err = SyntaxError(fmt.Sprintf("unexpected packet: %#v", p))
			return false
		}
	}
	panic("impossible state")
}
