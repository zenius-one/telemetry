package readthrough

import "encoding/binary"

const (
	kindValue     byte = 1
	kindTombstone byte = 2
	envelopeHdr        = 9
)

// envelope is what is stored: a kind, the soft expiry (unix nanos, value kind only) and the
// payload. The store's own TTL is the hard expiry.
type envelope struct {
	kind    byte
	softExp int64
	payload []byte
}

func (e envelope) encode() []byte {
	b := make([]byte, envelopeHdr+len(e.payload))
	b[0] = e.kind
	binary.BigEndian.PutUint64(b[1:9], uint64(e.softExp))
	copy(b[envelopeHdr:], e.payload)
	return b
}

// decode reports ok=false for anything that is not a well-formed envelope (a shape change between
// deploys, a foreign write): the caller treats that as a miss.
func decode(b []byte) (envelope, bool) {
	if len(b) < envelopeHdr || (b[0] != kindValue && b[0] != kindTombstone) {
		return envelope{}, false
	}
	return envelope{
		kind:    b[0],
		softExp: int64(binary.BigEndian.Uint64(b[1:9])),
		payload: b[envelopeHdr:],
	}, true
}
