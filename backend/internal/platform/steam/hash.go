package steam

import "crypto/sha1"

func sha1Sum(b []byte) []byte {
	s := sha1.Sum(b)
	return s[:]
}
