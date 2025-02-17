// Copyright 2021 ChainSafe Systems
// SPDX-License-Identifier: LGPL-3.0-only

// Package crawl holds the eth2 node discovery utilities
package crawl

import (
	"context"
	"crypto/ecdsa"
	"eth2-crawler/store/peerstore"
	"eth2-crawler/store/record"
	"fmt"
	"net"

	"github.com/robfig/cron/v3"

	"eth2-crawler/crawler/p2p"
	ipResolver "eth2-crawler/resolver"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/log"
	"github.com/libp2p/go-libp2p"
	ic "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	"github.com/libp2p/go-libp2p/p2p/transport/tcp"
	ma "github.com/multiformats/go-multiaddr"
)

// listenConfig holds configuration for running v5discovry node
type listenConfig struct {
	bootNodeAddrs []string
	listenAddress net.IP
	listenPORT    int
	dbPath        string
	privateKey    *ecdsa.PrivateKey
}

// Initialize initializes the core crawler component
func Initialize(peerStore peerstore.Provider, historyStore record.Provider, ipResolver ipResolver.Provider, bootNodeAddrs []string) error {
	ctx := context.Background()
	pkey, _ := crypto.GenerateKey()
	listenCfg := &listenConfig{
		bootNodeAddrs: bootNodeAddrs,
		listenAddress: net.IPv4zero,
		listenPORT:    30304,
		dbPath:        "",
		privateKey:    pkey,
	}
	disc, err := startV5(listenCfg)
	if err != nil {
		return err
	}

	listenAddrs, err := multiAddressBuilder(listenCfg.listenAddress, listenCfg.listenPORT)
	if err != nil {
		return err
	}
	host, err := p2p.NewHost(
		libp2p.Identity(ConvertToInterfacePrivkey(listenCfg.privateKey)),
		libp2p.ListenAddrs(listenAddrs),
		libp2p.UserAgent("Eth2-Crawler"),
		libp2p.Transport(tcp.NewTCPTransport),
		libp2p.Security(noise.ID, noise.New),
		libp2p.NATPortMap(),
	)
	if err != nil {
		return err
	}

	c := newCrawler(disc, peerStore, historyStore, ipResolver, listenCfg.privateKey, disc.RandomNodes(), host, 200)
	go c.start(ctx)
	// scheduler for updating peer
	go c.updatePeer(ctx)

	// add scheduler for updating history store
	scheduler := cron.New()
	_, err = scheduler.AddFunc("@daily", c.insertToHistory)
	if err != nil {
		return err
	}
	_, err = scheduler.AddFunc("@every 1m", func() {
		log.Info("peer summary", "len", len(c.host.Network().Peers()))
	})
	scheduler.Start()
	return nil
}

//func convertToInterfacePrivkey(privkey *ecdsa.PrivateKey) ic.PrivKey {
//	typeAssertedKey := ic.PrivKey((*ic.Secp256k1PrivateKey)(privkey))
//	return typeAssertedKey
//}

func ConvertToInterfacePrivkey(privkey *ecdsa.PrivateKey) ic.PrivKey {
	privBytes := privkey.D.Bytes()
	// In the event the number of bytes outputted by the big-int are less than 32,
	// we append bytes to the start of the sequence for the missing most significant
	// bytes.
	if len(privBytes) < 32 {
		privBytes = append(make([]byte, 32-len(privBytes)), privBytes...)
	}
	if k, err := ic.UnmarshalSecp256k1PrivateKey(privBytes); err == nil {
		return k
	} else {
		panic(err)
	}
}

func multiAddressBuilder(ipAddr net.IP, port int) (ma.Multiaddr, error) {
	if ipAddr.To4() == nil && ipAddr.To16() == nil {
		return nil, fmt.Errorf("invalid ip address provided: %s", ipAddr)
	}
	if ipAddr.To4() != nil {
		return ma.NewMultiaddr(fmt.Sprintf("/ip4/%s/tcp/%d", ipAddr.String(), port))
	}
	return ma.NewMultiaddr(fmt.Sprintf("/ip6/%s/tcp/%d", ipAddr.String(), port))
}
