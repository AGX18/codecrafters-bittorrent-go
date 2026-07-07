package main

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
	// bencode "github.com/jackpal/bencode-go" // Available if you need it!
)

// Ensures gofmt doesn't remove the "os" encoding/json import (feel free to remove this!)
var _ = json.Marshal

// Example:
// - 5:hello -> hello
// - 10:hello12345 -> hello12345
func decodeBencode(bencodedString string) (interface{}, error) {
	value, consumed, err := decodeBencodeValue(bencodedString)

	if err != nil {
		return nil, err
	}

	if consumed != len(bencodedString) {
		return nil, fmt.Errorf("unexpected trailing data")
	}

	return value, nil
}

func decodeBencodedList(bencodedString string) ([]interface{}, error) {
	value, consumed, err := decodeBencodedListValue(bencodedString)
	if err != nil {
		return nil, err
	}
	if consumed != len(bencodedString) {
		return nil, fmt.Errorf("unexpected trailing data")
	}

	return value, nil
}

func decodeBencodeValue(bencodedString string) (interface{}, int, error) {
	if len(bencodedString) == 0 {
		return nil, 0, fmt.Errorf("empty bencoded value")
	}

	switch {
	case bencodedString[0] >= '0' && bencodedString[0] <= '9':
		// decoding a string
		colonIndex := -1
		for i := 0; i < len(bencodedString); i++ {
			if bencodedString[i] == ':' {
				colonIndex = i
				break
			}
		}
		if colonIndex == -1 {
			return nil, 0, fmt.Errorf("invalid bencoded string")
		}
		length, err := strconv.Atoi(bencodedString[:colonIndex])
		if err != nil {
			return nil, 0, fmt.Errorf("decode string length: %w", err)
		}
		end := colonIndex + 1 + length
		if end > len(bencodedString) {
			return nil, 0, fmt.Errorf("bencoded string exceeds input")
		}
		return bencodedString[colonIndex+1 : end], end, nil

	case bencodedString[0] == 'i':
		// decode integer
		endIndex := -1
		for i := 1; i < len(bencodedString); i++ {
			if bencodedString[i] == 'e' {
				endIndex = i
				break
			}
		}
		if endIndex == -1 {
			return nil, 0, fmt.Errorf("unterminated bencoded integer")
		}
		val, err := strconv.Atoi(bencodedString[1:endIndex])
		if err != nil {
			return nil, 0, fmt.Errorf("decode integer: %w", err)
		}
		return val, endIndex + 1, nil

	case bencodedString[0] == 'l':
		return decodeBencodedListValue(bencodedString)
	case bencodedString[0] == 'd':
		return decodeBencodedDictValue(bencodedString)
	default:
		return nil, 0, fmt.Errorf("unsupported bencoded value")
	}
}

func decodeBencodedDictValue(bencodedString string) (map[string]interface{}, int, error) {
	if len(bencodedString) == 0 || bencodedString[0] != 'd' {
		return nil, 0, fmt.Errorf("invalid bencoded dict")
	}

	values := make(map[string]interface{})
	offset := 1
	for {
		if offset >= len(bencodedString) {
			return nil, 0, fmt.Errorf("unterminated bencoded dict")
		}
		if bencodedString[offset] == 'e' {
			return values, offset + 1, nil
		}

		decodedKey, consumed, err := decodeBencodeValue(bencodedString[offset:])
		if err != nil {
			return nil, 0, fmt.Errorf("decode dict item: %w", err)
		}
		key, ok := decodedKey.(string)
		if !ok {
			return nil, 0, fmt.Errorf("dictionary key must be a string")
		}
		offset += consumed

		value, consumed, err := decodeBencodeValue(bencodedString[offset:])
		if err != nil {
			return nil, 0, fmt.Errorf("decode dictionary value: %w", err)
		}
		offset += consumed

		values[key] = value
	}

}

func extractRawInfo(bencodedString string) (string, error) {
	if len(bencodedString) == 0 || bencodedString[0] != 'd' {
		return "", fmt.Errorf("invalid bencoded dict")
	}

	offset := 1
	for {
		if offset >= len(bencodedString) {
			return "", fmt.Errorf("unterminated bencoded dict")
		}
		if bencodedString[offset] == 'e' {
			return "", fmt.Errorf("couldn't find the info field")
		}

		decodedKey, consumed, err := decodeBencodeValue(bencodedString[offset:])
		if err != nil {
			return "", fmt.Errorf("decode dict item: %w", err)
		}
		key, ok := decodedKey.(string)
		if !ok {
			return "", fmt.Errorf("dictionary key must be a string")
		}
		offset += consumed

		valueStart := offset
		_, consumed, err = decodeBencodeValue(bencodedString[offset:])
		if err != nil {
			return "", err
		}
		valueEnd := valueStart + consumed

		if key == "info" {
			return bencodedString[valueStart:valueEnd], nil
		}

		offset = valueEnd

	}
}

func decodeBencodedListValue(bencodedString string) ([]interface{}, int, error) {
	if len(bencodedString) == 0 || bencodedString[0] != 'l' {
		return nil, 0, fmt.Errorf("invalid bencoded list")
	}

	values := make([]interface{}, 0) // this where will store the list items
	offset := 1
	for {
		if offset >= len(bencodedString) {
			return nil, 0, fmt.Errorf("unterminated bencoded list")
		}
		if bencodedString[offset] == 'e' {
			return values, offset + 1, nil
		}
		value, consumed, err := decodeBencodeValue(bencodedString[offset:])
		if err != nil {
			return nil, 0, fmt.Errorf("decode list item: %w", err)
		}
		values = append(values, value)
		offset += consumed
	}

}

func calculateInfoHash(torrentData []byte) ([20]byte, error) {
	infoBytes, err := extractRawInfo(string(torrentData))
	if err != nil {
		return [20]byte{}, err
	}

	return sha1.Sum([]byte(infoBytes)), nil
}

type torrentMetadata struct {
	Info        map[string]interface{}
	Announce    string
	InfoHash    [20]byte
	PieceLength int
}

func loadTorrentMetadata(path string) (torrentMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return torrentMetadata{}, fmt.Errorf("read torrent file: %w", err)
	}

	decoded, err := decodeBencode(string(data))
	if err != nil {
		return torrentMetadata{}, fmt.Errorf("decode torrent metadata: %w", err)
	}

	torrent, ok := decoded.(map[string]interface{})
	if !ok {
		return torrentMetadata{}, fmt.Errorf("torrent metadata must be a dictionary")
	}

	announce, ok := torrent["announce"].(string)
	if !ok {
		return torrentMetadata{}, fmt.Errorf("tracker url must be a string")
	}

	info, ok := torrent["info"].(map[string]interface{})
	if !ok {
		return torrentMetadata{}, fmt.Errorf("torrent info must be a dictionary")
	}

	pieceLength, ok := info["piece length"].(int)
	if !ok {
		return torrentMetadata{}, fmt.Errorf("piece length must be an integer")
	}

	hash, err := calculateInfoHash(data)
	if err != nil {
		return torrentMetadata{}, fmt.Errorf("hash info section: %w", err)
	}

	return torrentMetadata{
		Info:        info,
		Announce:    announce,
		InfoHash:    hash,
		PieceLength: pieceLength,
	}, nil
}

func torrentLength(info map[string]interface{}) (int, error) {
	length, ok := info["length"].(int)
	if !ok {
		return 0, fmt.Errorf("torrent length must be an integer")
	}

	return length, nil
}

func decodeTrackerResponse(responseData []byte) (map[string]interface{}, error) {
	decoded, err := decodeBencode(string(responseData))
	if err != nil {
		return nil, fmt.Errorf("decode tracker response: %w", err)
	}

	trackerResponse, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("tracker response must be a dictionary")
	}

	return trackerResponse, nil
}

func parseCompactPeers(peers string) ([]string, error) {
	if len(peers)%6 != 0 {
		return nil, fmt.Errorf("compact peer data has invalid length")
	}

	addresses := make([]string, 0, len(peers)/6)
	for i := 0; i < len(peers); i += 6 {
		ip := net.IP([]byte(peers[i : i+4]))
		port := binary.BigEndian.Uint16([]byte(peers[i+4 : i+6]))

		addresses = append(addresses, fmt.Sprintf("%s:%d", ip, port))
	}

	return addresses, nil
}

func generatePeerID() ([20]byte, error) {
	const prefix = "-BT0001-"

	var peerID [20]byte
	copy(peerID[:], prefix)
	if _, err := rand.Read(peerID[len(prefix):]); err != nil {
		return [20]byte{}, fmt.Errorf("generate peer id: %w", err)
	}

	return peerID, nil
}

func handleDecodeCommand(bencodedValue string) {
	decoded, err := decodeBencode(bencodedValue)
	if err != nil {
		fmt.Println(err)
		return
	}

	jsonOutput, _ := json.Marshal(decoded)
	fmt.Println(string(jsonOutput))
}

func handleInfoCommand(torrentPath string) {
	metadata, err := loadTorrentMetadata(torrentPath)
	if err != nil {
		fmt.Printf("error while loading torrent metadata: %v", err)
		os.Exit(1)
	}

	pieces, ok := metadata.Info["pieces"].(string)
	if !ok {
		fmt.Println("piece length must be a integer")
		os.Exit(1)
	}

	if len(pieces)%20 != 0 {
		fmt.Printf("malformed pieces hashes: not 20 bytes each")
		os.Exit(1)
	}

	fmt.Printf("Tracker URL: %s\n", metadata.Announce)
	fmt.Printf("Length: %d\n", metadata.Info["length"])
	fmt.Printf("Info Hash: %x\n", metadata.InfoHash)
	fmt.Printf("Piece Length: %d\n", metadata.PieceLength)
	fmt.Println("Piece Hashes:")
	count := len(pieces) / 20
	for i := range count {
		fmt.Printf("%x\n", pieces[20*i:20*i+20])
	}
}

func handlePeersCommand(metadata torrentMetadata, peerID [20]byte) ([]string, error) {
	length, err := torrentLength(metadata.Info)
	if err != nil {
		return nil, fmt.Errorf("error parsing piecelength: %w", err)
	}

	trackerRequest := TrackerRequest{
		AnnounceURL: metadata.Announce,
		InfoHash:    metadata.InfoHash,
		PeerID:      peerID,
		Port:        6881,
		Uploaded:    0,
		Downloaded:  0,
		Left:        int64(length),
		Compact:     true,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client := &http.Client{
		Timeout: 20 * time.Second,
	}

	responseData, err := requestPeers(ctx, client, trackerRequest)
	if err != nil {
		return nil, fmt.Errorf("error while sending the request: %w", err)
	}

	trackerResponse, err := decodeTrackerResponse(responseData)
	if err != nil {
		return nil, fmt.Errorf("error while decoding the tracker response: %w", err)
	}
	peers, ok := trackerResponse["peers"].(string)
	if !ok {
		return nil, fmt.Errorf("tracker peers must be a byte string")
	}

	peerAddresses, err := parseCompactPeers(peers)
	if err != nil {
		return nil, fmt.Errorf("error while parsing the peer addresses: %w", err)

	}
	return peerAddresses, nil
}

func handleHandshakeCommand(torrentPath string, peerAddress string, peerID [20]byte) {
	// get info
	metadata, err := loadTorrentMetadata(torrentPath)
	if err != nil {
		fmt.Printf("error while loading torrent metadata: %v", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, receivedPeerID, err := connectToPeer(ctx, peerAddress, metadata.InfoHash, peerID)
	if err != nil {
		fmt.Printf("connect to peer: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	fmt.Print("Peer ID: ")
	for _, b := range receivedPeerID {
		fmt.Printf("%02x", b)
	}
	fmt.Println()
}

func main() {
	// You can use print statements as follows for debugging, they'll be visible when running tests.
	fmt.Fprintln(os.Stderr, "Logs from your program will appear here!")

	if len(os.Args) < 2 {
		fmt.Println("missing command")
		os.Exit(1)
	}
	peerID, err := generatePeerID()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "decode":
		if len(os.Args) < 3 {
			fmt.Println("not enough arguments: decode <bencoded_value>")
			os.Exit(1)
		}
		handleDecodeCommand(os.Args[2])
	case "info":
		if len(os.Args) < 3 {
			fmt.Println("not enough arguments, you must provide the path of the .torrent file")
			os.Exit(1)
		}
		handleInfoCommand(os.Args[2])
	case "peers":
		if len(os.Args) < 3 {
			fmt.Println("not enough arguments, you must provide the path of the .torrent file")
			os.Exit(1)
		}
		metadata, err := loadTorrentMetadata(os.Args[2])
		if err != nil {
			fmt.Printf("error while loading torrent metadata: %v", err)
			os.Exit(1)
		}

		peerAddresses, err := handlePeersCommand(metadata, peerID)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		for _, peerAddress := range peerAddresses {
			fmt.Println(peerAddress)
		}
	case "handshake":
		if len(os.Args) < 4 {
			fmt.Println("not enough arguments: handshake sample.torrent <peer_ip>:<peer_port>")
			os.Exit(1)
		}

		handleHandshakeCommand(os.Args[2], os.Args[3], peerID)
	case "download_piece":
		if len(os.Args) < 5 {
			fmt.Println("not enough arguments: download-piece -o /tmp/test-piece sample.torrent <piece_index>")
			os.Exit(1)
		}
		outputPath, torrentFile, piece_index, err := parseDownloadPieceArgs(os.Args)
		if err != nil {
			fmt.Println("error while parsing arguments: " + err.Error())
			os.Exit(1)
		}
		metadata, err := loadTorrentMetadata(torrentFile)
		if err != nil {
			fmt.Printf("error while loading torrent metadata: %v", err)
			os.Exit(1)
		}

		peerAddresses, err := handlePeersCommand(metadata, peerID)
		if err != nil {
			fmt.Printf("error while getting the peer addresses: %v", err)
			os.Exit(1)

		}
		ctx := context.Background()
		isSuccessful := false
		for _, peerAddress := range peerAddresses {
			fmt.Printf("trying peer: %s", peerAddress)
			pieceBytes, err := downloadPiece(ctx, peerAddress, metadata, piece_index, peerID)
			if err != nil {
				fmt.Println("error occured while getting the piece from a peer: " + err.Error())
				fmt.Println("will try another one if there's any")
			} else {
				if err := writePieceFile(outputPath, pieceBytes); err != nil {
					fmt.Println(err)
					os.Exit(1)
				}
				isSuccessful = true
				break
			}
		}

		if isSuccessful == false {
			fmt.Printf("downloading the piece: %d failed\n", piece_index)
			os.Exit(1)
		}

	case "download":
		if len(os.Args) < 4 {
			fmt.Println("not enough arguments: download -o /tmp/test.txt sample.torrent")
			os.Exit(1)
		}

		outputPath, torrentFile, err := parseDownloadArgs(os.Args)
		if err != nil {
			fmt.Println("error while parsing arguments: " + err.Error())
			os.Exit(1)
		}
		metadata, err := loadTorrentMetadata(torrentFile)
		if err != nil {
			fmt.Printf("error while loading torrent metadata: %v", err)
			os.Exit(1)
		}

		peerAddresses, err := handlePeersCommand(metadata, peerID)
		if err != nil {
			fmt.Printf("error while getting the peer addresses: %v", err)
			os.Exit(1)

		}

		ctx := context.Background()

		indexes, err := pieceIndexes(metadata)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		for _, pieceIndex := range indexes {
			isSuccessful := false

			for _, peerAddress := range peerAddresses {
				fmt.Printf("trying peer: %s for piece index (%d)", peerAddress, pieceIndex)

				pieceBytes, err := downloadPiece(ctx, peerAddress, metadata, pieceIndex, peerID)
				if err != nil {
					fmt.Println("error occured while getting the piece from a peer: " + err.Error())
					fmt.Println("will try another one if there's any")
				} else {
					if err := writePieceAt(outputPath, pieceIndex, metadata.PieceLength, pieceBytes); err != nil {
						fmt.Println(err)
						os.Exit(1)
					}
					isSuccessful = true
					break
				}
			}

			if isSuccessful == false {
				fmt.Printf("downloading the piece: %d failed\n", pieceIndex)
			}
		}
	case "magnet_parse":
		magnet, err := parseMagnetLink(os.Args[2])
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		fmt.Println("Tracker URL:", magnet.Trackers[0])
		fmt.Printf("Info Hash: %x\n", magnet.InfoHash)
	default:
		fmt.Println("Unknown command: " + command)
		os.Exit(1)
	}
}

func parsePeerAddress(address string) (string, uint16, error) {
	host, portString, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, fmt.Errorf("invalid peer address: %w", err)
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return "", 0, fmt.Errorf("peer host must be an ip address")
	}

	port, err := strconv.ParseUint(portString, 10, 16)
	if err != nil {
		return "", 0, fmt.Errorf("peer port must be between 0 and 65535: %w", err)
	}

	return ip.String(), uint16(port), nil
}

func buildHandshakePayload(infoHash [20]byte, peerID [20]byte) []byte {
	payload := make([]byte, 68)
	// The handshake is a message consisting of the following parts as described in the peer protocol:
	protocol := "BitTorrent protocol"

	// length of the protocol string (BitTorrent protocol) which is 19 (1 byte)
	payload[0] = byte(len(protocol))

	// the string BitTorrent protocol (19 bytes)
	copy(payload[1:], protocol)

	// eight reserved bytes, which are all set to zero (8 bytes)
	// payload[20:28] are reserved bytes.
	// They are already zero because make() zeroes byte slices.

	// sha1 infohash (20 bytes) (NOT the hexadecimal representation, which is 40 bytes long)
	copy(payload[28:48], infoHash[:])

	// peer id (20 bytes)
	copy(payload[48:68], peerID[:])

	return payload
}
