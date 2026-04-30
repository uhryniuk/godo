package proto

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
)

type sample struct {
	Op   string `json:"op"`
	Body string `json:"body"`
}

func TestRoundtrip(t *testing.T) {
	cases := []sample{
		{Op: "Ping"},
		{Op: "Run", Body: "echo hi"},
		{Op: "X", Body: strings.Repeat("a", 1024)},
		{Op: "Y", Body: "\x00\x01\x02 unicode: ñ ✓"},
	}
	for _, c := range cases {
		t.Run(c.Op, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteFrame(&buf, c); err != nil {
				t.Fatalf("write: %v", err)
			}
			var got sample
			if err := ReadFrame(&buf, &got); err != nil {
				t.Fatalf("read: %v", err)
			}
			if got != c {
				t.Fatalf("roundtrip mismatch:\n got=%+v\nwant=%+v", got, c)
			}
			if buf.Len() != 0 {
				t.Fatalf("trailing bytes after frame: %d", buf.Len())
			}
		})
	}
}

// byteAtATimeReader returns one byte per Read until exhausted.
type byteAtATimeReader struct{ buf []byte }

func (r *byteAtATimeReader) Read(p []byte) (int, error) {
	if len(r.buf) == 0 {
		return 0, io.EOF
	}
	p[0] = r.buf[0]
	r.buf = r.buf[1:]
	return 1, nil
}

func TestReadFramePartialReads(t *testing.T) {
	var buf bytes.Buffer
	want := sample{Op: "Run", Body: "split-across-reads"}
	if err := WriteFrame(&buf, want); err != nil {
		t.Fatalf("write: %v", err)
	}
	r := &byteAtATimeReader{buf: buf.Bytes()}
	var got sample
	if err := ReadFrame(r, &got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != want {
		t.Fatalf("partial-read mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}

func TestReadFrameOversizeRejected(t *testing.T) {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(MaxFrameSize+1))
	r := bytes.NewReader(hdr[:])
	var dst sample
	err := ReadFrame(r, &dst)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("expected ErrFrameTooLarge, got %v", err)
	}
}

func TestWriteFrameOversizeRejected(t *testing.T) {
	// Force an oversize body via a payload that marshals to >MaxFrameSize.
	big := strings.Repeat("a", MaxFrameSize+10)
	err := WriteFrame(io.Discard, sample{Op: "X", Body: big})
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("expected ErrFrameTooLarge, got %v", err)
	}
}

func TestReadFrameTruncatedHeader(t *testing.T) {
	r := bytes.NewReader([]byte{0x00, 0x01}) // only 2 of 4 length bytes
	var dst sample
	err := ReadFrame(r, &dst)
	if err == nil {
		t.Fatal("expected error on truncated header, got nil")
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF/UnexpectedEOF, got %v", err)
	}
}

func TestReadFrameTruncatedBody(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, sample{Op: "X", Body: "0123456789"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Lop off the last 5 bytes of the body.
	truncated := buf.Bytes()[:buf.Len()-5]
	r := bytes.NewReader(truncated)
	var dst sample
	err := ReadFrame(r, &dst)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected ErrUnexpectedEOF, got %v", err)
	}
}

func TestReadFrameInvalidJSON(t *testing.T) {
	body := []byte("{not json")
	var buf bytes.Buffer
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(body)))
	buf.Write(hdr[:])
	buf.Write(body)
	var dst sample
	err := ReadFrame(&buf, &dst)
	if err == nil {
		t.Fatal("expected JSON decode error, got nil")
	}
}

func TestReadFrameEmptyReader(t *testing.T) {
	var dst sample
	err := ReadFrame(bytes.NewReader(nil), &dst)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF on empty reader, got %v", err)
	}
}
