/*
File Name:  Bootstrap.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner

Strategy for sending our IPv6 Multicast and IPv4 Broadcast messages:
* During bootstrap: Immediately at the beginning, then every 10 seconds until there is at least 1 peer.
* Every 10 minutes during regular operation.
* Each time a network adapter / IP change is detected. [FUTURE]

*/

package core

import (
	"encoding/hex"
	"errors"
	"log"
	"net"
	"strconv"
	"time"

	"github.com/btcsuite/btcd/btcec"
)

// rootPeer is a single root peer info
type rootPeer struct {
	peer      *PeerInfo        // loaded PeerInfo
	publicKey *btcec.PublicKey // Public key
	addresses []*net.UDPAddr   // IP:Port addresses
}

var rootPeers map[[btcec.PubKeyBytesLenCompressed]byte]*rootPeer

// initSeedList loads the seed list from the config
func initSeedList() {
	rootPeers = make(map[[btcec.PubKeyBytesLenCompressed]byte]*rootPeer)

loopSeedList:
	for _, seed := range config.SeedList {
		peer := &rootPeer{}

		// parse the Public Key
		publicKeyB, err := hex.DecodeString(seed.PublicKey)
		if err != nil {
			log.Printf("initSeedList error public key '%s': %v", seed.PublicKey, err.Error())
			continue
		}

		if peer.publicKey, err = btcec.ParsePubKey(publicKeyB, btcec.S256()); err != nil {
			log.Printf("initSeedList error public key '%s': %v", seed.PublicKey, err.Error())
			continue
		}

		// parse all IP addresses
		for _, addressA := range seed.Address {
			address, err := parseAddress(addressA)
			if err != nil {
				log.Printf("initSeedList error public key '%s' address '%s': %v", seed.PublicKey, addressA, err.Error())
				continue loopSeedList
			}

			peer.addresses = append(peer.addresses, address)
		}

		rootPeers[publicKey2Compressed(peer.publicKey)] = peer
	}
}

// parseAddress parses an input peer address in the form "IP:Port".
func parseAddress(Address string) (remote *net.UDPAddr, err error) {
	host, portA, err := net.SplitHostPort(Address)
	if err != nil {
		return nil, err
	}

	portI, err := strconv.Atoi(portA)
	if err != nil {
		return nil, err
	} else if portI <= 0 || portI > 65535 {
		return nil, errors.New("invalid port number")
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return nil, errors.New("invalid input IP")
	}

	return &net.UDPAddr{IP: ip, Port: portI}, err
}

// contact tries to contact the root peer on all networks
func (peer *rootPeer) contact() {
	for _, address := range peer.addresses {
		sendAllNetworks(peer.publicKey, &PacketRaw{Command: 0}, address)
	}
}

// bootstrap connects to the initial set of peers. It will also start the routine for ongoing sending of multicast/broadcast messages.
func bootstrap() {
	if len(rootPeers) == 0 {
		log.Printf("bootstrap warning: Empty list of root peers. Connectivity relies on local peer discovery and incoming connections.\n")
		return
	}

	contactRootPeers := func() {
		for _, peer := range rootPeers {
			if peer.peer == nil {
				peer.contact()
			}
		}
	}

	countConnectedRootPeers := func() (connectedCount, total int) {
		for _, peer := range rootPeers {
			if peer.peer != nil {
				connectedCount++
			} else if peer.peer = PeerlistLookup(peer.publicKey); peer.peer != nil {
				connectedCount++
			}
		}
		return connectedCount, len(rootPeers)
	}

	// initial contact to all root peer
	contactRootPeers()

	// Phase 1: First 10 minutes. Try every 7 seconds to connect to all root peers until at least 2 peers connected.
	for n := 0; n < 10*60/7; n++ {
		time.Sleep(time.Second * 7)

		if connected, total := countConnectedRootPeers(); connected == total || connected >= 2 {
			return
		}

		contactRootPeers()
	}

	// Phase 2: After that (if not 2 peers), try every 5 minutes to connect to remaining root peers for a maximum of 1 hour.
	for n := 0; n < 1*60/5; n++ {
		time.Sleep(time.Minute * 5)

		contactRootPeers()

		if connected, total := countConnectedRootPeers(); connected == total || connected >= 2 {
			return
		}
	}

	log.Printf("bootstrap unable to connect to at least 2 root peers, aborting\n")
}

func autoMulticastBroadcast() {
	sendMulticastBroadcast := func() {
		networksMutex.RLock()
		defer networksMutex.RUnlock()

		for _, network := range networks6 {
			if err := network.MulticastIPv6Send(); err != nil {
				log.Printf("bootstrap error multicast from network address '%s': %v", network.address.IP.String(), err.Error())
			}
		}

		for _, network := range networks4 {
			if err := network.BroadcastIPv4Send(); err != nil {
				log.Printf("bootstrap error broadcast from network address '%s': %v", network.address.IP.String(), err.Error())
			}
		}
	}

	// Send out multicast/broadcast immediately.
	sendMulticastBroadcast()

	// Phase 1: Resend every 10 seconds until at least 1 peer in the peer list
	for {
		time.Sleep(time.Second * 10)

		if PeerlistCount() >= 1 {
			break
		}

		sendMulticastBroadcast()
	}

	// Phase 2: Every 10 minutes.
	for {
		time.Sleep(time.Minute * 10)
		sendMulticastBroadcast()
	}
}
