package main

// The LAN server: one fixed port, a self-signed certificate that outlives the
// process, and a mux where only /pair is open to a stranger.
//
// Fixed rather than random, unlike the local browsing server: a firewall rule
// or a router's port forward has to be written against something that does not
// change on every launch, and "what port is Pictogrep on right now" is exactly
// the kind of question the diagnostics screen exists to make unnecessary.
//
// Nothing here starts unless the user has opened "Connect phone" at least
// once. A machine that has never paired a phone has no reason to hold a port
// open on the network at all.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// syncPort is the one Pictogrep LAN port. Chosen in the ephemeral-adjacent
// range unlikely to already mean something to a router or another app, and
// distinct from the local browsing server, which picks a random port on
// purpose because nothing outside the machine ever needs to find it.
const syncPort = 58217

type syncServer struct {
	// appServer is where a manifest and an upload end up: the library, the
	// dedup index and the folder-linking logic all live behind it already, so
	// sync reuses that path instead of keeping a second one in step with it.
	appServer *server
	app       *application

	identity *identity
	peers    *peerStore
	pairing  *pairingManager
	cert     tls.Certificate

	listener net.Listener
	// advertiser makes this machine findable again after its address changes.
	// Held here rather than started alongside the app because there is nothing
	// to announce until the port is open. See sync_mdns.go.
	advertiser mdnsAdvertiser
}

// newSyncServer loads or creates the device's identity and certificate, and
// the peer store, but does not open a socket. Failing to load either must not
// stop the app from opening a library: sync is additive, and a machine that
// cannot sign should still show pictures.
func newSyncServer(appServer *server, deviceName string) (*syncServer, error) {
	app := appServer.app
	dir := filepath.Join(app.dataDir, "sync")
	id, err := loadIdentity(dir, deviceName)
	if err != nil {
		return nil, fmt.Errorf("device identity: %w", err)
	}
	peers, err := openPeerStore(dir)
	if err != nil {
		return nil, fmt.Errorf("paired devices: %w", err)
	}
	cert, err := loadOrCreateCertificate(dir, id)
	if err != nil {
		return nil, fmt.Errorf("sync certificate: %w", err)
	}
	return &syncServer{
		appServer: appServer, app: app,
		identity: id, peers: peers, pairing: newPairingManager(), cert: cert,
	}, nil
}

func (s *syncServer) fingerprint() string { return certFingerprint(&s.cert) }

// start opens the port. Idempotent, so "Connect phone" can be pressed on a
// server that is already listening without tearing anything down.
func (s *syncServer) start() error {
	if s.listener != nil {
		return nil
	}
	config := &tls.Config{
		Certificates: []tls.Certificate{s.cert},
		// The desktop asks the phone for its certificate too, and decides
		// whether to trust it itself, against the pinned fingerprint recorded
		// at pairing rather than against any certificate authority: there isn't
		// one, by design.
		ClientAuth: tls.RequestClientCert,
	}
	listener, err := tls.Listen("tcp", fmt.Sprintf(":%d", syncPort), config)
	if err != nil {
		return err
	}
	s.listener = listener
	mux := http.NewServeMux()
	// The only door open to someone who has never paired.
	mux.HandleFunc("POST /pair", s.handlePair)
	// Everything past this line checks the caller against the peer store.
	mux.Handle("GET /manifest", s.authenticated(http.HandlerFunc(s.handleManifest)))
	mux.Handle("POST /blobs/{hash}", s.authenticated(http.HandlerFunc(s.handleUploadBlob)))
	go func() { _ = http.Serve(listener, mux) }()
	// Announced only once the listener is up, so nothing is ever told to knock
	// on a door that is not there yet. A failure here is logged and swallowed:
	// what matters to a caller of start() is that the port is open, and every
	// paired device can still reach it by its stored address or a fresh QR.
	if err := s.advertiser.start(s.identity.id, s.identity.name, syncPort); err != nil {
		logMDNSUnavailable(err)
	}
	return nil
}

func (s *syncServer) stop() error {
	if s.listener == nil {
		return nil
	}
	// Withdrawn before the port closes, so the window where this device is
	// advertised and not answering is as short as it can be made.
	s.advertiser.stop()
	err := s.listener.Close()
	s.listener = nil
	return err
}

// authenticated checks the caller's TLS certificate against a paired peer
// before running the handler, and hands the handler which peer it is.
func (s *syncServer) authenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.TLS.PeerCertificates) == 0 {
			sendError(w, http.StatusUnauthorized, fmt.Errorf("no device certificate presented"))
			return
		}
		presented := certFingerprint(&tls.Certificate{Certificate: [][]byte{r.TLS.PeerCertificates[0].Raw}})
		var found *peer
		for _, candidate := range s.peers.all() {
			if candidate.Fingerprint == presented {
				local := candidate
				found = &local
				break
			}
		}
		if found == nil {
			sendError(w, http.StatusForbidden, fmt.Errorf("this device is not paired"))
			return
		}
		_ = s.peers.seen(found.ID, r.RemoteAddr)
		next.ServeHTTP(w, r.WithContext(withPeer(r.Context(), *found)))
	})
}

// loadOrCreateCertificate returns a certificate for id's own key, generating
// and persisting one on first use.
//
// It is signed by the device's own identity key rather than a throwaway one, so
// the certificate fingerprint a QR carries and the public key a peer record
// carries describe the same thing: this device, not this process.
func loadOrCreateCertificate(dir string, id *identity) (tls.Certificate, error) {
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "cert-key.pem")
	if certPEM, err := os.ReadFile(certPath); err == nil {
		if keyPEM, err := os.ReadFile(keyPath); err == nil {
			if cert, err := tls.X509KeyPair(certPEM, keyPEM); err == nil {
				return cert, nil
			}
		}
	}

	// The certificate carries its own key rather than the device's signing key,
	// so that the device key never appears on the wire in any form, including
	// as a certificate a TLS library serializes for you. The certificate's key
	// is what TLS actually needs; the device key is what pairing and manifests
	// are signed with.
	certKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: id.id},
		NotBefore:    time.Now().Add(-time.Hour),
		// Ten years: there is no renewal flow, and the fingerprint is pinned by
		// every peer the moment it pairs, so a certificate that changed would
		// look identical to an impostor to every device already paired.
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &certKey.PublicKey, certKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(certKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return tls.Certificate{}, err
	}
	if err := writeFileAtomically(certPath, certPEM, 0o600); err != nil {
		return tls.Certificate{}, err
	}
	if err := writeFileAtomically(keyPath, keyPEM, 0o600); err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(certPEM, keyPEM)
}

// publicKeyDER is the base64 SPKI encoding of a signer's public key, the form
// carried in every wire message that names a key.
func publicKeyDER(id *identity) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(id.signer.Public())
	if err != nil {
		return "", err
	}
	return pemlessBase64(der), nil
}

func pemlessBase64(der []byte) string {
	return base64.StdEncoding.EncodeToString(der)
}

type peerContextKey struct{}

func withPeer(ctx context.Context, p peer) context.Context {
	return context.WithValue(ctx, peerContextKey{}, p)
}

func peerFromContext(ctx context.Context) (peer, bool) {
	p, ok := ctx.Value(peerContextKey{}).(peer)
	return p, ok
}
