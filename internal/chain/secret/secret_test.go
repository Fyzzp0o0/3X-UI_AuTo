package secret

import (
	"errors"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/crypto/nodetoken"
)

func TestSealRefusesWithoutKey(t *testing.T) {
	off, err := nodetoken.NewCodec(nodetoken.ModeOff, nil)
	if err != nil {
		t.Fatal(err)
	}
	nodetoken.Init(off)

	if _, err := Seal(1, "p1"); !errors.Is(err, ErrEncryptionRequired) {
		t.Fatalf("Seal without a key = %v, want ErrEncryptionRequired", err)
	}
}

func TestOpenPassesLegacyPlaintext(t *testing.T) {
	off, err := nodetoken.NewCodec(nodetoken.ModeOff, nil)
	if err != nil {
		t.Fatal(err)
	}
	nodetoken.Init(off)

	got, err := Open(7, "legacy-plaintext")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got != "legacy-plaintext" {
		t.Fatalf("Open = %q", got)
	}
}

func TestSealBindsCiphertextToRow(t *testing.T) {
	ring := &nodetoken.Keyring{ActiveID: "k1", Keys: map[string][32]byte{"k1": {0x01}}}
	codec, err := nodetoken.NewCodec(nodetoken.ModeRequired, ring)
	if err != nil {
		t.Fatal(err)
	}
	nodetoken.Init(codec)
	t.Cleanup(func() {
		off, offErr := nodetoken.NewCodec(nodetoken.ModeOff, nil)
		if offErr != nil {
			t.Fatal(offErr)
		}
		nodetoken.Init(off)
	})

	sealed, err := Seal(1, "p1")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if !strings.HasPrefix(sealed, "enc:v1:") {
		t.Fatalf("ciphertext = %q", sealed)
	}
	got, err := Open(1, sealed)
	if err != nil || got != "p1" {
		t.Fatalf("Open(1) = %q, %v", got, err)
	}
	// Moving the ciphertext to another row must fail: the AAD carries the id.
	if _, err := Open(2, sealed); err == nil {
		t.Fatal("ciphertext was accepted under a different row id")
	}
}
