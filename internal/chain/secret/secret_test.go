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

func TestRekeyMovesCiphertextToActiveKey(t *testing.T) {
	k1 := &nodetoken.Keyring{ActiveID: "k1", Keys: map[string][32]byte{"k1": {1}}}
	c1, err := nodetoken.NewCodec(nodetoken.ModeRequired, k1)
	if err != nil {
		t.Fatal(err)
	}
	nodetoken.Init(c1)
	sealed, err := Seal(5, "pw")
	if err != nil {
		t.Fatal(err)
	}

	k2 := &nodetoken.Keyring{ActiveID: "k2", Keys: map[string][32]byte{"k1": {1}, "k2": {2}}}
	c2, err := nodetoken.NewCodec(nodetoken.ModeRequired, k2)
	if err != nil {
		t.Fatal(err)
	}
	nodetoken.Init(c2)
	rotated, changed, err := Rekey(5, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || rotated == sealed {
		t.Fatalf("rekey did not rotate: changed=%v same=%v", changed, rotated == sealed)
	}
	if _, again, err := Rekey(5, rotated); err != nil || again {
		t.Fatalf("second rekey = changed %v, err %v", again, err)
	}

	// The old key is dropped: only the rotated ciphertext may still decrypt.
	c3, err := nodetoken.NewCodec(nodetoken.ModeRequired, &nodetoken.Keyring{
		ActiveID: "k2", Keys: map[string][32]byte{"k2": {2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	nodetoken.Init(c3)
	t.Cleanup(func() {
		off, _ := nodetoken.NewCodec(nodetoken.ModeOff, nil)
		nodetoken.Init(off)
	})
	if _, err := Open(5, sealed); err == nil {
		t.Fatal("old ciphertext decrypted after the old key was dropped")
	}
	plain, err := Open(5, rotated)
	if err != nil || plain != "pw" {
		t.Fatalf("rotated ciphertext = %q, %v", plain, err)
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
