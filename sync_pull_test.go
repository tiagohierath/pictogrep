package main

// The receiving half, against a real desktop on a real socket.
//
// Same fixture as the sending tests, which is the point: a pull is not a second
// network, it is the same paired pair of devices with the request going the
// other way. What these prove is that the phone asks, takes what it lacks,
// leaves what it has, and never learns anything about the computer's disk
// beyond the pictures themselves.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// shareBytes puts exact bytes into a device's library, so that two devices can
// be given the SAME picture. The ordinary share helper derives its pixels from
// the file's path, which is a different temporary directory on each fixture, so
// two calls with one name produce two different pictures on purpose.
func (f *syncPeerFixture) shareBytes(t *testing.T, name string, data []byte) [32]byte {
	t.Helper()
	if _, _, err := f.server.saveImportedImage(bytes.NewReader(data), name, "", ""); err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(data)
}

// samePictureOnBoth gives two devices one picture, byte for byte, the way a
// library that has been syncing for a while looks.
func samePictureOnBoth(t *testing.T, name string, devices ...*syncPeerFixture) [32]byte {
	t.Helper()
	source := filepath.Join(t.TempDir(), name)
	writeTestPNG(t, source)
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	var digest [32]byte
	for _, device := range devices {
		digest = device.shareBytes(t, name, data)
	}
	return digest
}

// putInFolder adds a picture this device holds to one of its folders, creating
// the folder if it is new.
func (f *syncPeerFixture) putInFolder(t *testing.T, folder string, digest [32]byte) {
	t.Helper()
	path, held := f.sync.libraryIndex()[digest]
	if !held {
		t.Fatal("putInFolder was given a picture this device does not have")
	}
	if _, err := f.server.linkTag(folder, path); err != nil {
		t.Fatal(err)
	}
}

// pull runs one pass and waits for it, so a test reads as a sequence rather
// than as a poll loop. Bounded, because a pull that never finishes should fail
// the test rather than hang the suite.
func (f *syncPeerFixture) pull(t *testing.T, from *syncPeerFixture, folder, into string) pullState {
	t.Helper()
	target, found := f.sync.puller.reachablePeer("")
	if !found {
		t.Fatal("no reachable peer to pull from")
	}
	if err := f.sync.puller.start(target, folder, into); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		state := f.sync.puller.snapshot()
		if !state.Running {
			return state
		}
		if time.Now().After(deadline) {
			t.Fatal("the pull never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAPhoneTakesPicturesTheDesktopHasAndItDoesNot(t *testing.T) {
	phone, desktop := pairedPhoneAndDesktop(t)

	beach := desktop.share(t, "beach.png")
	market := desktop.share(t, "market.png")

	if phone.holds(beach) || phone.holds(market) {
		t.Fatal("the phone started with the desktop's pictures")
	}

	state := phone.pull(t, desktop, "", "")

	if !phone.holds(beach) || !phone.holds(market) {
		t.Fatal("a picture the desktop had did not reach the phone")
	}
	if state.Done != 2 || state.Failed != 0 {
		t.Fatalf("pull reported done=%d failed=%d, want 2 and 0", state.Done, state.Failed)
	}
}

func TestAPullSkipsWhatThePhoneAlreadyHas(t *testing.T) {
	phone, desktop := pairedPhoneAndDesktop(t)

	// The same picture on both sides, which is the ordinary case after the
	// phone has been sending for a while: identical bytes, so an identical
	// hash, whatever either side calls the file.
	shared := samePictureOnBoth(t, "beach.png", desktop, phone)
	if !phone.holds(shared) || !desktop.holds(shared) {
		t.Fatal("the fixture did not put the same picture on both sides")
	}
	desktop.share(t, "market.png")

	state := phone.pull(t, desktop, "", "")

	if state.Skipped != 1 {
		t.Fatalf("pull skipped %d, want the 1 the phone already had", state.Skipped)
	}
	if state.Done != 1 {
		t.Fatalf("pull downloaded %d, want only the 1 that was new", state.Done)
	}
}

func TestAPullTakesOnlyTheFolderItWasAskedFor(t *testing.T) {
	phone, desktop := pairedPhoneAndDesktop(t)

	inFolder := desktop.share(t, "beach.png")
	loose := desktop.share(t, "market.png")
	desktop.putInFolder(t, "trips", inFolder)

	state := phone.pull(t, desktop, "trips", "")

	if !phone.holds(inFolder) {
		t.Fatal("the folder's own picture did not arrive")
	}
	if phone.holds(loose) {
		t.Fatal("a picture outside the folder arrived anyway")
	}
	if state.Done != 1 {
		t.Fatalf("pull downloaded %d, want 1", state.Done)
	}
}

// The catalogue is how a phone draws its folder picker, and the one place a
// computer describes its library to another device. It must not describe where
// that library lives.
func TestTheCatalogueNamesNoPathOnTheComputer(t *testing.T) {
	phone, desktop := pairedPhoneAndDesktop(t)
	beach := desktop.share(t, "beach.png")
	desktop.putInFolder(t, "trips", beach)

	target, found := phone.sync.puller.reachablePeer("")
	if !found {
		t.Fatal("no reachable peer")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	catalogue, err := phone.sync.puller.catalogueOf(ctx, target, "")
	if err != nil {
		t.Fatal(err)
	}

	if len(catalogue.Entries) != 1 {
		t.Fatalf("catalogue listed %d pictures, want 1", len(catalogue.Entries))
	}
	if got := catalogue.Entries[0].Name; strings.ContainsAny(got, `/\`) {
		t.Errorf("catalogue entry name %q carries a path separator", got)
	}
	// The folder list is what the picker draws, so it has to be there whether
	// or not a folder was named in the request.
	if len(catalogue.Folders) != 1 || catalogue.Folders[0].Name != "trips" {
		t.Errorf("catalogue folders = %+v, want the one folder named trips", catalogue.Folders)
	}
}

// A hash that is not in the library is not a file the caller gets to read.
// Without this, "give me that one" would be a way to ask a paired computer for
// anything it can open.
func TestADownloadIsRefusedForAnythingNotInTheLibrary(t *testing.T) {
	phone, desktop := pairedPhoneAndDesktop(t)
	desktop.share(t, "beach.png")

	target, _ := phone.sync.puller.reachablePeer("")
	client := phone.sync.peerClient(target)
	for _, hash := range []string{
		"0000000000000000000000000000000000000000000000000000000000000000", // well formed, not held
		"../../etc/passwd", // not a hash at all
		"",
	} {
		response, err := client.Get("https://" + target.Address + "/blobs/" + hash)
		if err != nil {
			continue // a malformed path the router refuses outright is also a refusal
		}
		body := response.StatusCode
		response.Body.Close()
		if body == 200 {
			t.Errorf("a download of %q was served", hash)
		}
	}
}

// A pull is one at a time. Two at once would have both writing into the same
// library with two sets of counters, and the second would report progress that
// belonged to neither.
func TestASecondPullIsRefusedWhileOneIsRunning(t *testing.T) {
	phone, desktop := pairedPhoneAndDesktop(t)
	desktop.share(t, "beach.png")

	target, _ := phone.sync.puller.reachablePeer("")
	if err := phone.sync.puller.start(target, "", ""); err != nil {
		t.Fatal(err)
	}
	// Whether the first has finished by now is a race, so this asserts only the
	// thing that is always true: if it is still running, a second is refused.
	if phone.sync.puller.snapshot().Running {
		if err := phone.sync.puller.start(target, "", ""); err == nil {
			t.Error("a second pull started while the first was running")
		}
	}
}
