package crypto

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pkg/errors"
)

// fastKDF shrinks Argon2id to a test-sized cost. Every test seals and opens at
// least once, and the interactive 64 MiB profile would make the package's test
// run take minutes for no extra coverage — the derivation path is identical.
func fastKDF(t *testing.T) {
	t.Helper()
	mem, iters := argonMemory, argonTime
	argonMemory, argonTime = 8, 1
	t.Cleanup(func() { argonMemory, argonTime = mem, iters })
}

func passwordFactors() []Factor { return []Factor{{Kind: KindPassword}} }

func TestSealOpenRoundTrip(t *testing.T) {
	fastKDF(t)
	msg := []byte(`{"title":"bank","children":[{"name":"iban"}]}`)
	env, vault, err := Seal(msg, passwordFactors(), Secrets{Password: []byte("correct horse")})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if vault == nil {
		t.Fatal("seal returned no open vault")
	}
	// the sealed bytes must not contain the plaintext anywhere
	blob, err := env.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(blob, []byte("bank")) || bytes.Contains(blob, []byte("iban")) {
		t.Error("the sealed envelope leaks plaintext")
	}

	reopened, err := Unmarshal(blob)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got, v2, err := reopened.Open(Secrets{Password: []byte("correct horse")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if string(got) != string(msg) {
		t.Errorf("round trip: got %q want %q", got, msg)
	}
	if v2 == nil {
		t.Fatal("open returned no vault to re-seal with")
	}
}

func TestOpenWrongPassword(t *testing.T) {
	fastKDF(t)
	env, _, err := Seal([]byte("secret"), passwordFactors(), Secrets{Password: []byte("right")})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, _, err := env.Open(Secrets{Password: []byte("wrong")}); !errors.Is(err, ErrWrongKey) {
		t.Errorf("wrong password: got %v want ErrWrongKey", err)
	}
}

func TestOpenMissingFactorIsNotWrongKey(t *testing.T) {
	fastKDF(t)
	factors := []Factor{{Kind: KindPassword}, {Kind: KindKeyfile, Hint: "id_vault"}}
	env, _, err := Seal([]byte("secret"), factors, Secrets{Password: []byte("pw"), Keyfile: []byte("file bytes")})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	// a prompt that forgot to collect the keyfile must be told to ask again,
	// not told the password was wrong
	_, _, err = env.Open(Secrets{Password: []byte("pw")})
	if !errors.Is(err, ErrFactorMissing) {
		t.Errorf("missing keyfile: got %v want ErrFactorMissing", err)
	}
	if errors.Is(err, ErrWrongKey) {
		t.Error("a missing factor must not read as a wrong key")
	}
}

func TestEveryFactorIsRequired(t *testing.T) {
	fastKDF(t)
	factors := []Factor{{Kind: KindPassword}, {Kind: KindKeyfile}}
	full := Secrets{Password: []byte("pw"), Keyfile: []byte("the keyfile")}
	env, _, err := Seal([]byte("secret"), factors, full)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, _, err := env.Open(Secrets{Password: []byte("pw"), Keyfile: []byte("a different keyfile")}); !errors.Is(err, ErrWrongKey) {
		t.Errorf("wrong keyfile: got %v want ErrWrongKey", err)
	}
	if _, _, err := env.Open(full); err != nil {
		t.Errorf("both factors right: %v", err)
	}
}

func TestTokenFactorUsesTheDeviceResponse(t *testing.T) {
	fastKDF(t)
	f, err := NewTokenFactor(2)
	if err != nil {
		t.Fatalf("new token factor: %v", err)
	}
	if len(f.Challenge) == 0 {
		t.Fatal("a token factor must carry a stored challenge")
	}
	key := fakeToken{secret: "device-hmac-key"}
	env, _, err := Seal([]byte("secret"), []Factor{{Kind: KindPassword}, f},
		Secrets{Password: []byte("pw"), Token: key})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, _, err := env.Open(Secrets{Password: []byte("pw"), Token: key}); err != nil {
		t.Errorf("same key: %v", err)
	}
	other := fakeToken{secret: "somebody else's yubikey"}
	if _, _, err := env.Open(Secrets{Password: []byte("pw"), Token: other}); !errors.Is(err, ErrWrongKey) {
		t.Errorf("another device: got %v want ErrWrongKey", err)
	}
}

func TestHeaderIsAuthenticated(t *testing.T) {
	fastKDF(t)
	env, _, err := Seal([]byte("secret"), passwordFactors(), Secrets{Password: []byte("pw")})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	blob, _ := env.Marshal()

	// strip the password factor from the header: an attacker asking the vault to
	// open with no factors at all must not get a plaintext, and must not get a
	// silently different failure either.
	var w wireEnvelope
	if err := json.Unmarshal(blob, &w); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var h Header
	_ = json.Unmarshal(w.Header, &h)
	h.KDF.Time = 9 // a downgrade/upgrade an attacker could try
	w.Header, _ = json.Marshal(h)
	tampered, _ := json.Marshal(w)

	e2, err := Unmarshal(tampered)
	if err != nil {
		t.Fatalf("unmarshal tampered: %v", err)
	}
	if _, _, err := e2.Open(Secrets{Password: []byte("pw")}); !errors.Is(err, ErrWrongKey) {
		t.Errorf("tampered header: got %v want ErrWrongKey", err)
	}
}

func TestResealKeepsTheGarbleAndTheKey(t *testing.T) {
	fastKDF(t)
	env, vault, err := Seal([]byte("v1"), passwordFactors(), Secrets{Password: []byte("pw")})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	again, err := vault.Seal([]byte("v2 — longer, edited content"))
	if err != nil {
		t.Fatalf("reseal: %v", err)
	}
	if env.Garble() != again.Garble() {
		t.Error("re-sealing changed the garble: the row would churn on every save")
	}
	if bytes.Equal(env.nonce, again.nonce) {
		t.Error("re-seal reused the nonce")
	}
	got, _, err := again.Open(Secrets{Password: []byte("pw")})
	if err != nil {
		t.Fatalf("open resealed: %v", err)
	}
	if string(got) != "v2 — longer, edited content" {
		t.Errorf("resealed content: %q", got)
	}
}

func TestGarbleIsFixedLengthAndStable(t *testing.T) {
	fastKDF(t)
	short, _, err := Seal([]byte("x"), passwordFactors(), Secrets{Password: []byte("pw")})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	long, _, err := Seal(bytes.Repeat([]byte("x"), 50_000), passwordFactors(), Secrets{Password: []byte("pw")})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if len(short.Garble()) != len(long.Garble()) {
		t.Error("the garble leaks the plaintext length")
	}
	if short.Garble() == long.Garble() {
		t.Error("two different vaults share a garble")
	}
	if !IsGarble(short.Garble()) {
		t.Errorf("IsGarble rejected its own output: %q", short.Garble())
	}
	if IsGarble("bank passwords") {
		t.Error("IsGarble accepted real text")
	}
}

func TestUnmarshalRejectsForeignBlobs(t *testing.T) {
	if _, err := Unmarshal([]byte("not json at all")); err == nil {
		t.Error("parsed a non-json blob")
	}
	if _, err := Unmarshal([]byte(`{"header":{"magic":"something-else"}}`)); err == nil ||
		!strings.Contains(err.Error(), "Not an lflow vault") {
		t.Errorf("foreign magic: %v", err)
	}
}

func TestDuplicateFactorsRejected(t *testing.T) {
	fastKDF(t)
	_, _, err := Seal([]byte("x"), []Factor{{Kind: KindPassword}, {Kind: KindPassword}}, Secrets{Password: []byte("pw")})
	if err == nil || !strings.Contains(err.Error(), "Duplicate") {
		t.Errorf("duplicate factors: %v", err)
	}
}

func TestKeyfileSecretTakesRawKeysLiterally(t *testing.T) {
	raw := bytes.Repeat([]byte{7}, 32)
	if got := KeyfileSecret(raw); !bytes.Equal(got, raw) {
		t.Error("a 32-byte keyfile was hashed instead of used as the key")
	}
	hexed := []byte("0707070707070707070707070707070707070707070707070707070707070707")
	if got := KeyfileSecret(hexed); !bytes.Equal(got, raw) {
		t.Error("a hex keyfile did not decode to the same key as its raw form")
	}
	if got := KeyfileSecret([]byte("a photo of my cat")); len(got) != 32 {
		t.Errorf("an arbitrary keyfile hashed to %d bytes", len(got))
	}
}

// fakeToken stands in for a hardware key: a deterministic response derived from
// a per-device secret, which is exactly the property the real HMAC slot has.
type fakeToken struct{ secret string }

func (f fakeToken) Respond(fac Factor) ([]byte, error) {
	return KeyfileSecret(append([]byte(f.secret), fac.Challenge...)), nil
}
