package main

// Who a device is, on a network where nothing else can be trusted.
//
// The rule this file exists to enforce is that a device is its key, not its
// address. An IP address is where something is standing this afternoon; a phone
// that paired at home and comes back on a café's wifi is the same phone, and
// the machine that answers on the address it used to have is not necessarily
// the desktop. So every device generates one P-256 key on first use, keeps the
// private half forever, and is named by the public half.
//
// The private key is reached only through crypto.Signer, never as bytes, so
// that the phone can hold it in Android Keystore, where it is not extractable
// even in principle, and the desktop can hold it in a file. Everything above
// this line works the same either way.

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base32"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
)

// identity is this device, as far as any other device is concerned.
type identity struct {
	// id is what a person sees and what a peer record is filed under. It is
	// derived from the public key, so it cannot be claimed by anyone else.
	id string
	// name is what the other end shows in its list of paired devices.
	name   string
	signer crypto.Signer
}

// deviceID names a public key.
//
// Sixteen base32 characters of a SHA-256 over the key's SPKI encoding. Long
// enough that finding a second key with the same name is not a thing anyone can
// do, short enough to read out loud, and base32 rather than hex because it is
// the alphabet people mistype least.
func deviceID(public crypto.PublicKey) (string, error) {
	encoded, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:10]), nil
}

// loadIdentity returns the device's identity, generating the key the first time.
//
// The key file is written before it is used, and used only after it is written:
// a key that exists in memory and not on disk would pair a phone to a desktop
// that forgets it on the next restart, which reads as pairing that silently
// stops working.
func loadIdentity(dir, name string) (*identity, error) {
	if signer, ok := bridgedSigner(); ok {
		// A phone. The key is in Android Keystore and this process can only ask
		// it to sign. See sync_bridge.go.
		return newIdentity(signer, name)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "identity.pem")
	switch data, err := os.ReadFile(path); {
	case err == nil:
		key, err := parseIdentityKey(data)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		return newIdentity(key, name)
	case !os.IsNotExist(err):
		return nil, err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	// 0600 and written whole. This is the one file in the library whose loss
	// costs the user every pairing they have made.
	if err := writeFileAtomically(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}), 0o600); err != nil {
		return nil, err
	}
	return newIdentity(key, name)
}

func newIdentity(signer crypto.Signer, name string) (*identity, error) {
	public := signer.Public()
	if public == nil {
		// A bridged signer reports why here and nowhere else: crypto.Signer has
		// no way to return an error from Public(), so without this the failure
		// surfaces as "unsupported public key type: <nil>", which describes the
		// symptom and names nothing that would help fix it.
		if bridge, ok := signer.(*bridgeSigner); ok {
			if reason := bridge.publicKeyError(); reason != nil {
				return nil, reason
			}
		}
		return nil, fmt.Errorf("this device has no usable signing key")
	}
	id, err := deviceID(public)
	if err != nil {
		return nil, err
	}
	return &identity{id: id, name: name, signer: signer}, nil
}

func parseIdentityKey(data []byte) (crypto.Signer, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("key of type %T cannot sign", key)
	}
	return signer, nil
}
