package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/mlkem"
	"crypto/rand"
	"crypto/sha3"
	"encoding/json"

	"github.com/pkg/errors"
)

// Header is everything needed to re-derive a vault's wrapping key, and nothing
// that helps derive it without the factors. It is the AEAD's additional data:
// both the wrapped content key and the content itself are authenticated under
// it, so an attacker cannot strip a factor, swap the KEM ciphertext or downgrade
// the Argon2id cost and have the result still open.
type Header struct {
	Magic   string   `json:"magic"`
	Version int      `json:"version"`
	Suite   string   `json:"suite"`
	KDF     KDF      `json:"kdf"`
	Factors []Factor `json:"factors"`
	KEM     KEM      `json:"kem"`
}

// KEM is the ML-KEM-1024 encapsulation this envelope carries.
type KEM struct {
	Name       string `json:"name"` // "ML-KEM-1024"
	Ciphertext []byte `json:"ct"`
}

// Envelope is a sealed vault. The header is kept as the exact bytes it was
// written with, not re-marshalled from the parsed struct: it is the AEAD's
// additional data, so a future field this binary does not know about must still
// authenticate byte for byte rather than silently vanish and fail the tag.
type Envelope struct {
	header Header
	aad    []byte

	wrapNonce []byte
	wrapped   []byte // AES-256-GCM(KEK, contentKey)
	nonce     []byte
	box       []byte // AES-256-GCM(contentKey, plaintext)
}

// Header returns the parsed header — the factors a prompt has to ask for.
func (e *Envelope) Header() Header { return e.header }

// Factors are the unlock inputs this vault requires, in prompt order.
func (e *Envelope) Factors() []Factor { return e.header.Factors }

type wireEnvelope struct {
	Header    json.RawMessage `json:"header"`
	WrapNonce []byte          `json:"wrap_nonce"`
	Wrapped   []byte          `json:"wrapped"`
	Nonce     []byte          `json:"nonce"`
	Box       []byte          `json:"box"`
}

// Marshal renders the envelope as the bytes stored in the node's blob.
func (e *Envelope) Marshal() ([]byte, error) {
	b, err := json.Marshal(wireEnvelope{
		Header: json.RawMessage(e.aad), WrapNonce: e.wrapNonce,
		Wrapped: e.wrapped, Nonce: e.nonce, Box: e.box,
	})
	return b, errors.Wrap(err, "marshalling a vault envelope")
}

// Unmarshal parses a stored envelope. It validates only the frame — whether the
// key is right is decided by the AEAD tag in Open, never by a comparison here.
func Unmarshal(b []byte) (*Envelope, error) {
	var w wireEnvelope
	if err := json.Unmarshal(b, &w); err != nil {
		return nil, errors.Wrap(err, "parsing a vault envelope")
	}
	e := &Envelope{aad: []byte(w.Header), wrapNonce: w.WrapNonce, wrapped: w.Wrapped, nonce: w.Nonce, box: w.Box}
	if err := json.Unmarshal(w.Header, &e.header); err != nil {
		return nil, errors.Wrap(err, "parsing a vault header")
	}
	if e.header.Magic != Magic {
		return nil, errors.Errorf("Not an lflow vault (magic %q)", e.header.Magic)
	}
	if e.header.Version > Version {
		return nil, errors.Errorf("This vault was sealed by a newer lflow (format v%d, this build reads v%d)", e.header.Version, Version)
	}
	if e.header.Suite != Suite {
		return nil, errors.Errorf("Unknown cipher suite %q", e.header.Suite)
	}
	if len(e.header.Factors) == 0 {
		return nil, errors.New("A vault envelope declares no unlock factors")
	}
	return e, nil
}

// Vault is an OPEN envelope: the header it was sealed under plus the content
// key, held for as long as the session keeps it unlocked. It is what lets a
// later save re-seal the edited subtree without prompting again — the factors
// were already proven, and running Argon2id on every keystroke's flush would
// make the editor unusable.
//
// A Vault is secret. It must never be persisted, logged, or synced.
type Vault struct {
	header    Header
	aad       []byte
	wrapNonce []byte
	wrapped   []byte

	contentKey []byte
}

// Header returns the header the vault is sealed under.
func (v *Vault) Header() Header { return v.header }

// Seal encrypts plaintext under a fresh vault locked by the given factors.
// Every factor must be answerable from s, or it returns ErrFactorMissing.
func Seal(plaintext []byte, factors []Factor, s Secrets) (*Envelope, *Vault, error) {
	kdf, err := newKDF()
	if err != nil {
		return nil, nil, err
	}
	if err := checkFactors(factors); err != nil {
		return nil, nil, err
	}
	prk, err := compositeKey(kdf, factors, s)
	if err != nil {
		return nil, nil, err
	}
	dk, err := kemKey(prk)
	if err != nil {
		return nil, nil, err
	}
	shared, kemCT := dk.EncapsulationKey().Encapsulate()

	header := Header{Magic: Magic, Version: Version, Suite: Suite, KDF: kdf,
		Factors: factors, KEM: KEM{Name: "ML-KEM-1024", Ciphertext: kemCT}}
	aad, err := json.Marshal(header)
	if err != nil {
		return nil, nil, errors.Wrap(err, "marshalling a vault header")
	}

	kek, err := wrapKey(prk, shared, kdf.Salt)
	if err != nil {
		return nil, nil, err
	}
	contentKey := make([]byte, contentKeyLen)
	if _, err := rand.Read(contentKey); err != nil {
		return nil, nil, errors.Wrap(err, "drawing a content key")
	}
	wrapNonce, wrapped, err := gcmSeal(kek, contentKey, aad)
	if err != nil {
		return nil, nil, err
	}

	v := &Vault{header: header, aad: aad, wrapNonce: wrapNonce, wrapped: wrapped, contentKey: contentKey}
	env, err := v.Seal(plaintext)
	return env, v, err
}

// Seal re-encrypts content under an already-open vault: same factors, same
// wrapped content key, a fresh nonce for the new plaintext. This is the save
// path — no prompt, no Argon2id, no key rotation.
func (v *Vault) Seal(plaintext []byte) (*Envelope, error) {
	nonce, box, err := gcmSeal(v.contentKey, plaintext, v.aad)
	if err != nil {
		return nil, err
	}
	return &Envelope{header: v.header, aad: v.aad, wrapNonce: v.wrapNonce,
		wrapped: v.wrapped, nonce: nonce, box: box}, nil
}

// Open unwraps the envelope with the supplied secrets and returns both the
// plaintext and the open vault to re-seal it with. A wrong password, a wrong
// keyfile or a tampered envelope all come back as ErrWrongKey: the AEAD tag
// cannot tell them apart, and pretending otherwise would be an oracle.
func (e *Envelope) Open(s Secrets) ([]byte, *Vault, error) {
	prk, err := compositeKey(e.header.KDF, e.header.Factors, s)
	if err != nil {
		return nil, nil, err
	}
	dk, err := kemKey(prk)
	if err != nil {
		return nil, nil, err
	}
	shared, err := dk.Decapsulate(e.header.KEM.Ciphertext)
	if err != nil {
		return nil, nil, errors.Wrap(err, "decapsulating the vault key")
	}
	kek, err := wrapKey(prk, shared, e.header.KDF.Salt)
	if err != nil {
		return nil, nil, err
	}
	contentKey, err := gcmOpen(kek, e.wrapNonce, e.wrapped, e.aad)
	if err != nil {
		return nil, nil, ErrWrongKey
	}
	plaintext, err := gcmOpen(contentKey, e.nonce, e.box, e.aad)
	if err != nil {
		return nil, nil, ErrWrongKey
	}
	v := &Vault{header: e.header, aad: e.aad, wrapNonce: e.wrapNonce, wrapped: e.wrapped, contentKey: contentKey}
	return plaintext, v, nil
}

// ErrWrongKey is the single answer to every failed unwrap.
var ErrWrongKey = errors.New("Wrong key — the vault did not open")

// checkFactors rejects a factor list a prompt could not sensibly serve: an
// empty one, or one asking for the same kind twice (Secrets holds one of each,
// and two passwords is a worse user experience than one longer one).
func checkFactors(factors []Factor) error {
	if len(factors) == 0 {
		return errors.New("A vault must declare at least one unlock factor")
	}
	seen := map[Kind]bool{}
	for _, f := range factors {
		if seen[f.Kind] {
			return errors.Errorf("Duplicate unlock factor %q", f.Kind)
		}
		seen[f.Kind] = true
	}
	return nil
}

// kemKey derives this vault's ML-KEM-1024 decapsulation key from the composite
// factor material. It is deterministic on purpose: there is no private key to
// store anywhere, so the KEM adds a step to the derivation without adding a
// secret to lose. See the package doc for what that does and does not buy.
func kemKey(prk []byte) (*mlkem.DecapsulationKey1024, error) {
	seed, err := hkdf.Expand(sha3.New256, prk, infoKEMSeed, kemSeedLen)
	if err != nil {
		return nil, errors.Wrap(err, "deriving the kem seed")
	}
	dk, err := mlkem.NewDecapsulationKey1024(seed)
	return dk, errors.Wrap(err, "deriving the kem key")
}

// wrapKey mixes the composite key and the KEM shared secret into the AES key
// that wraps the content key. Both inputs are required, so the wrap is no
// weaker than the stronger of them.
func wrapKey(prk, shared, salt []byte) ([]byte, error) {
	base, err := hkdf.Expand(sha3.New256, prk, infoKEKBase, 32)
	if err != nil {
		return nil, errors.Wrap(err, "deriving the wrap base")
	}
	kek, err := hkdf.Key(sha3.New256, append(base, shared...), salt, infoWrapKey, 32)
	return kek, errors.Wrap(err, "deriving the wrap key")
}

func gcmSeal(key, plaintext, aad []byte) (nonce, box []byte, err error) {
	g, err := newGCM(key)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, errors.Wrap(err, "drawing a nonce")
	}
	return nonce, g.Seal(nil, nonce, plaintext, aad), nil
}

func gcmOpen(key, nonce, box, aad []byte) ([]byte, error) {
	g, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(nonce) != g.NonceSize() {
		return nil, errors.New("Malformed vault: wrong nonce length")
	}
	out, err := g.Open(nil, nonce, box, aad)
	return out, errors.Wrap(err, "opening the vault box")
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.Wrap(err, "creating the cipher")
	}
	g, err := cipher.NewGCM(block)
	return g, errors.Wrap(err, "creating the aead")
}
