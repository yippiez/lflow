package crypto

import (
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha3"
	"encoding/hex"

	"github.com/pkg/errors"
	"golang.org/x/crypto/argon2"
)

// Kind is one unlock input's flavour.
type Kind string

const (
	// KindPassword is a passphrase, stretched with Argon2id.
	KindPassword Kind = "password"
	// KindKeyfile is the contents of a file on disk — something you have that
	// the terminal never sees you type.
	KindKeyfile Kind = "keyfile"
	// KindToken is a hardware key answering a challenge (a YubiKey's HMAC-SHA1
	// challenge-response slot). The secret never leaves the device; the envelope
	// stores only the challenge that was put to it.
	KindToken Kind = "token"
)

// Factor is one required unlock input as recorded in a sealed header. It holds
// only public parameters — a challenge, a slot number, a filename to jog the
// memory. Nothing here helps an attacker who has the envelope.
type Factor struct {
	Kind Kind `json:"kind"`
	// Hint is a human reminder shown at the prompt (the keyfile's path as it
	// was when the vault was sealed). It is advisory: a keyfile that moved still
	// opens the vault, and the hint is never part of the derived key.
	Hint string `json:"hint,omitempty"`
	// Device names the token backend the challenge was answered by
	// ("ykchalresp", "ykman"); empty means probe for whichever is installed.
	Device string `json:"device,omitempty"`
	// Slot is the token's challenge-response slot (a YubiKey has 1 and 2).
	Slot int `json:"slot,omitempty"`
	// Challenge is the fixed random challenge this vault puts to the token. It
	// is generated once at seal and stored, because the response has to be
	// reproducible: a fresh challenge every time would derive a fresh key.
	Challenge []byte `json:"challenge,omitempty"`
}

// Label is the factor's name at a prompt.
func (f Factor) Label() string {
	switch f.Kind {
	case KindPassword:
		return "password"
	case KindKeyfile:
		return "keyfile"
	case KindToken:
		if f.Device != "" {
			return "token · " + f.Device
		}
		return "token"
	}
	return string(f.Kind)
}

// Secrets is what the user just supplied at the prompt — at most one of each
// kind, because a vault requires at most one of each kind.
type Secrets struct {
	Password []byte
	Keyfile  []byte // the keyfile's raw contents, already read
	// Token answers a hardware challenge. nil uses the exec backends
	// (see token.go); tests inject their own.
	Token TokenResponder
}

// Wipe zeroes the supplied secrets. Go's GC gives no guarantee that the
// original bytes are gone from memory, so this is hygiene rather than a
// promise — it shortens the window, it does not close it.
func (s *Secrets) Wipe() {
	for i := range s.Password {
		s.Password[i] = 0
	}
	for i := range s.Keyfile {
		s.Keyfile[i] = 0
	}
	s.Password, s.Keyfile = nil, nil
}

// Missing reports the factors in fs that s has nothing to answer with, so a
// prompt can ask for exactly those instead of failing after an Argon2id run.
func (s Secrets) Missing(fs []Factor) []Factor {
	var out []Factor
	for _, f := range fs {
		switch f.Kind {
		case KindPassword:
			if len(s.Password) == 0 {
				out = append(out, f)
			}
		case KindKeyfile:
			if len(s.Keyfile) == 0 {
				out = append(out, f)
			}
		}
	}
	return out
}

// Argon2id cost. Tuned for an interactive unlock on a laptop: 64 MiB and one
// pass is the RFC 9106 second-recommended profile, which lands around a tenth
// of a second here and costs a bulk cracker 64 MiB of memory per guess.
//
// These are vars, not consts, only so the package's own tests can seal at a
// cost that does not turn a table of round trips into minutes of Argon2id. The
// parameters an envelope was sealed with are recorded in its header, so raising
// them later re-locks nothing: old vaults keep opening at their old cost.
var (
	argonTime    uint32 = 1
	argonMemory  uint32 = 64 * 1024 // KiB
	argonThreads uint8  = 4
)

// KDF records how a password was stretched, so an envelope sealed under
// different cost parameters still opens.
type KDF struct {
	Name    string `json:"name"` // "argon2id"
	Salt    []byte `json:"salt"`
	Time    uint32 `json:"time"`
	Memory  uint32 `json:"memory"` // KiB
	Threads uint8  `json:"threads"`
}

func newKDF() (KDF, error) {
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return KDF{}, errors.Wrap(err, "drawing a kdf salt")
	}
	return KDF{Name: "argon2id", Salt: salt, Time: argonTime, Memory: argonMemory, Threads: argonThreads}, nil
}

// ErrFactorMissing is returned when the supplied secrets do not answer every
// factor the header declares. It is a prompt-again error, not a wrong-key one.
var ErrFactorMissing = errors.New("A required unlock factor was not supplied")

// factorSecret turns one factor plus what the user gave into its raw secret.
func factorSecret(f Factor, kdf KDF, s Secrets) ([]byte, error) {
	switch f.Kind {
	case KindPassword:
		if len(s.Password) == 0 {
			return nil, errors.Wrapf(ErrFactorMissing, "password")
		}
		return argon2.IDKey(s.Password, kdf.Salt, kdf.Time, kdf.Memory, kdf.Threads, 32), nil
	case KindKeyfile:
		if len(s.Keyfile) == 0 {
			return nil, errors.Wrapf(ErrFactorMissing, "keyfile")
		}
		return KeyfileSecret(s.Keyfile), nil
	case KindToken:
		responder := s.Token
		if responder == nil {
			responder = ExecTokens{}
		}
		resp, err := responder.Respond(f)
		if err != nil {
			return nil, err
		}
		if len(resp) == 0 {
			return nil, errors.Wrapf(ErrFactorMissing, "token")
		}
		return resp, nil
	}
	return nil, errors.Errorf("Unknown unlock factor %q", f.Kind)
}

// KeyfileSecret reads a keyfile the way a key manager does: a file that IS
// exactly a 32-byte key, or exactly 64 hex characters, is taken at its word;
// anything else — a photo, a poem, a downloaded binary — is hashed. Taking raw
// key files literally is what lets a keyfile be moved between tools without
// re-encrypting every vault.
func KeyfileSecret(b []byte) []byte {
	if len(b) == contentKeyLen {
		return append([]byte(nil), b...)
	}
	if len(b) == 2*contentKeyLen {
		if raw, err := hex.DecodeString(string(b)); err == nil {
			return raw
		}
	}
	sum := sha3.Sum256(b)
	return sum[:]
}

// compositeKey folds every factor into one pseudorandom key. Each factor is
// domain-separated by its kind and its position, so two factors can never swap
// places and still derive the same key, and a factor's raw secret can never be
// replayed as a different kind's.
func compositeKey(kdf KDF, factors []Factor, s Secrets) ([]byte, error) {
	if len(factors) == 0 {
		return nil, errors.New("A vault must declare at least one unlock factor")
	}
	ikm := make([]byte, 0, 32*len(factors))
	for i, f := range factors {
		raw, err := factorSecret(f, kdf, s)
		if err != nil {
			return nil, err
		}
		d := sha3.New256()
		d.Write([]byte("lflow/vault/v1/factor/"))
		d.Write([]byte{byte(i)})
		d.Write([]byte(f.Kind))
		d.Write([]byte{0})
		d.Write(raw)
		ikm = append(ikm, d.Sum(nil)...)
	}
	prk, err := hkdf.Extract(sha3.New256, ikm, kdf.Salt)
	if err != nil {
		return nil, errors.Wrap(err, "extracting the composite key")
	}
	return prk, nil
}
