// Package crypto is lflow's at-rest encryption: the sealed envelope an
// Encrypted node keeps its subtree inside, and the composite key — password,
// keyfile, hardware token — that opens it.
//
// # The one invariant
//
// Plaintext never reaches the nodes table. An Encrypted node's own row carries
// only garble (see Envelope.Garble); everything it holds — its real title, its
// note, its whole subtree — lives in the sealed blob and exists in the clear
// only in memory, only while a session key is held. That is also why the
// ordinary Query node cannot see into a vault and the Encrypted Query node has
// to open one first: there is genuinely nothing indexed to find.
//
// # The suite
//
//	SUITE-1 = Argon2id + HKDF-SHA3-256 + ML-KEM-1024 + AES-256-GCM
//
// Post-quantum, honestly stated. A vault's confidentiality rests on AES-256-GCM
// under a key derived by Argon2id and HKDF-SHA3-256. None of those has a
// Shor-shaped weakness — there is no factoring or discrete log anywhere in the
// path — and Grover only halves a symmetric key, so 256 bits stays at a
// ~128-bit quantum security level. That, not the KEM, is what makes a vault
// safe against a quantum adversary today.
//
// The ML-KEM-1024 (FIPS 203) layer sits on top: the composite key material
// seeds a decapsulation key, seal encapsulates to it, and the shared secret is
// mixed into the wrapping key. Sealing and opening therefore already run a real
// KEM round trip, and the envelope already carries a KEM ciphertext field. It
// adds no strength against today's attacker — the keypair comes from the same
// secret the wrap already depends on — and it is not claimed to. It exists so
// that a second recipient kind (a shared vault sealed to somebody's public
// encapsulation key, a printed recovery key) is a new factor, not a format
// break and a migration of everyone's data.
//
// # What a factor is
//
// Factors are ANDed, KeePass-style: a vault declares the factors it was sealed
// with, and every one of them must be supplied again to open it. Each factor
// contributes a 32-byte digest to the HKDF input keying material in the order
// the header lists, so dropping, reordering or substituting one changes the
// derived key and the unwrap fails closed.
package crypto

// Suite names the algorithm set an envelope was sealed with. It is written into
// every header so a future suite can be told apart from this one by inspection
// rather than by version arithmetic.
const Suite = "ARGON2ID-HKDF-SHA3-256-MLKEM1024-AES256GCM"

// Magic and Version identify the envelope format itself.
const (
	Magic   = "lflow-vault"
	Version = 1
)

// BlobMime is the node_blobs mime an encrypted node's envelope is stored under.
const BlobMime = "application/x-lflow-vault"

// Sizes the format fixes.
const (
	saltSize      = 16 // Argon2id salt
	contentKeyLen = 32 // AES-256
	kemSeedLen    = 64 // ML-KEM seed (d ‖ z)
)

// HKDF info strings. Each derived value gets its own label so no two purposes
// can ever share bytes.
const (
	infoKEMSeed = "lflow/vault/v1/mlkem-seed"
	infoKEKBase = "lflow/vault/v1/kek-base"
	infoWrapKey = "lflow/vault/v1/wrap-key"
	infoGarble  = "lflow/vault/v1/garble"
)
