package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"time"
)

const blockSize = 16 * 1024

func buildInterestedMessage() []byte {
	msg := make([]byte, 5)
	binary.BigEndian.PutUint32(msg[0:4], 1)
	msg[4] = 2
	return msg
}

func buildRequestMessage(pieceIndex int, begin int, length int) []byte {
	msg := make([]byte, 17)

	binary.BigEndian.PutUint32(msg[0:4], 13)
	msg[4] = 6
	binary.BigEndian.PutUint32(msg[5:9], uint32(pieceIndex))
	binary.BigEndian.PutUint32(msg[9:13], uint32(begin))
	binary.BigEndian.PutUint32(msg[13:17], uint32(length))

	return msg
}

type PeerMessage struct {
	ID      byte
	Payload []byte
}

func readPeerMessage(r io.Reader) (PeerMessage, error) {
	lengthBuf := make([]byte, 4)
	if _, err := io.ReadFull(r, lengthBuf); err != nil {
		return PeerMessage{}, fmt.Errorf("read message length: %w", err)
	}

	length := binary.BigEndian.Uint32(lengthBuf)
	if length == 0 {
		// keep-alive message
		return PeerMessage{ID: 255}, nil
	}

	msgBuf := make([]byte, length)
	if _, err := io.ReadFull(r, msgBuf); err != nil {
		return PeerMessage{}, fmt.Errorf("read message payload: %w", err)
	}

	return PeerMessage{
		ID:      msgBuf[0],
		Payload: msgBuf[1:],
	}, nil
}

func parsePieceMessage(msg PeerMessage) (int, int, []byte, error) {
	if msg.ID != 7 {
		return 0, 0, nil, fmt.Errorf("expected piece message, got %d", msg.ID)
	}
	if len(msg.Payload) < 8 {
		return 0, 0, nil, fmt.Errorf("piece payload too short")
	}

	index := int(binary.BigEndian.Uint32(msg.Payload[0:4]))
	begin := int(binary.BigEndian.Uint32(msg.Payload[4:8]))
	block := msg.Payload[8:]

	return index, begin, block, nil
}

func pieceLengthForIndex(totalLength int, normalPieceLength int, pieceIndex int) (int, error) {
	if pieceIndex < 0 {
		return 0, fmt.Errorf("piece index must not be negative")
	}

	pieceStart := pieceIndex * normalPieceLength
	if pieceStart >= totalLength {
		return 0, fmt.Errorf("piece index out of range")
	}

	remaining := totalLength - pieceStart
	if remaining < normalPieceLength {
		return remaining, nil
	}

	return normalPieceLength, nil
}

type requestedBlock struct {
	begin  int
	length int
}

func pieceBlocks(pieceLength int) []requestedBlock {
	var blocks []requestedBlock

	for begin := 0; begin < pieceLength; begin += blockSize {
		length := blockSize
		if pieceLength-begin < length {
			length = pieceLength - begin
		}

		blocks = append(blocks, requestedBlock{
			begin:  begin,
			length: length,
		})
	}

	return blocks
}

func downloadPiece(ctx context.Context, peerAddress string, metadata torrentMetadata, pieceIndex int, peerID [20]byte) ([]byte, error) {
	// TODO: Check if the peer has the piece or not
	totalLength, err := torrentLength(metadata.Info)
	if err != nil {
		return nil, fmt.Errorf("parse torrent length: %w", err)
	}
	pieceLenFromMeta, err := torrentPieceLength(metadata.Info)
	if err != nil {
		return nil, fmt.Errorf("error while parsing the piece length: %w", err)
	}

	pieceLength, err := pieceLengthForIndex(totalLength, pieceLenFromMeta, pieceIndex)
	if err != nil {
		return nil, err
	}

	// 1. Connect to peer.
	// 2. Handshake.
	conn, _, err := connectToPeer(ctx, peerAddress, metadata.InfoHash, peerID)
	if err != nil {
		return nil, fmt.Errorf("error while connecting to the peer: %w", err)
	}
	defer conn.Close()

	err = conn.SetDeadline(time.Now().Add(15 * time.Second))
	if err != nil {
		return nil, fmt.Errorf("set peer deadline: %w", err)
	}

	// Wait for a bitfield message from the peer indicating which pieces it has
	bitfield, err := waitForBitfield(conn)
	if err != nil {
		return nil, fmt.Errorf("error while waiting for the bitfield message: %w", err)
	}
	if !bitfieldHasPiece(bitfield, pieceIndex) {
		return nil, fmt.Errorf("peer does not have piece %d", pieceIndex)
	}

	// Send interested.
	if err := conn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		return nil, fmt.Errorf("set peer deadline: %w", err)
	}
	interestedPayload := buildInterestedMessage()
	err = writeAll(conn, interestedPayload)
	if err != nil {
		return nil, fmt.Errorf("send interested: %w", err)
	}

	// 4. Wait for unchoke.
	if err := conn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		return nil, fmt.Errorf("set peer deadline: %w", err)
	}
	err = waitForUnchoke(conn)
	if err != nil {
		return nil, err
	}

	// 5. Request blocks for that piece.
	if err := conn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		return nil, fmt.Errorf("set peer deadline: %w", err)
	}
	err = requestPieceBlocks(conn, pieceIndex, pieceLength)
	if err != nil {
		return nil, err
	}
	// 6. Read piece messages.
	if err := conn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		return nil, fmt.Errorf("set peer deadline: %w", err)
	}
	pieceBytes, err := readPieceBlocks(conn, pieceIndex, pieceLength)
	if err != nil {
		return nil, err
	}
	// 7. Verify piece SHA-1 against info["pieces"].
	pieces, ok := metadata.Info["pieces"].(string)
	if !ok {
		return nil, fmt.Errorf("pieces must be a byte string")
	}
	hashStart := pieceIndex * 20
	hashEnd := hashStart + 20
	if hashStart < 0 || hashEnd > len(pieces) {
		return nil, fmt.Errorf("piece index out of range")
	}
	actualHash := sha1.Sum(pieceBytes)
	if !bytes.Equal(actualHash[:], []byte(pieces[hashStart:hashEnd])) {
		return nil, fmt.Errorf("piece hash mismatch")
	}
	return pieceBytes, nil
}

func writePieceFile(outputPath string, pieceBytes []byte) error {
	if err := os.WriteFile(outputPath, pieceBytes, 0644); err != nil {
		return fmt.Errorf("write piece file: %w", err)
	}

	return nil
}

func writePieceAt(outputPath string, pieceIndex int, normalPieceLength int, pieceBytes []byte) error {
	file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("open output file: %w", err)
	}
	defer file.Close()

	offset := int64(pieceIndex * normalPieceLength)
	if _, err := file.WriteAt(pieceBytes, offset); err != nil {
		return fmt.Errorf("write piece: %w", err)
	}

	return nil
}

func readPieceBlocks(conn net.Conn, pieceIndex int, pieceLength int) ([]byte, error) {
	piece := make([]byte, pieceLength)

	expected := make(map[int]int)
	for _, block := range pieceBlocks(pieceLength) {
		expected[block.begin] = block.length
	}

	for len(expected) > 0 {
		if err := conn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
			return nil, fmt.Errorf("set peer deadline: %w", err)
		}
		msg, err := readPeerMessage(conn)
		if err != nil {
			return nil, fmt.Errorf("read piece block: %w", err)
		}

		if msg.ID == 0 {
			return nil, fmt.Errorf("Got a choke response")
		}

		if msg.ID != 7 {
			continue
		}

		index, begin, block, err := parsePieceMessage(msg)
		if err != nil {
			return nil, fmt.Errorf("parse piece block: %w", err)
		}

		if index != pieceIndex {
			return nil, fmt.Errorf("received block for piece %d, want piece %d", index, pieceIndex)
		}

		expectedLength, ok := expected[begin]
		if !ok {
			return nil, fmt.Errorf("unexpected piece block begin %d", begin)
		}

		if len(block) != expectedLength {
			return nil, fmt.Errorf("piece block length mismatch")
		}

		copy(piece[begin:begin+len(block)], block)
		delete(expected, begin)
	}

	return piece, nil
}

func torrentPieceLength(info map[string]interface{}) (int, error) {
	pieceLength, ok := info["piece length"].(int)
	if !ok {
		return 0, fmt.Errorf("piece length must be an integer")
	}

	return pieceLength, nil
}

func waitForUnchoke(r io.Reader) error {
	for {
		msg, err := readPeerMessage(r)
		if err != nil {
			return fmt.Errorf("wait for unchoke: %w", err)
		}

		switch msg.ID {
		case 1:
			return nil // unchoke

		case 0:
			// choke; keep waiting or return an error depending on how strict you want to be
			continue

		case 255:
			// keep-alive
			continue

		default:
			// bitfield, have, etc. Ignore for now.
			continue
		}
	}
}

func waitForBitfield(r io.Reader) ([]byte, error) {
	for {
		msg, err := readPeerMessage(r)
		if err != nil {
			return nil, fmt.Errorf("wait for bitfield: %w", err)
		}

		switch msg.ID {
		case 5:
			return msg.Payload, nil

		case 255:
			// keep-alive
			continue

		default:
			// For now, ignore other messages while waiting.
			continue
		}
	}
}

func writeAll(w io.Writer, data []byte) error {
	total := 0
	for total < len(data) {
		n, err := w.Write(data[total:])
		if err != nil {
			return fmt.Errorf("write all: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("write all: wrote 0 bytes")
		}
		total += n
	}

	return nil
}

func connectToPeer(ctx context.Context, peerAddress string, infoHash [20]byte, peerID [20]byte) (net.Conn, [20]byte, error) {
	return connectToPeerWithHandshake(ctx, peerAddress, infoHash, buildHandshakePayload(infoHash, peerID))
}

func connectToPeerWithExtensions(ctx context.Context, peerAddress string, infoHash [20]byte, peerID [20]byte) (net.Conn, [20]byte, error) {
	handshake := buildHandshakePayload(infoHash, peerID)
	handshake[25] |= 0x10

	return connectToPeerWithHandshake(ctx, peerAddress, infoHash, handshake)
}

func connectToPeerWithHandshake(ctx context.Context, peerAddress string, infoHash [20]byte, handshake []byte) (net.Conn, [20]byte, error) {
	host, port, err := parsePeerAddress(peerAddress)
	if err != nil {
		return nil, [20]byte{}, err
	}

	address := net.JoinHostPort(host, strconv.Itoa(int(port)))
	dialer := net.Dialer{
		Timeout: 5 * time.Second,
	}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, [20]byte{}, fmt.Errorf("connect to peer: %w", err)
	}

	err = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err != nil {
		conn.Close()
		return nil, [20]byte{}, fmt.Errorf("set connection deadline: %w", err)
	}

	n, err := conn.Write(handshake)
	if err != nil {
		conn.Close()
		return nil, [20]byte{}, fmt.Errorf("send handshake: %w", err)
	}
	if n != len(handshake) {
		conn.Close()
		return nil, [20]byte{}, fmt.Errorf("send handshake: wrote %d bytes, want %d", n, len(handshake))
	}

	response := make([]byte, 68)
	if _, err := io.ReadFull(conn, response); err != nil {
		conn.Close()
		return nil, [20]byte{}, fmt.Errorf("read handshake response: %w", err)
	}

	if response[0] != 19 || string(response[1:20]) != "BitTorrent protocol" {
		conn.Close()
		return nil, [20]byte{}, fmt.Errorf("invalid handshake response")
	}

	if !bytes.Equal(infoHash[:], response[28:48]) {
		conn.Close()
		return nil, [20]byte{}, fmt.Errorf("handshake info hash mismatch")
	}

	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, [20]byte{}, fmt.Errorf("clear connection deadline: %w", err)
	}

	var receivedPeerID [20]byte
	copy(receivedPeerID[:], response[48:68])

	return conn, receivedPeerID, nil
}

func requestPieceBlocks(w io.Writer, pieceIndex int, pieceLength int) error {
	for begin := 0; begin < pieceLength; begin += blockSize {
		blockLength := blockSize
		if pieceLength-begin < blockLength {
			blockLength = pieceLength - begin
		}

		msg := buildRequestMessage(pieceIndex, begin, blockLength)

		if err := writeAll(w, msg); err != nil {
			return fmt.Errorf("send request message: %w", err)
		}
	}

	return nil
}

func parseDownloadPieceArgs(args []string) (string, string, int, error) {
	if len(args) < 2 {
		return "", "", 0, fmt.Errorf("expected command name")
	}

	flags := flag.NewFlagSet("download_piece", flag.ContinueOnError)

	outputPath := flags.String("o", "", "output file path")

	if err := flags.Parse(args[2:]); err != nil {
		return "", "", 0, fmt.Errorf("parse download_piece  flags: %w", err)
	}

	remaining := flags.Args()
	if *outputPath == "" {
		return "", "", 0, fmt.Errorf("output path is required")
	}
	if len(remaining) != 2 {
		return "", "", 0, fmt.Errorf("expected torrent path and piece index")
	}

	pieceIndex, err := strconv.Atoi(remaining[1])
	if err != nil {
		return "", "", 0, fmt.Errorf("parse piece index: %w", err)
	}

	return *outputPath, remaining[0], pieceIndex, nil
}
