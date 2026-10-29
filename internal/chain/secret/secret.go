// Package secret seals upstream proxy credentials with row-bound AES-GCM.
// Reads accept legacy plaintext; writes refuse to run without a key.
package secret

import (
	"errors"
	"fmt"

	"github.com/mhsanaei/3x-ui/v3/internal/crypto/nodetoken"
)

// ErrEncryptionRequired is returned when new credentials would be stored in
// plaintext. Import must fail rather than silently downgrade.
var ErrEncryptionRequired = errors.New("chain: credential encryption is not configured; set NODE_TOKEN_ENCRYPTION=required and a key")

func bound(id int) []byte { return []byte(fmt.Sprintf("chain/proxy/%d/password", id)) }

// Enabled reports whether new credentials can be sealed at rest.
func Enabled() bool { return nodetoken.Enabled() }

// Seal encrypts a proxy password. The row id is part of the AAD, so copying a
// ciphertext to another row makes it undecryptable.
func Seal(id int, plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	if !nodetoken.Enabled() {
		return "", ErrEncryptionRequired
	}
	return nodetoken.EncryptBound(bound(id), plain)
}

// Rekey re-encrypts a stored password under the active key. changed is false
// when the row already uses it (or is empty); legacy plaintext is migrated.
func Rekey(id int, stored string) (string, bool, error) {
	if stored == "" {
		return stored, false, nil
	}
	codec := nodetoken.Active()
	if !codec.Enabled() {
		return "", false, ErrEncryptionRequired
	}
	if codec.EncryptedWithActive(stored) {
		return stored, false, nil
	}
	plain, err := codec.DecryptBound(bound(id), stored)
	if err != nil {
		return "", false, err
	}
	sealed, err := codec.EncryptBound(bound(id), plain)
	if err != nil {
		return "", false, err
	}
	return sealed, true, nil
}

// Open decrypts a stored password, passing legacy plaintext through unchanged.
func Open(id int, stored string) (string, error) {
	if stored == "" {
		return "", nil
	}
	return nodetoken.DecryptBound(bound(id), stored)
}
