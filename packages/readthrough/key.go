package readthrough

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// JoinKey builds an opaque key from scope parts (tenant, region, entity, id, ...). The package
// does not know what the parts mean; the consuming service decides the scope and must always
// include whatever isolates one tenant/caller from another. ':' and '\' inside a part are escaped
// so two different part lists can never produce the same key.
func JoinKey(parts ...string) string {
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			b.WriteByte(':')
		}
		for j := 0; j < len(p); j++ {
			if p[j] == ':' || p[j] == '\\' {
				b.WriteByte('\\')
			}
			b.WriteByte(p[j])
		}
	}
	return b.String()
}

// HashKey returns a keyed hash (hex HMAC-SHA256) of v for use as a key part when the natural
// identifier is secret or personal (a phone number, a bearer token, an anchor value). Use a
// secret that is rotated by the consumer; rotating it simply invalidates the affected keys.
func HashKey(secret []byte, v string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(v))
	return hex.EncodeToString(m.Sum(nil))
}

func validateKey(key string) error {
	if key == "" || len(key) > MaxKeyLen {
		return ErrInvalidKey
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x20 || key[i] == 0x7f {
			return ErrInvalidKey
		}
	}
	return nil
}

const (
	valuePrefix = "v:"
	leasePrefix = "l:"
)
