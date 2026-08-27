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
	f, wire, payload, _, err := r.next()
	if err != nil || f != 1 || wire != 2 || string(payload) != "hello" {
		t.Fatalf("field1: f=%d wire=%d payload=%q err=%v", f, wire, payload, err)
	}
	var num uint64
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

// Real Steam BeginAuthSessionViaCredentials responses carry `interval` as a
// protobuf float (wire type 5) and other responses may carry fixed64 fields
// (wire type 1). Decoders that only understand varint/length-delimited must
// still walk past those fields — a wire-5 field previously aborted login with
// "steam: bad protobuf: wire type 5". (Upstream proto: field 3 interval=float.)
func TestPBReaderSkipsFixedFields(t *testing.T) {
	var w pbWriter
	w.str(1, "client")
	w.f32(3, 5.0)               // BeginAuthSession interval (float)
	w.f64(5, 76561199000000000) // fixed64, e.g. steamid-shaped field
	w.str(2, "reqid")
	r := newPBReader(w.buf)

	f, wire, payload, _, err := r.next()
	if err != nil || f != 1 || wire != 2 || string(payload) != "client" {
		t.Fatalf("field1: f=%d wire=%d payload=%q err=%v", f, wire, payload, err)
	}
	var num uint64
	f, wire, _, _, err = r.next()
	if err != nil || f != 3 || wire != 5 {
		t.Fatalf("field3 (float): f=%d wire=%d err=%v", f, wire, err)
	}
	f, wire, _, num, err = r.next()
	if err != nil || f != 5 || wire != 1 || num != 76561199000000000 {
		t.Fatalf("field5 (fixed64): f=%d wire=%d num=%d err=%v", f, wire, num, err)
	}
	f, wire, payload, _, err = r.next()
	if err != nil || f != 2 || wire != 2 || string(payload) != "reqid" {
		t.Fatalf("field2: f=%d wire=%d payload=%q err=%v", f, wire, payload, err)
	}
	if _, _, _, _, err = r.next(); !errors.Is(err, errProtoDone) {
		t.Fatalf("expected done, got %v", err)
	}
}

func TestPBReaderTruncatedFixedFields(t *testing.T) {
	for name, raw := range map[string][]byte{
		"fixed64": {0x21, 1, 2, 3}, // field 4, wire 1, 3 of 8 bytes
		"fixed32": {0x2d, 1, 2},    // field 5, wire 5, 2 of 4 bytes
	} {
		r := newPBReader(raw)
		if _, _, _, _, err := r.next(); !errors.Is(err, errProto) {
			t.Fatalf("%s: truncated fixed field must error, got %v", name, err)
		}
	}
}

func TestPBReaderStillRejectsGroups(t *testing.T) {
	r := newPBReader([]byte{0x0b}) // field 1, wire 3 (start group)
	if _, _, _, _, err := r.next(); !errors.Is(err, errProto) {
		t.Fatalf("group wire types must stay unsupported, got %v", err)
	}
}

// End-to-end: the BeginAuthSession mock must mirror the real response shape,
// including the float interval field, so this regression cannot reappear.
func TestDecodeBeginResponseWithInterval(t *testing.T) {
	var w pbWriter
	w.u64(1, 42)
	w.bytes(2, []byte("reqid"))
	w.f32(3, 5.0)
	var inner pbWriter
	inner.u64(1, 3)
	w.bytes(4, inner.buf)
	w.u64(5, 76561199000000000)
	out, err := decodeBeginResponse(w.buf)
	if err != nil {
		t.Fatalf("real-shaped response must decode: %v", err)
	}
	if out.ClientID != 42 || out.SteamID != 76561199000000000 ||
		string(out.RequestID) != "reqid" ||
		len(out.AllowedConfirmations) != 1 || out.AllowedConfirmations[0] != 3 {
		t.Fatalf("decoded: %+v", out)
	}
}
