package crypto

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
)

// A hardware key is the third factor: something in your hand, which — unlike a
// password or a keyfile — cannot be copied off the machine. lflow talks to it
// through the vendor CLI rather than linking libusb/libfido2, for the same
// reason the rest of the editor shells out: a cgo USB dependency would make the
// whole binary harder to build on every platform for a feature most outlines
// never touch. The tools are declared as node CLI deps (see the Encrypted
// node's cliDeps), so a missing one is reported before the prompt, not after.
//
// The protocol is HMAC-SHA1 challenge-response, the mode a YubiKey's slot 1/2
// can be configured for (`ykman otp chalresp --touch 2`). The vault stores the
// challenge; the device holds the HMAC key and never reveals it. Read the
// device out of a stolen laptop and you still cannot open the vault.
//
// WARNING (invariant): the challenge is passed to every backend as the SAME
// lowercase hex TEXT — never as raw bytes to one tool and hex to another. Both
// CLIs HMAC the literal argument they are given, so a backend that decoded the
// hex first would compute a different response and lock the vault against the
// very key that sealed it.

// TokenResponder answers a vault's challenge with a hardware key.
type TokenResponder interface {
	Respond(f Factor) ([]byte, error)
}

// tokenBackend is one vendor CLI able to run challenge-response.
type tokenBackend struct {
	name string
	bin  string
	args func(slot int, challenge string) []string
}

// tokenBackends in probe order. ykchalresp (yubikey-personalization) is the
// older, narrower tool and is tried first because it does exactly this one
// thing; ykman is the modern suite and covers the same slots.
var tokenBackends = []tokenBackend{
	{
		name: "ykchalresp",
		bin:  "ykchalresp",
		args: func(slot int, challenge string) []string {
			return []string{"-" + strconv.Itoa(slot), challenge}
		},
	},
	{
		name: "ykman",
		bin:  "ykman",
		args: func(slot int, challenge string) []string {
			return []string{"otp", "calculate", strconv.Itoa(slot), challenge}
		},
	},
}

// TokenBins is every binary a token factor may shell out to — the Encrypted
// node's cliDeps list, so /type can grey the type and alt+e can say which tool
// is missing instead of failing at the prompt.
func TokenBins() []string {
	out := make([]string, 0, len(tokenBackends))
	for _, b := range tokenBackends {
		out = append(out, b.bin)
	}
	return out
}

// TokenAvailable reports whether any backend CLI is installed, so a create
// prompt can offer the token factor only when it could actually be answered.
func TokenAvailable() bool { return backendFor("") != nil }

// tokenTimeout bounds a single challenge. A touch-required slot waits for the
// user's finger, so this is generous — but it is bounded, because a device that
// never answers must not wedge the editor's key prompt forever.
const tokenTimeout = 30 * time.Second

// ExecTokens is the real backend: it runs the vendor CLI.
type ExecTokens struct{}

// Respond puts the vault's challenge to the hardware key and returns the raw
// HMAC-SHA1 response.
func (ExecTokens) Respond(f Factor) ([]byte, error) {
	if len(f.Challenge) == 0 {
		return nil, errors.New("The token factor has no stored challenge")
	}
	b := backendFor(f.Device)
	if b == nil {
		if f.Device != "" {
			return nil, errors.Errorf("Missing dependency: %s — this vault's token factor was sealed with it", f.Device)
		}
		return nil, errors.Errorf("Missing dependency: install %s to use a hardware key", strings.Join(TokenBins(), " or "))
	}
	slot := f.Slot
	if slot == 0 {
		slot = 2 // slot 2 is the one a YubiKey ships free for challenge-response
	}
	ctx, cancel := context.WithTimeout(context.Background(), tokenTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, b.bin, b.args(slot, hex.EncodeToString(f.Challenge))...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, errors.Errorf("The hardware key did not answer in %s — touch it when it blinks", tokenTimeout)
		}
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return nil, errors.Wrapf(err, "%s: %s", b.name, firstLine(msg))
		}
		return nil, errors.Wrapf(err, "running %s", b.name)
	}
	return parseTokenResponse(out.String())
}

// backendFor picks the named backend, or the first installed one when the
// envelope did not record a device (an older seal, or a vault made on a machine
// with a different tool installed).
func backendFor(device string) *tokenBackend {
	for i := range tokenBackends {
		b := &tokenBackends[i]
		if device != "" && b.name != device {
			continue
		}
		if _, err := exec.LookPath(b.bin); err == nil {
			return b
		}
	}
	return nil
}

// parseTokenResponse reads the hex response both CLIs print. ykman prefixes
// nothing and ykchalresp prints the bare digest, but either may add trailing
// noise on a verbose build, so the first hex-looking line wins.
func parseTokenResponse(s string) ([]byte, error) {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 32 || len(line)%2 != 0 {
			continue
		}
		raw, err := hex.DecodeString(line)
		if err != nil {
			continue
		}
		return raw, nil
	}
	return nil, errors.New("The hardware key returned no usable response")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// NewTokenFactor mints a token factor: a fresh 32-byte challenge this vault
// will put to the key forever after, bound to the backend that answered it.
func NewTokenFactor(slot int) (Factor, error) {
	challenge := make([]byte, 32)
	if _, err := rand.Read(challenge); err != nil {
		return Factor{}, errors.Wrap(err, "drawing a token challenge")
	}
	device := ""
	if b := backendFor(""); b != nil {
		device = b.name
	}
	if slot == 0 {
		slot = 2
	}
	return Factor{Kind: KindToken, Device: device, Slot: slot, Challenge: challenge}, nil
}
