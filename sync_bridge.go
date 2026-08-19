package main

// Signing with a key this process is not allowed to see.
//
// On Android the device key lives in the system keystore, where it may be held
// by hardware and cannot be exported even by the app that created it. That is
// worth having, and it is not reachable from Go: it is a Java API. So the
// Kotlin shell generates the key, listens on a loopback socket, and this file
// asks it for signatures. What crosses the socket is a digest and a signature,
// never key material.
//
// It is a crypto.Signer, which is the only shape the rest of the sync code
// knows, so a desktop with a key in a file and a phone with a key in hardware
// are the same thing from every other file's point of view. That also makes it
// testable without a phone: a test can be the shell.

import (
	"crypto"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// Where the shell is listening, and the word that proves we are the app it
// started rather than another app on the same phone, for which loopback is
// just as reachable.
const (
	signerAddressEnv = "PICTOGREP_SIGNER"
	signerTokenEnv   = "PICTOGREP_SIGNER_TOKEN"
)

// How long a signature may take. Generous, because a hardware key can involve
// the secure element, and bounded, because a shell that has stopped answering
// must not hang a TLS handshake forever.
const signerTimeout = 15 * time.Second

type bridgeSigner struct {
	address string
	token   string

	// One at a time. The protocol is a request and a reply on a fresh
	// connection, and a lock is cheaper to reason about than a pool for
	// something asked a few times per sync.
	mu     sync.Mutex
	public crypto.PublicKey
}

// bridgedSigner returns the shell's key service, if this process was started by
// a shell that has one.
func bridgedSigner() (crypto.Signer, bool) {
	address := os.Getenv(signerAddressEnv)
	if address == "" {
		return nil, false
	}
	return &bridgeSigner{address: address, token: os.Getenv(signerTokenEnv)}, true
}

func (b *bridgeSigner) Public() crypto.PublicKey {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.public != nil {
		return b.public
	}
	reply, err := b.ask("public")
	if err != nil {
		// crypto.Signer cannot report this, and a nil public key is refused by
		// everything that would use it, which is the failure we want: no
		// identity, so no pairing and no sync, rather than an identity that
		// cannot sign.
		return nil
	}
	der, err := base64.StdEncoding.DecodeString(reply)
	if err != nil {
		return nil
	}
	public, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil
	}
	b.public = public
	return public
}

func (b *bridgeSigner) Sign(_ io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	if opts != nil && opts.HashFunc() != crypto.SHA256 {
		// The shell signs a digest it is handed, with no hashing of its own, so
		// the hash has to be the one both ends agreed on.
		return nil, fmt.Errorf("the device key signs SHA-256 digests, not %s", opts.HashFunc())
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	reply, err := b.ask("sign " + base64.StdEncoding.EncodeToString(digest))
	if err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(reply)
}

// ask sends one line and reads one line back.
func (b *bridgeSigner) ask(request string) (string, error) {
	connection, err := net.DialTimeout("tcp", b.address, signerTimeout)
	if err != nil {
		return "", fmt.Errorf("the app's key service is not answering: %w", err)
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(signerTimeout)); err != nil {
		return "", err
	}
	if _, err := io.WriteString(connection, b.token+" "+request+"\n"); err != nil {
		return "", err
	}
	reply, err := io.ReadAll(io.LimitReader(connection, 8<<10))
	if err != nil {
		return "", err
	}
	answer := strings.TrimSpace(string(reply))
	if rest, found := strings.CutPrefix(answer, "error "); found {
		return "", errors.New(rest)
	}
	if answer == "" {
		return "", errors.New("the app's key service returned nothing")
	}
	return answer, nil
}
