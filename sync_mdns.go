package main

// Being findable again after the address changes.
//
// Pairing hands the phone a list of IP addresses, because at the moment the QR
// is drawn that is the cheapest possible answer to "where is the desktop". It
// is also the shortest-lived one: a laptop takes a new DHCP lease, sleeps in
// one building and wakes in another, moves between wifi and a cable, and every
// address that QR carried now belongs to somebody else. Re-scanning a QR to fix
// that would mean pairing is not really something you do once, which was the
// whole promise.
//
// So the desktop announces itself instead. It advertises _pictogrep._tcp with
// its device id in a TXT record, and a device that already holds a peer record
// for that id looks up the id rather than trusting the address it stored. The
// id is derived from the device's public key (see sync_identity.go), so an
// impostor is free to put any id it likes in a TXT record and still gets
// nowhere: the certificate fingerprint pinned at pairing is what decides
// whether the thing that answered is really the desktop. What is advertised
// here is allowed to be wrong in exactly the way peer.Address is allowed to be
// wrong. It only ever suggests where to knock.
//
// The DNS wire format is not hand-rolled here. Encoding it by hand is the kind
// of job that looks finished long before it is correct, and unlike a file
// format you cannot check the result by reading it: proving it right needs a
// second machine and a packet capture. hashicorp/mdns already answers what real
// responders and Android's NsdManager expect, so this file stays small.

import (
	"fmt"
	"log"
	"strings"

	"github.com/hashicorp/mdns"
)

// mdnsService is the DNS-SD service type the desktop answers to, and the one
// the Android discovery side asks NsdManager for. Changing it breaks
// rediscovery for every already-paired device, silently, in the direction
// nobody tests: they simply stop finding each other.
const mdnsService = "_pictogrep._tcp"

// The TXT record keys. Constants because the Kotlin side has to spell them
// identically, and a typo there produces a service that is discovered and then
// ignored, which is harder to notice than one that is never found at all.
const (
	// mdnsTXTDeviceID carries the advertising device's id, the same string its
	// peer record is filed under. This is the field a discovering device
	// matches on, and the reason it does not have to open a connection just to
	// learn whether a responder is anyone it knows.
	mdnsTXTDeviceID = "id"
	// mdnsTXTDeviceName carries the display name, so a device that has not
	// paired with this one yet can still offer "Tiago's Laptop" rather than a
	// sixteen-character id nobody chose.
	mdnsTXTDeviceName = "name"
	// mdnsTXTVersion is the pairing protocol version, the same number
	// pairingSession.ProtocolVersion carries. Advertised so a future
	// incompatible responder can be skipped at discovery time rather than
	// halfway through a handshake.
	mdnsTXTVersion = "v"
)

// mdnsProtocolVersion matches the version a QR's pairingSession declares. The
// two have to move together: a device found by mDNS is paired with and talked
// to over the same protocol as one found by QR.
const mdnsProtocolVersion = 1

// mdnsAdvertiser owns at most one running responder.
type mdnsAdvertiser struct {
	server *mdns.Server
}

// start announces this device on the LAN.
//
// Idempotent, matching syncServer.start, because it is called from it: pressing
// "Connect phone" twice must not put a second responder on the network under
// the same instance name, which is the one thing mDNS genuinely will not
// tolerate.
func (a *mdnsAdvertiser) start(id, name string, port int) error {
	if a.server != nil {
		return nil
	}
	// The instance name is the device id, not the hostname. Instance names have
	// to be unique on the network, and two machines belonging to the same
	// person are exactly the case where hostnames collide: a desktop and a
	// laptop set up from the same image answer to the same name, and one of
	// them loses the record. An id cannot collide, being a hash of a key nobody
	// else holds.
	service, err := mdns.NewMDNSService(
		id,
		mdnsService,
		"", // domain: local., inferred
		"", // host name: this machine's, inferred
		port,
		nil, // addresses: this machine's, inferred, and re-read at answer time
		[]string{
			fmt.Sprintf("%s=%s", mdnsTXTDeviceID, id),
			fmt.Sprintf("%s=%s", mdnsTXTDeviceName, name),
			fmt.Sprintf("%s=%d", mdnsTXTVersion, mdnsProtocolVersion),
		},
	)
	if err != nil {
		return err
	}
	server, err := mdns.NewServer(&mdns.Config{Zone: service})
	if err != nil {
		return err
	}
	a.server = server
	return nil
}

func (a *mdnsAdvertiser) stop() {
	if a.server == nil {
		return
	}
	_ = a.server.Shutdown()
	a.server = nil
}

// logMDNSUnavailable reports that the announcement did not start, and then lets
// the app carry on.
//
// Multicast is the first thing a corporate network, a VPN client or a firewall
// profile switches off, and none of that stops sync working: the TLS listener
// is what actually serves, a QR still hands over an address, and a peer whose
// address has not changed still connects on the stored hint. Failing start()
// over this would take a working feature away to punish a missing shortcut.
func logMDNSUnavailable(err error) {
	log.Printf("sync: LAN announcement unavailable, devices will fall back to the stored address or a QR: %v", err)
}

// discoverPeers returns the Pictogrep devices answering on the LAN right now,
// keyed by the device id in their TXT record, with the address each is
// answering on at this moment.
//
// The desktop does not need this yet: the phone is the side that moves, and its
// discovery runs in Kotlin through NsdManager. It exists because desktop to
// desktop rediscovery will want precisely this and the library hands it over
// for nothing, and because it is what the test queries with, which is the only
// way to show the records are really on the wire rather than merely built
// without returning an error.
func discoverPeers(params *mdns.QueryParam) (map[string]mdns.ServiceEntry, error) {
	entries := make(chan *mdns.ServiceEntry, 8)
	found := map[string]mdns.ServiceEntry{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for entry := range entries {
			id, ok := txtField(entry.InfoFields, mdnsTXTDeviceID)
			if !ok {
				// Something else answered to this service type, or a responder
				// too old to carry the field. Either way there is nothing here
				// that can be matched against a peer record.
				continue
			}
			found[id] = *entry
		}
	}()
	params.Entries = entries
	err := mdns.Query(params)
	close(entries)
	<-done
	return found, err
}

// txtField reads one key=value out of a TXT record. A TXT record is an
// unordered bag of strings by definition, so this searches rather than indexes.
func txtField(fields []string, key string) (string, bool) {
	prefix := key + "="
	for _, field := range fields {
		if strings.HasPrefix(field, prefix) {
			return strings.TrimPrefix(field, prefix), true
		}
	}
	return "", false
}
