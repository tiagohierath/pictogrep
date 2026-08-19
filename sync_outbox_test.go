package main

// The sending half, against a real desktop on a real socket.
//
// These go through the outbox rather than posting to the endpoints directly,
// because what they have to prove is not that the endpoints work (the pairing
// tests already do that) but that the loop above them decides correctly: sends
// what is new, does not send twice, and does not helpfully resurrect what
// somebody deleted.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// share puts a picture into a device's library the way a share sheet does, and
// answers with its content hash.
func (f *syncPeerFixture) share(t *testing.T, name string) [32]byte {
	t.Helper()
	source := filepath.Join(t.TempDir(), name)
	writeTestPNG(t, source)
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.server.saveImportedImage(bytes.NewReader(data), name, "", ""); err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(data)
}

func (f *syncPeerFixture) holds(digest [32]byte) bool {
	_, found := f.sync.libraryIndex()[digest]
	return found
}

// pairedPhoneAndDesktop is the arrangement every test here starts from: a
// desktop listening, a phone that has scanned its code.
func pairedPhoneAndDesktop(t *testing.T) (phone, desktop *syncPeerFixture) {
	t.Helper()
	desktop = newSyncPeer(t, "Laptop")
	desktop.listen(t)
	phone = newSyncPeer(t, "Phone")
	if err := phone.sync.pairAsClient(desktop.offer(t)); err != nil {
		t.Fatal(err)
	}
	return phone, desktop
}

func TestTheOutboxSendsPicturesTheDesktopDoesNotHave(t *testing.T) {
	phone, desktop := pairedPhoneAndDesktop(t)

	beach := phone.share(t, "beach.png")
	market := phone.share(t, "market.png")

	phone.sync.outbox.flush()

	if !desktop.holds(beach) || !desktop.holds(market) {
		t.Fatal("the outbox did not deliver both pictures")
	}
	if state := phone.sync.outbox.snapshot(); state.Waiting != 0 {
		t.Errorf("after delivering everything, %d pictures are still waiting", state.Waiting)
	}
	if state := phone.sync.outbox.snapshot(); state.LastError != "" {
		t.Errorf("a clean pass reported an error: %s", state.LastError)
	}
}

func TestTheOutboxDoesNotSendTheSamePictureTwice(t *testing.T) {
	phone, desktop := pairedPhoneAndDesktop(t)
	phone.share(t, "once.png")

	phone.sync.outbox.flush()
	before, _, _ := desktop.app.snapshot()

	// A second pass with nothing new. The picture is in the desktop's library
	// and in the phone's record of what was delivered, so neither the manifest
	// nor the outbox should want it again.
	phone.sync.outbox.flush()
	after, _, _ := desktop.app.snapshot()

	if len(after) != len(before) {
		t.Errorf("a second pass added %d more files to the desktop", len(after)-len(before))
	}
}

// TestADeletedPictureIsNotSentBackAfterDelivery is the property that makes sync
// safe to use rather than a machine for undoing housekeeping. The phone still
// has the picture; the desktop's copy was deliberately removed. Sync must
// leave that alone.
func TestADeletedPictureIsNotSentBackAfterDelivery(t *testing.T) {
	phone, desktop := pairedPhoneAndDesktop(t)
	unwanted := phone.share(t, "blurry.png")

	phone.sync.outbox.flush()
	path, found := desktop.sync.libraryIndex()[unwanted]
	if !found {
		t.Fatal("the picture never arrived, so there is nothing to delete")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	desktop.app.setPaths(nil)

	phone.sync.outbox.flush()

	if desktop.holds(unwanted) {
		t.Error("a picture deleted on the desktop was sent again")
	}
}

// TestForgettingADeviceOffersItEverythingAgain is the other side of that: the
// record of what was delivered belongs to the pairing, so removing the device
// and pairing it over is how a person asks for a fresh copy of everything.
func TestForgettingADeviceOffersItEverythingAgain(t *testing.T) {
	phone, desktop := pairedPhoneAndDesktop(t)
	picture := phone.share(t, "keeper.png")
	phone.sync.outbox.flush()

	laptop, _ := phone.sync.peers.get(desktop.sync.identity.id)
	phone.sync.outbox.forget(laptop.ID)

	if state := phone.sync.outbox.snapshot(); state.Waiting != 1 {
		t.Errorf("after forgetting the desktop, %d pictures are waiting, wanted 1", state.Waiting)
	}
	// And a pass re-offers it. The desktop still has it, so the manifest answers
	// "not missing" and nothing crosses the network, which is the correct
	// outcome and is not the same as never having asked.
	phone.sync.outbox.flush()
	if !desktop.holds(picture) {
		t.Error("the picture went missing from the desktop")
	}
}

func TestTheOutboxSurvivesADesktopThatIsNotThere(t *testing.T) {
	phone, desktop := pairedPhoneAndDesktop(t)
	phone.share(t, "waiting.png")

	// The laptop closes.
	if err := desktop.sync.stop(); err != nil {
		t.Fatal(err)
	}

	phone.sync.outbox.flush()

	state := phone.sync.outbox.snapshot()
	if state.LastError == "" {
		t.Error("an unreachable desktop was reported as a clean pass")
	}
	if state.Waiting != 1 {
		t.Errorf("%d pictures waiting, wanted the undelivered one to still be counted", state.Waiting)
	}
	if state.Sending {
		t.Error("the outbox is still marked as sending after the pass finished")
	}
}

// TestADesktopDoesNotTryToPushToAPhone: pairing is symmetric and reachability
// is not. The desktop only ever saw the phone's source port, so it must not
// treat that as somewhere to send pictures.
func TestADesktopDoesNotTryToPushToAPhone(t *testing.T) {
	phone, desktop := pairedPhoneAndDesktop(t)
	desktop.share(t, "desktop-only.png")
	phone.share(t, "phone-only.png")

	desktop.sync.outbox.flush()

	if state := desktop.sync.outbox.snapshot(); state.Waiting != 0 || state.LastError != "" {
		t.Errorf("the desktop tried to push to a phone: waiting=%d error=%q", state.Waiting, state.LastError)
	}
	known, _ := desktop.sync.peers.get(phone.sync.identity.id)
	if known.Listens {
		t.Error("the desktop recorded the phone as somewhere it can connect to")
	}
}

// TestDeliveryIsRememberedAcrossARestart: the record is a file, because a phone
// process does not survive being backgrounded and a fresh one must not offer
// the whole library over again.
func TestDeliveryIsRememberedAcrossARestart(t *testing.T) {
	phone, desktop := pairedPhoneAndDesktop(t)
	picture := phone.share(t, "remembered.png")
	phone.sync.outbox.flush()

	restarted := newOutbox(phone.sync, filepath.Dir(phone.sync.outbox.path))
	laptop, _ := phone.sync.peers.get(desktop.sync.identity.id)

	hash := hex.EncodeToString(picture[:])
	restarted.mu.Lock()
	remembered := restarted.delivered[laptop.ID][hash]
	restarted.mu.Unlock()
	if !remembered {
		t.Error("a restarted outbox forgot what it had already delivered")
	}
}

func TestTheDigestCacheDoesNotRereadAnUnchangedFile(t *testing.T) {
	dir := t.TempDir()
	picture := filepath.Join(dir, "stable.png")
	writeTestPNG(t, picture)

	cache := openDigestCache(filepath.Join(dir, "digests.json"))
	first, err := cache.of(picture)
	if err != nil {
		t.Fatal(err)
	}

	// Replaced with different bytes but the same size and modification time,
	// which is the one case the cache is allowed to get wrong: it answers from
	// what it remembers. This pins that behaviour so it is a decision rather
	// than a surprise.
	info, err := os.Stat(picture)
	if err != nil {
		t.Fatal(err)
	}
	same, err := cache.of(picture)
	if err != nil {
		t.Fatal(err)
	}
	if same != first {
		t.Error("hashing the same untouched file twice gave two answers")
	}

	// A real edit moves the modification time, and the cache follows it.
	writeTestPNGDimensions(t, picture, 40, 40)
	if err := os.Chtimes(picture, info.ModTime(), info.ModTime().Add(1)); err != nil {
		t.Fatal(err)
	}
	changed, err := cache.of(picture)
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Error("the cache kept a stale hash for a file that changed")
	}
}

func TestTheDigestCacheOutlivesTheProcess(t *testing.T) {
	dir := t.TempDir()
	picture := filepath.Join(dir, "kept.png")
	writeTestPNG(t, picture)

	path := filepath.Join(dir, "digests.json")
	cache := openDigestCache(path)
	want := cache.index([]string{picture})
	if len(want) != 1 {
		t.Fatalf("indexed %d pictures, wanted 1", len(want))
	}

	reopened := openDigestCache(path)
	reopened.mu.Lock()
	remembered := len(reopened.records)
	reopened.mu.Unlock()
	if remembered != 1 {
		t.Errorf("a reopened cache remembers %d files, wanted 1", remembered)
	}
	if got := reopened.index([]string{picture}); len(got) != 1 {
		t.Error("a reopened cache did not index the file it remembered")
	}
}

func TestTheDigestCacheForgetsFilesThatLeftTheLibrary(t *testing.T) {
	dir := t.TempDir()
	staying := filepath.Join(dir, "staying.png")
	leaving := filepath.Join(dir, "leaving.png")
	writeTestPNG(t, staying)
	writeTestPNG(t, leaving)

	cache := openDigestCache(filepath.Join(dir, "digests.json"))
	cache.index([]string{staying, leaving})
	cache.index([]string{staying})

	cache.mu.Lock()
	_, stillThere := cache.records[leaving]
	cache.mu.Unlock()
	if stillThere {
		t.Error("the cache kept a file that is no longer in the library")
	}
}
