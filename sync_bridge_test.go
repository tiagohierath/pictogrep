package main

// bridgeSigner talks to a key service over a loopback socket rather than
// touching key material directly, because on the phone that service is
// Kotlin, backed by Android Keystore, and this repo has no way to run that
// code to test against. This file is the substitute: a fake key service in
// Go, implementing exactly the protocol sync_bridge.go speaks, built from an
// ordinary in-memory key so the whole round trip (ask for the public key,
// sign a digest, verify the signature against that public key) can be proven
// correct without a phone. Kotlin's real service has to answer identically
// to this one wire-for-wire.

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"net"
	"strings"
	"testing"
)

// fakeKeyService is the test's stand-in for the Kotlin shell: an EC key it
// never hands out, reachable only by asking it to sign.
func fakeKeyService(t *testing.T) (address, token string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	token = "test-token"

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go serveOneRequest(conn, key, token)
		}
	}()
	return listener.Addr().String(), token
}

func serveOneRequest(conn net.Conn, key *ecdsa.PrivateKey, token string) {
	defer conn.Close()
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return
	}
	line := strings.TrimSpace(string(buf[:n]))
	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 2 || parts[0] != token {
		_, _ = conn.Write([]byte("error bad token\n"))
		return
	}
	switch parts[1] {
	case "public":
		der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
		if err != nil {
			_, _ = conn.Write([]byte("error " + err.Error() + "\n"))
			return
		}
		_, _ = conn.Write([]byte(base64.StdEncoding.EncodeToString(der) + "\n"))
	case "sign":
		if len(parts) < 3 {
			_, _ = conn.Write([]byte("error missing digest\n"))
			return
		}
		digest, err := base64.StdEncoding.DecodeString(parts[2])
		if err != nil {
			_, _ = conn.Write([]byte("error bad digest\n"))
			return
		}
		signature, err := ecdsa.SignASN1(rand.Reader, key, digest)
		if err != nil {
			_, _ = conn.Write([]byte("error " + err.Error() + "\n"))
			return
		}
		_, _ = conn.Write([]byte(base64.StdEncoding.EncodeToString(signature) + "\n"))
	default:
		_, _ = conn.Write([]byte("error unknown request\n"))
	}
}

func TestBridgeSignerRoundTrips(t *testing.T) {
	address, token := fakeKeyService(t)
	signer := &bridgeSigner{address: address, token: token}

	public := signer.Public()
	if public == nil {
		t.Fatal("Public() returned nil")
	}
	ecdsaPublic, ok := public.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("Public() returned %T, want *ecdsa.PublicKey", public)
	}

	message := sha256.Sum256([]byte("a pairing session worth trusting"))
	signature, err := signer.Sign(nil, message[:], crypto.SHA256)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if !ecdsa.VerifyASN1(ecdsaPublic, message[:], signature) {
		t.Fatal("the signature the bridge returned does not verify against the public key it reported")
	}
}

func TestBridgeSignerRejectsTheWrongHash(t *testing.T) {
	address, token := fakeKeyService(t)
	signer := &bridgeSigner{address: address, token: token}
	if _, err := signer.Sign(nil, make([]byte, 32), crypto.SHA512); err == nil {
		t.Fatal("signing with the wrong declared hash function should fail rather than silently sign anyway")
	}
}

func TestBridgeSignerFailsClosedWithNoService(t *testing.T) {
	signer := &bridgeSigner{address: "127.0.0.1:1", token: "x"}
	if public := signer.Public(); public != nil {
		t.Fatalf("Public() with no service reachable should be nil, got %v", public)
	}
	if _, err := signer.Sign(nil, make([]byte, 32), crypto.SHA256); err == nil {
		t.Fatal("Sign() with no service reachable should fail")
	}
}
