// Package proto implements godo's CLI<->daemon wire protocol.
//
// Frames are length-prefixed JSON: a 4-byte big-endian length followed by
// that many bytes of UTF-8 JSON. Frames cap at MaxFrameSize.
//
// The codec is not safe for concurrent writers on the same writer. Callers
// that share a writer between goroutines must serialize externally.
package proto

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const MaxFrameSize = 4 << 20 // 4 MiB

var (
	ErrFrameTooLarge = errors.New("proto: frame exceeds max size")
)

// WriteFrame marshals payload as JSON and writes it as one length-prefixed frame.
func WriteFrame(w io.Writer, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if len(body) > MaxFrameSize {
		return ErrFrameTooLarge
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(body)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := w.Write(body); err != nil {
		return err
	}
	return nil
}

// ReadFrame reads one length-prefixed frame and unmarshals its body into dst.
func ReadFrame(r io.Reader, dst any) error {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > MaxFrameSize {
		return ErrFrameTooLarge
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return err
	}
	return json.Unmarshal(body, dst)
}
