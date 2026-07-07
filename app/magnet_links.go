package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type MagnetLink struct {
	InfoHash    [20]byte
	DisplayName string
	Trackers    []string
}

func parseMagnetLink(raw string) (MagnetLink, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return MagnetLink{}, fmt.Errorf("parse magnet link: %w", err)
	}

	if u.Scheme != "magnet" {
		return MagnetLink{}, fmt.Errorf("invalid magnet link scheme")
	}

	query := u.Query()

	var infoHash [20]byte
	foundInfoHash := false

	for _, xt := range query["xt"] {
		const prefix = "urn:btih:"
		if !strings.HasPrefix(xt, prefix) {
			continue
		}

		hashText := strings.TrimPrefix(xt, prefix)

		hashBytes, err := hex.DecodeString(hashText)
		if err != nil {
			return MagnetLink{}, fmt.Errorf("decode info hash: %w", err)
		}
		if len(hashBytes) != 20 {
			return MagnetLink{}, fmt.Errorf("info hash must be 20 bytes")
		}

		copy(infoHash[:], hashBytes)
		foundInfoHash = true
		break
	}

	if !foundInfoHash {
		return MagnetLink{}, fmt.Errorf("magnet link missing btih info hash")
	}

	return MagnetLink{
		InfoHash:    infoHash,
		DisplayName: query.Get("dn"),
		Trackers:    query["tr"],
	}, nil
}

func requestMagnetPeers(ctx context.Context, magnet MagnetLink, peerID [20]byte) ([]string, error) {
	if len(magnet.Trackers) == 0 {
		return nil, fmt.Errorf("magnet link missing tracker url")
	}

	trackerRequest := TrackerRequest{
		AnnounceURL: magnet.Trackers[0],
		InfoHash:    magnet.InfoHash,
		PeerID:      peerID,
		Port:        6881,
		Uploaded:    0,
		Downloaded:  0,
		Left:        0,
		Compact:     true,
	}

	client := &http.Client{
		Timeout: 20 * time.Second,
	}

	responseData, err := requestPeers(ctx, client, trackerRequest)
	if err != nil {
		return nil, fmt.Errorf("request magnet peers: %w", err)
	}

	trackerResponse, err := decodeTrackerResponse(responseData)
	if err != nil {
		return nil, fmt.Errorf("decode tracker response: %w", err)
	}

	peers, ok := trackerResponse["peers"].(string)
	if !ok {
		return nil, fmt.Errorf("tracker peers must be a byte string")
	}

	peerAddresses, err := parseCompactPeers(peers)
	if err != nil {
		return nil, fmt.Errorf("parse compact peers: %w", err)
	}

	return peerAddresses, nil
}

func magnetHandshake(ctx context.Context, magnet MagnetLink, peerID [20]byte) ([20]byte, error) {
	peerAddresses, err := requestMagnetPeers(ctx, magnet, peerID)
	if err != nil {
		return [20]byte{}, err
	}
	if len(peerAddresses) == 0 {
		return [20]byte{}, fmt.Errorf("tracker returned no peers")
	}

	var lastErr error
	for _, peerAddress := range peerAddresses {
		conn, receivedPeerID, err := connectToPeerWithExtensions(ctx, peerAddress, magnet.InfoHash, peerID)
		if err != nil {
			lastErr = err
			continue
		}
		conn.Close()

		return receivedPeerID, nil
	}

	return [20]byte{}, fmt.Errorf("magnet handshake failed: %w", lastErr)
}
