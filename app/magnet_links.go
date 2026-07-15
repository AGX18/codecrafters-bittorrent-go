package main

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
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

const (
	extendedMessageID          byte = 20
	extensionHandshakeID       byte = 0
	localUTMetadataExtensionID      = 16
)

type MagnetHandshakeResult struct {
	PeerID              [20]byte
	MetadataExtensionID int
}

func buildExtensionHandshakeMessage() []byte {
	payload := fmt.Appendf(nil, "d1:md11:ut_metadatai%deee", localUTMetadataExtensionID)
	message := make([]byte, 6+len(payload))

	binary.BigEndian.PutUint32(message[0:4], uint32(2+len(payload)))
	message[4] = extendedMessageID
	message[5] = extensionHandshakeID
	copy(message[6:], payload)

	return message
}

func parseMetadataExtensionID(msg PeerMessage) (int, error) {
	if msg.ID != extendedMessageID {
		return 0, fmt.Errorf("expected extended message, got %d", msg.ID)
	}
	if len(msg.Payload) < 2 {
		return 0, fmt.Errorf("extension handshake payload too short")
	}
	if msg.Payload[0] != extensionHandshakeID {
		return 0, fmt.Errorf("expected extension handshake, got extension message %d", msg.Payload[0])
	}

	decoded, err := decodeBencode(string(msg.Payload[1:]))
	if err != nil {
		return 0, fmt.Errorf("decode extension handshake: %w", err)
	}

	handshake, ok := decoded.(map[string]interface{})
	if !ok {
		return 0, fmt.Errorf("extension handshake must be a dictionary")
	}

	extensions, ok := handshake["m"].(map[string]interface{})
	if !ok {
		return 0, fmt.Errorf("extension handshake missing extension mapping")
	}

	metadataID, ok := extensions["ut_metadata"].(int)
	if !ok {
		return 0, fmt.Errorf("peer does not advertise ut_metadata")
	}
	if metadataID < 1 || metadataID > 255 {
		return 0, fmt.Errorf("ut_metadata extension id must be between 1 and 255")
	}

	return metadataID, nil
}

func readMetadataExtensionID(r io.Reader) (int, error) {
	for {
		msg, err := readPeerMessage(r)
		if err != nil {
			return 0, fmt.Errorf("read extension handshake: %w", err)
		}

		if msg.ID != extendedMessageID {
			continue
		}
		if len(msg.Payload) == 0 {
			return 0, fmt.Errorf("extension message payload is empty")
		}
		if msg.Payload[0] != extensionHandshakeID {
			continue
		}

		return parseMetadataExtensionID(msg)
	}
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
		Left:        1,
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

func handshakeWithMagnetPeer(ctx context.Context, peerAddress string, infoHash [20]byte, peerID [20]byte) (result MagnetHandshakeResult, err error) {
	conn, receivedPeerID, supportsExtensions, err := connectToPeerWithExtensions(ctx, peerAddress, infoHash, peerID)
	if err != nil {
		return MagnetHandshakeResult{}, err
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil && err == nil {
			result = MagnetHandshakeResult{}
			err = fmt.Errorf("close peer connection: %w", closeErr)
		}
	}()

	if !supportsExtensions {
		return MagnetHandshakeResult{}, fmt.Errorf("peer does not support extensions")
	}

	deadline := time.Now().Add(15 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return MagnetHandshakeResult{}, fmt.Errorf("set extension handshake deadline: %w", err)
	}

	if _, err := waitForBitfield(conn); err != nil {
		return MagnetHandshakeResult{}, fmt.Errorf("wait for bitfield: %w", err)
	}

	if err := writeAll(conn, buildExtensionHandshakeMessage()); err != nil {
		return MagnetHandshakeResult{}, fmt.Errorf("send extension handshake: %w", err)
	}

	metadataExtensionID, err := readMetadataExtensionID(conn)
	if err != nil {
		return MagnetHandshakeResult{}, err
	}

	return MagnetHandshakeResult{
		PeerID:              receivedPeerID,
		MetadataExtensionID: metadataExtensionID,
	}, nil
}

func magnetHandshake(ctx context.Context, magnet MagnetLink, peerID [20]byte) (MagnetHandshakeResult, error) {
	peerAddresses, err := requestMagnetPeers(ctx, magnet, peerID)
	if err != nil {
		return MagnetHandshakeResult{}, err
	}
	if len(peerAddresses) == 0 {
		return MagnetHandshakeResult{}, fmt.Errorf("tracker returned no peers")
	}

	var lastErr error
	for _, peerAddress := range peerAddresses {
		result, err := handshakeWithMagnetPeer(ctx, peerAddress, magnet.InfoHash, peerID)
		if err != nil {
			lastErr = err
			continue
		}

		return result, nil
	}

	return MagnetHandshakeResult{}, fmt.Errorf("magnet handshake failed: %w", lastErr)
}
