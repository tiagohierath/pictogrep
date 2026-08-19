package main

// The point of this test is that the advertisement is real: not that
// NewMDNSService returned without an error, but that a query sent over an
// actual multicast socket comes back with this device's id in the TXT record.
// Network code that only ever gets checked by compiling is how you find out on
// a phone, in another room, that the field name was wrong.

import (
	"testing"
	"time"

	"github.com/hashicorp/mdns"
)

func TestAdvertiserIsDiscoverableWithItsDeviceID(t *testing.T) {
	const (
		id   = "QK7ZB4T2MHW5X3AA"
		name = "Tiago's Laptop"
	)
	var advertiser mdnsAdvertiser
	if err := advertiser.start(id, name, syncPort); err != nil {
		// A machine with multicast blocked cannot prove or disprove anything
		// here, and the app itself treats this as survivable, so the test does
		// too rather than failing on somebody's VPN.
		t.Skipf("mDNS unavailable on this machine: %v", err)
	}
	defer advertiser.stop()

	// Queried in a loop because the first multicast query can be sent before
	// the responder has finished binding, and a single miss would make this
	// test flaky in exactly the way a network test must not be.
	deadline := time.Now().Add(10 * time.Second)
	for {
		found, err := discoverPeers(&mdns.QueryParam{
			Service: mdnsService,
			Domain:  "local",
			Timeout: time.Second,
			// IPv6 multicast is not available in every sandbox, and the desktop
			// only ever advertises an IPv4 port anyway.
			DisableIPv6: true,
		})
		if err != nil {
			t.Skipf("mDNS query failed on this machine: %v", err)
		}
		entry, ok := found[id]
		if !ok {
			if time.Now().After(deadline) {
				t.Fatalf("advertised device %s never appeared in a query for %s (saw %d other services)", id, mdnsService, len(found))
			}
			continue
		}
		if got, _ := txtField(entry.InfoFields, mdnsTXTDeviceName); got != name {
			t.Errorf("TXT %s = %q, want %q", mdnsTXTDeviceName, got, name)
		}
		if got, _ := txtField(entry.InfoFields, mdnsTXTVersion); got != "1" {
			t.Errorf("TXT %s = %q, want \"1\"", mdnsTXTVersion, got)
		}
		if entry.Port != syncPort {
			t.Errorf("advertised port = %d, want %d", entry.Port, syncPort)
		}
		return
	}
}

// A second start must not put a second responder on the network under the same
// instance name, because "Connect phone" is a button a person can press twice.
func TestAdvertiserStartIsIdempotent(t *testing.T) {
	var advertiser mdnsAdvertiser
	if err := advertiser.start("QK7ZB4T2MHW5X3AB", "Test", syncPort); err != nil {
		t.Skipf("mDNS unavailable on this machine: %v", err)
	}
	defer advertiser.stop()
	first := advertiser.server
	if err := advertiser.start("QK7ZB4T2MHW5X3AB", "Test", syncPort); err != nil {
		t.Fatalf("second start: %v", err)
	}
	if advertiser.server != first {
		t.Error("second start replaced the running responder instead of doing nothing")
	}
}

// stop on an advertiser that never started is a no-op, because syncServer.stop
// runs it on a server whose mDNS may have failed to come up at all.
func TestAdvertiserStopWithoutStart(t *testing.T) {
	var advertiser mdnsAdvertiser
	advertiser.stop()
}

func TestTXTFieldReadsOnlyExactKeys(t *testing.T) {
	fields := []string{"id=ABC", "name=Desk", "v=1"}
	if got, ok := txtField(fields, mdnsTXTDeviceID); !ok || got != "ABC" {
		t.Errorf("id = %q, %v", got, ok)
	}
	// "i" is a prefix of "id" but not a key, and a naive match would return the
	// wrong device's identity, which is the one mistake here that would be
	// dangerous rather than merely broken.
	if got, ok := txtField(fields, "i"); ok {
		t.Errorf("key %q matched, returning %q", "i", got)
	}
	if _, ok := txtField(nil, mdnsTXTDeviceID); ok {
		t.Error("empty TXT record reported a device id")
	}
}
