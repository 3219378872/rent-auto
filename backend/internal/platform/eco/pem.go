package eco

import (
	"encoding/pem"
	"errors"
)

func decodePEM(data []byte) (*pem.Block, []byte) {
	block, rest := pem.Decode(data)
	return block, rest
}

var _ = errors.New
