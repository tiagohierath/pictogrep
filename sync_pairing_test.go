package main

// The whole pairing path, both halves of it, in one process.
//
// Everything else about sync is testable in pieces: the QR encoder against its
// own output, the bridge against a fake key service, mDNS against a loopback
// socket. Pairing is the one part where the pieces only mean something
// together, because what it has to prove is not that a request succeeds but
// that the three ways it should fail do fail: a stranger with the right address
// and the wrong fingerprint, a replay of a secret that was already spent, and a
// device that never paired at all reaching for the library.
//
// So this file stands two complete instances up, a desktop and a phone, and
// pairs them over real TLS on a real socket.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// syncPeerFixture is one device: its library, its server, and its sync half.
type syncPeerFixture struct {
	app    *application
	server *server
	sync   *syncServer
}

// newSyncPeer builds a device. Listening is left to the caller: only the
// desktop half opens a port, exactly as in a real pairing.
func newSyncPeer(t *testing.T, name string) *syncPeerFixture {
	t.Helper()
	app := testApplication(t)
	handler, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	sync, err := newSyncServer(handler, name)
	if err != nil {
		t.Fatal(err)
	}
	handler.sync = sync
	return &syncPeerFixture{app: app, server: handler, sync: sync}
}

// listen opens the device's port on a free one rather than the fixed 58217,
// so that two instances can pair inside one test process and so that a test
// run does not fail because the developer has Pictogrep open.
func (f *syncPeerFixture) listen(t *testing.T) {
	t.Helper()
	f.sync.port = freePort(t)
	if err := f.sync.start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.sync.stop() })
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

// offer is the QR a desktop would draw, as a struct rather than as pixels.
// localAddresses() is deliberately not used: a test machine's real LAN address
// is not reachable from a test, and loopback is what both ends are on here.
func (f *syncPeerFixture) offer(t *testing.T) pairingSession {
	t.Helper()
	secret, err := f.sync.pairing.begin()
	if err != nil {
		t.Fatal(err)
	}
	return pairingSession{
		ProtocolVersion: 1,
		DeviceID:        f.sync.identity.id,
		Name:            f.sync.identity.name,
		Addresses:       []string{"127.0.0.1"},
		Port:            f.sync.listeningPort(),
		Fingerprint:     f.sync.fingerprint(),
		Secret:          secret,
	}
}

func TestPairingIntroducesTwoDevicesToEachOther(t *testing.T) {
	desktop := newSyncPeer(t, "Tiago's Laptop")
	desktop.listen(t)
	phone := newSyncPeer(t, "Tiago's Phone")

	if err := phone.sync.pairAsClient(desktop.offer(t)); err != nil {
		t.Fatalf("pairing failed: %v", err)
	}

	// Both directions: a pairing that only one side recorded is one that stops
	// working the moment the other is asked to prove anything.
	if _, found := desktop.sync.peers.get(phone.sync.identity.id); !found {
		t.Error("the desktop did not record the phone")
	}
	knownDesktop, found := phone.sync.peers.get(desktop.sync.identity.id)
	if !found {
		t.Fatal("the phone did not record the desktop")
	}
	if knownDesktop.Name != "Tiago's Laptop" {
		t.Errorf("the phone recorded the desktop as %q", knownDesktop.Name)
	}
	// The address the phone kept has to be one it can dial again. The desktop's
	// record of the phone is a source port and is not expected to be dialable.
	if !strings.HasPrefix(knownDesktop.Address, "127.0.0.1:") {
		t.Errorf("the phone kept an address it cannot dial: %q", knownDesktop.Address)
	}
	if knownDesktop.Fingerprint != desktop.sync.fingerprint() {
		t.Error("the phone pinned a fingerprint that is not the desktop's")
	}
}

func TestAPairingSecretCannotBeSpentTwice(t *testing.T) {
	desktop := newSyncPeer(t, "Laptop")
	desktop.listen(t)
	phone := newSyncPeer(t, "Phone")
	stranger := newSyncPeer(t, "Someone Else's Phone")

	session := desktop.offer(t)
	if err := phone.sync.pairAsClient(session); err != nil {
		t.Fatalf("the first pairing should have worked: %v", err)
	}
	// The same QR, photographed off the screen and replayed. This is the attack
	// the single-use secret exists for.
	if err := stranger.sync.pairAsClient(session); err == nil {
		t.Fatal("a spent pairing secret was accepted a second time")
	}
	if _, found := desktop.sync.peers.get(stranger.sync.identity.id); found {
		t.Error("the replaying device was paired anyway")
	}
}

func TestPairingRefusesAComputerThatIsNotTheOneInTheCode(t *testing.T) {
	desktop := newSyncPeer(t, "Laptop")
	desktop.listen(t)
	phone := newSyncPeer(t, "Phone")

	// Right address, right port, right secret, wrong machine: what a device on
	// the same wifi answering first would look like. The fingerprint is the only
	// thing that distinguishes it, which is the whole reason it is in the QR.
	session := desktop.offer(t)
	session.Fingerprint = strings.Repeat("00", sha256.Size)

	if err := phone.sync.pairAsClient(session); err == nil {
		t.Fatal("pairing completed against an unverified certificate")
	}
	if len(phone.sync.peers.all()) != 0 {
		t.Error("the phone recorded a peer it could not verify")
	}
	// And the secret was not spent by the attempt, so the person who is standing
	// there with the real code can still use it.
	if !desktop.sync.pairing.redeem(session.Secret) {
		t.Error("a failed handshake burned the pairing secret")
	}
}

func TestAnUnpairedDeviceCannotReachTheLibrary(t *testing.T) {
	desktop := newSyncPeer(t, "Laptop")
	desktop.listen(t)
	stranger := newSyncPeer(t, "Stranger")

	// A well-formed client with a valid certificate that simply never paired.
	// It knows where the desktop is; that is not supposed to be enough.
	client := stranger.sync.peerClient(peer{
		Name:        "Laptop",
		Fingerprint: desktop.sync.fingerprint(),
		Address:     fmt.Sprintf("127.0.0.1:%d", desktop.sync.listeningPort()),
	})
	response, err := client.Post(
		fmt.Sprintf("https://127.0.0.1:%d/manifest", desktop.sync.listeningPort()),
		"application/json", strings.NewReader(`{"hashes":[]}`),
	)
	if err != nil {
		t.Fatalf("the request did not reach the server: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Errorf("an unpaired device got %d from /manifest, wanted 403", response.StatusCode)
	}
}

// TestAPairedDeviceCanSendAPictureThatIsNotAlreadyThere walks the two requests
// that actually move a picture: ask what is missing, then send only that.
//
// The client here is the test's own rather than the outbox, so that the
// receiving half is pinned on its own terms: that a paired device is let in,
// that the manifest answers honestly, that the bytes land in the library, and
// that asking again afterwards reports nothing missing. sync_outbox_test.go
// covers the same ground through the loop that will actually drive it.
func TestAPairedDeviceCanSendAPictureThatIsNotAlreadyThere(t *testing.T) {
	desktop := newSyncPeer(t, "Laptop")
	desktop.listen(t)
	phone := newSyncPeer(t, "Phone")
	if err := phone.sync.pairAsClient(desktop.offer(t)); err != nil {
		t.Fatal(err)
	}

	picture := filepath.Join(t.TempDir(), "beach.png")
	writeTestPNG(t, picture)
	data, err := os.ReadFile(picture)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])

	laptop, _ := phone.sync.peers.get(desktop.sync.identity.id)
	client := phone.sync.peerClient(laptop)
	base := "https://" + laptop.Address

	if missing := askManifest(t, client, base, digest); len(missing) != 1 || missing[0] != digest {
		t.Fatalf("a picture the desktop has never seen was reported as %#v", missing)
	}

	response, err := client.Post(base+"/blobs/"+digest+"?name=beach.png", "application/octet-stream", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		body, _ := json.Marshal(response.Status)
		t.Fatalf("upload was refused: %s", body)
	}

	// In the library, by content: the name it arrived under is not what makes it
	// the same picture.
	if _, held := desktop.sync.libraryIndex()[sum]; !held {
		t.Fatal("the picture did not reach the desktop's library")
	}
	// And the second phone to offer the same picture, or the same phone after a
	// reinstall, is told not to bother sending it.
	if missing := askManifest(t, client, base, digest); len(missing) != 0 {
		t.Errorf("a picture already in the library was still reported missing: %#v", missing)
	}
}

func TestAnUploadThatDoesNotMatchItsHashIsRefused(t *testing.T) {
	desktop := newSyncPeer(t, "Laptop")
	desktop.listen(t)
	phone := newSyncPeer(t, "Phone")
	if err := phone.sync.pairAsClient(desktop.offer(t)); err != nil {
		t.Fatal(err)
	}

	picture := filepath.Join(t.TempDir(), "claimed.png")
	writeTestPNG(t, picture)
	data, err := os.ReadFile(picture)
	if err != nil {
		t.Fatal(err)
	}
	// The hash of something else entirely. Corruption in transit looks like this,
	// and so does a device trying to get a file into the library under a name the
	// manifest already agreed to.
	wrong := hex.EncodeToString(bytes.Repeat([]byte{0xAB}, sha256.Size))

	laptop, _ := phone.sync.peers.get(desktop.sync.identity.id)
	client := phone.sync.peerClient(laptop)
	response, err := client.Post(
		"https://"+laptop.Address+"/blobs/"+wrong, "application/octet-stream", bytes.NewReader(data),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("a mismatched upload got %d, wanted 422", response.StatusCode)
	}
	if _, held := desktop.sync.libraryIndex()[sha256.Sum256(data)]; held {
		t.Error("the mismatched upload was kept anyway")
	}
}

func askManifest(t *testing.T, client *http.Client, base string, hashes ...string) []string {
	t.Helper()
	body, err := json.Marshal(manifestRequest{Hashes: hashes})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Post(base+"/manifest", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("/manifest answered %d", response.StatusCode)
	}
	var answer manifestResponse
	if err := json.NewDecoder(response.Body).Decode(&answer); err != nil {
		t.Fatal(err)
	}
	return answer.Missing
}
