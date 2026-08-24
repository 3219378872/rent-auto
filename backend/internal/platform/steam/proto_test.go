package steam

import (
	"encoding/binary"
	"errors"
	"testing"
)

func TestPBReaderRoundTrip(t *testing.T) {
	var w pbWriter
	w.str(1, "hello")
	w.u64(2, 300)
	w.b32(3, true)
	r := newPBReader(w.buf)
	f, wire, payload, num, err := r.next()
	if err != nil || f != 1 || wire != 2 || string(payload) != "hello" {
		t.Fatalf("field1: f=%d wire=%d payload=%q err=%v", f, wire, payload, err)
	}
	f, wire, payload, num, err = r.next()
	if err != nil || f != 2 || wire != 0 || num != 300 || payload != nil {
		t.Fatalf("field2: f=%d wire=%d num=%d err=%v", f, wire, num, err)
	}
	f, wire, _, _, err = r.next()
	if err != nil || f != 3 || wire != 0 || num == 0 {
		t.Fatalf("field3 (bool): f=%d wire=%d num=%d err=%v", f, wire, num, err)
	}
	if _, _, _, _, err = r.next(); !errors.Is(err, errProtoDone) {
		t.Fatalf("expected done, got %v", err)
	}
}

// A length varint ≥ 2^63 is a legal uint64 but a hostile/absurd declared
// length: int(l) wraps negative on 64-bit and the naive bound check
// `len(buf)-n < int(l)` passes, panicking on the slice below. The reader must
// reject it in the uint64 domain instead (remote-crash regression).
func TestPBReaderRejectsHostileLength(t *testing.T) {
	hostile := []byte{0x12} // field 1, wire type 2 (length-delimited)
	var tmp [10]byte
	n := binary.PutUvarint(tmp[:], ^uint64(0)) // 2^64-1
	hostile = append(hostile, tmp[:n]...)
	hostile = append(hostile, []byte("evil")...)

	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("hostile length must not panic: %v", p)
		}
	}()
	r := newPBReader(hostile)
	_, _, _, _, err := r.next()
	if !errors.Is(err, errProto) {
		t.Fatalf("expected errProto, got %v", err)
	}
}

func TestPBReaderTruncatedPayload(t *testing.T) {
	r := newPBReader([]byte{0x12, 0x20, 'x'}) // declares 32 bytes, has 1
	if _, _, _, _, err := r.next(); !errors.Is(err, errProto) {
		t.Fatalf("truncated payload must error, got %v", err)
	}
}
