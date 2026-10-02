// Package docs converts OpenAPI documents into Gravity api blocks.
package docs

import (
	"crypto/sha256"
	"encoding/hex"
)

// HashBytes returns "sha256:<hex>" of raw bytes.
func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
