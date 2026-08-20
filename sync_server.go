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
	"sync/atomic"
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
	// digests answers "what is in this library", for the manifest this device
	// serves and for the one its outbox asks of others.
	digests *digestCache
	// outbox is what sends. Nil is a valid state and means this device only
	// receives; see newSyncServer.
	outbox *outbox

	listener net.Listener
	// advertiser makes this machine findable again after its address changes.
	// Held here rather than started alongside the app because there is nothing
	// to announce until the port is open. See sync_mdns.go.
	advertiser mdnsAdvertiser

	// port to listen on, or 0 for syncPort. Only a test sets this, so that two
	// instances can pair inside one process without fighting over the one fixed
	// port a real installation wants.
	port int

	// arrivals counts pictures this device has received over sync, ever. Not
	// the interesting number on its own: what matters is whether it moved
	// since the page last asked, which is what tells a desktop something
	// showed up without it having to compare its whole library on a timer.
	// See idle.go's heartbeat, which is what carries this out.
	arrivals atomic.Int64
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
	server := &syncServer{
		appServer: appServer, app: app,
		identity: id, peers: peers, pairing: newPairingManager(), cert: cert,
		digests: openDigestCache(filepath.Join(dir, "digests.json")),
	}
	server.outbox = newOutbox(server, dir)
	return server, nil
}

// libraryIndex is every picture in the library by content hash, which is what
// both halves of a transfer are decided from: what this device can answer a
// manifest with, and what its outbox still has to offer.
func (s *syncServer) libraryIndex() map[[32]byte]string {
	paths, _, _ := s.app.snapshot()
	return s.digests.index(paths)
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
	port := s.port
	if port == 0 {
		port = syncPort
	}
	listener, err := tls.Listen("tcp", fmt.Sprintf(":%d", port), config)
	if err != nil {
		return err
	}
	s.listener = listener
	mux := http.NewServeMux()
	// The only door open to someone who has never paired.
	mux.HandleFunc("POST /pair", s.handlePair)
	// Everything past this line checks the caller against the peer store.
	//
	// POST, not GET, though it only asks a question: the question is a list of
	// hashes, which is a body, and a GET carrying a body is the kind of request
	// that works until it meets a proxy or an HTTP stack that drops it.
	mux.Handle("POST /manifest", s.authenticated(http.HandlerFunc(s.handleManifest)))
	mux.Handle("POST /blobs/{hash}", s.authenticated(http.HandlerFunc(s.handleUploadBlob)))
	go func() { _ = http.Serve(listener, mux) }()
	// Announced only once the listener is up, so nothing is ever told to knock
	// on a door that is not there yet. A failure here is logged and swallowed:
	// what matters to a caller of start() is that the port is open, and every
	// paired device can still reach it by its stored address or a fresh QR.
	if err := s.advertiser.start(s.identity.id, s.identity.name, s.listeningPort()); err != nil {
		logMDNSUnavailable(err)
	}
	return nil
}

// listeningPort is the port actually open, which is what a QR and an mDNS
// record have to carry: a constant is what was asked for, and this is what was
// got. Zero when nothing is listening.
func (s *syncServer) listeningPort() int {
	if s.listener == nil {
		return 0
	}
	if addr, ok := s.listener.Addr().(*net.TCPAddr); ok {
		return addr.Port
	}
	return 0
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
