// Package steam implements a minimal Steam web session client:
// protobuf-based IAuthenticationService login, token refresh,
// trade-offer polling/acceptance and mobile confirmations.
//
// Behavioral spec: docs/knowledge/design/platform-steam-api-notes.md
package steam

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Minimal protobuf wire-format writer/reader for the handful of
// IAuthenticationService messages we need (field numbers verified against
// SteamKit steammessages_auth.steamclient.proto via the upstream compiled pb2).

type pbWriter struct{ buf []byte }

func (w *pbWriter) u64(field uint32, v uint64) {
	w.tag(field, 0)
	var tmp [10]byte
	n := binary.PutUvarint(tmp[:], v)
	w.buf = append(w.buf, tmp[:n]...)
}

func (w *pbWriter) b32(field uint32, v bool) {
	if v {
		w.u64(field, 1)
	}
}

func (w *pbWriter) str(field uint32, s string) { w.bytes(field, []byte(s)) }

func (w *pbWriter) bytes(field uint32, b []byte) {
	w.tag(field, 2)
	var tmp [10]byte
	n := binary.PutUvarint(tmp[:], uint64(len(b)))
	w.buf = append(w.buf, tmp[:n]...)
	w.buf = append(w.buf, b...)
}

func (w *pbWriter) tag(field, wire uint32) {
	var tmp [10]byte
	n := binary.PutUvarint(tmp[:], uint64(field<<3|wire))
	w.buf = append(w.buf, tmp[:n]...)
}

// ---- readers ----

var errProto = errors.New("steam: bad protobuf")

type pbReader struct{ buf []byte }

func newPBReader(b []byte) *pbReader { return &pbReader{buf: b} }

// next walks fields; returns field number, wire type, payload.
func (r *pbReader) next() (field, wire uint32, payload []byte, num uint64, err error) {
	if len(r.buf) == 0 {
		return 0, 0, nil, 0, errProtoDone
	}
	key, n := binary.Uvarint(r.buf)
	if n <= 0 {
		return 0, 0, nil, 0, fmt.Errorf("%w: key", errProto)
	}
	r.buf = r.buf[n:]
	field, wire = uint32(key>>3), uint32(key&7)
	switch wire {
	case 0:
		v, n := binary.Uvarint(r.buf)
		if n <= 0 {
			return 0, 0, nil, 0, fmt.Errorf("%w: varint", errProto)
		}
		r.buf = r.buf[n:]
		return field, wire, nil, v, nil
	case 2:
		l, n := binary.Uvarint(r.buf)
		// Compare in the uint64 domain: a hostile length ≥ 2^63 makes int(l)
		// negative and the naive `len-n < int(l)` check pass through to a
		// panicking slice expression.
		if n <= 0 || l > uint64(len(r.buf)-n) {
			return 0, 0, nil, 0, fmt.Errorf("%w: length", errProto)
		}
		payload = r.buf[n : n+int(l)]
		r.buf = r.buf[n+int(l):]
		return field, wire, payload, 0, nil
	default:
		return 0, 0, nil, 0, fmt.Errorf("%w: wire type %d", errProto, wire)
	}
}

var errProtoDone = errors.New("steam: end of message")
