package crypto

import (
	"crypto/sha3"

	"github.com/pkg/errors"
)

// The garble is what a locked vault's row SAYS. An Encrypted node's name column
// holds it verbatim, so every surface that was never taught about encryption —
// `lflow list`, an export, the mobile view, a raw sqlite3 dump — shows the same
// meaningless run of base64 noise rather than the title. That is the point: the
// protection is not that those surfaces politely hide the text, it is that they
// have never been given it.
//
// Two properties matter and neither is cosmetic:
//
//   - It is derived from the WRAP, not the content. Re-sealing an edited subtree
//     draws a fresh nonce and a fresh box, but leaves the wrapped content key
//     alone — so the garble does not change on every save. A row that reshuffled
//     its noise each time you typed would be a modification oracle sitting in
//     plain sight of anyone reading the file.
//   - It is a FIXED length. Sizing it to the plaintext would leak how much is in
//     the vault to anyone who can see the row.
const garbleLen = 32

// garbleAlphabet is base64's, because base64 is what people read as "this is
// encrypted" — the noise should announce itself, not look like a lost password.
const garbleAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// Garble is the locked row's text for this envelope.
func (e *Envelope) Garble() string { return GarbleOf(e.header.KDF.Salt, e.wrapped) }

// GarbleOf derives the noise from arbitrary stable bytes. Exported so a node
// that has lost or never had its envelope can still show garble instead of an
// empty row — a blank line would advertise "this vault is broken", which is
// both alarming and more information than a bystander should get.
func GarbleOf(parts ...[]byte) string {
	h := sha3.NewSHAKE256()
	h.Write([]byte(infoGarble))
	for _, p := range parts {
		h.Write(p)
	}
	raw := make([]byte, garbleLen)
	if _, err := h.Read(raw); err != nil {
		// SHAKE256 is an infinite stream and its Read cannot fail; a fixed
		// fallback keeps the row non-empty if that ever stops being true.
		return "????????????????????????????????"
	}
	out := make([]byte, garbleLen)
	for i, b := range raw {
		out[i] = garbleAlphabet[int(b)%len(garbleAlphabet)]
	}
	return string(out)
}

// IsGarble reports whether s looks like a garble this package produced — a
// fixed-length run of the base64 alphabet. The editor uses it as a guard before
// overwriting a locked node's name, so a row that somehow holds real text
// (a hand-edited database, a half-finished conversion) is never quietly
// clobbered by a re-seal.
func IsGarble(s string) bool {
	if len(s) != garbleLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		found := false
		for j := 0; j < len(garbleAlphabet); j++ {
			if s[i] == garbleAlphabet[j] {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// ErrNoEnvelope marks a node typed as encrypted that has no sealed blob behind
// it yet — a fresh conversion the user has not chosen factors for.
var ErrNoEnvelope = errors.New("This node has no sealed vault yet")
