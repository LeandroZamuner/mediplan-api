// Package token genera tokens opacos aleatorios (enlaces de invitación). En la
// base solo se guarda el hash SHA-256; el token en claro viaja solo en el enlace.
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

type Generator struct{}

func (Generator) New() (raw, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, Generator{}.Hash(raw), nil
}

func (Generator) Hash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
