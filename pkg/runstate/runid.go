package runstate

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// GenerateRunID returns a cryptographically random run identifier with a
// "run-" prefix and 128 bits of entropy. It is the single canonical
// implementation shared by the framework facade, the runtime engine, the
// event router, and the async HTTP adapter (which previously each carried a
// private 64-bit copy). It falls back to a nanosecond timestamp on the rare
// occasion that the random reader fails.
func GenerateRunID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	return "run-" + hex.EncodeToString(b[:])
}
