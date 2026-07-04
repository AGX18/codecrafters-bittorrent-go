package main

import (
	"context"
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
			return "", fmt.Errorf("couldn't the info field")
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

func main() {
	// You can use print statements as follows for debugging, they'll be visible when running tests.
	fmt.Fprintln(os.Stderr, "Logs from your program will appear here!")

	command := os.Args[1]

	switch command {
	case "decode":
		bencodedValue := os.Args[2]

		decoded, err := decodeBencode(bencodedValue)
		if err != nil {
			fmt.Println(err)
			return
		}

		jsonOutput, _ := json.Marshal(decoded)
		fmt.Println(string(jsonOutput))
	case "info":
		if len(os.Args) <= 2 {
			fmt.Println("not enough arguments, you must provide the path of the .torrent file")
			os.Exit(1)
		}
		data, err := os.ReadFile(os.Args[2])
		if err != nil {
			fmt.Printf("error while opening the file %v", err)
			os.Exit(1)
		}
		decoded, err := decodeBencode(string(data))
		if err != nil {
			fmt.Printf("torrent metadata could not be decoded: %v", err)
		}
		torrent, ok := decoded.(map[string]interface{})
		if !ok {
			fmt.Println("torrent metadata must be a dictionary")
			os.Exit(1)
		}

		info, ok := torrent["info"].(map[string]interface{})
		if !ok {
			fmt.Println("torrent info must be a dictionary")
			os.Exit(1)
		}

		announce, ok := torrent["announce"].(string)
		if !ok {
			fmt.Println("Tracker URL must be a string")
			os.Exit(1)
		}
		pieceLength, ok := info["piece length"].(int)
		if !ok {
			fmt.Println("piece length must be a integer")
			fmt.Printf("Piece length: %v", pieceLength)
			os.Exit(1)
		}
		pieces, ok := info["pieces"].(string)
		if !ok {
			fmt.Println("piece length must be a integer")
			os.Exit(1)
		}
		hash, err := calculateInfoHash(data)
		if err != nil {
			fmt.Printf("error while hashing the info section: %v", err)
			os.Exit(1)
		}

		if len(pieces)%20 != 0 {
			fmt.Printf("malformed pieces hashes: not 20 bytes each")
			os.Exit(1)
		}

		fmt.Printf("Tracker URL: %s\n", announce)
		fmt.Printf("Length: %d\n", info["length"])
		fmt.Printf("Info Hash: %x\n", hash)
		fmt.Printf("Piece Length: %d\n", pieceLength)
		fmt.Println("Piece Hashes:")
		count := len(pieces) / 20
		for i := range count {
			fmt.Printf("%x\n", pieces[20*i:20*i+20])
		}
	case "peers":
		if len(os.Args) <= 2 {
			fmt.Println("not enough arguments, you must provide the path of the .torrent file")
			os.Exit(1)
		}
		data, err := os.ReadFile(os.Args[2])
		if err != nil {
			fmt.Printf("error while opening the file %v", err)
			os.Exit(1)
		}
		decoded, err := decodeBencode(string(data))
		if err != nil {
			fmt.Printf("torrent metadata could not be decoded: %v", err)
			os.Exit(1)
		}
		torrent, ok := decoded.(map[string]interface{})
		if !ok {
			fmt.Println("torrent metadata must be a dictionary")
			os.Exit(1)
		}

		hash, err := calculateInfoHash(data)
		if err != nil {
			fmt.Printf("error while hashing the info section: %v", err)
			os.Exit(1)
		}

		peerID := []byte("-BT0001-123456789012") // exactly 20 bytes

		announce, ok := torrent["announce"].(string)
		if !ok {
			fmt.Println("Tracker URL must be a string")
			os.Exit(1)
		}

		info, ok := torrent["info"].(map[string]interface{})
		if !ok {
			fmt.Println("torrent info must be a dictionary")
			os.Exit(1)
		}

		length, ok := info["length"].(int)
		if !ok {
			fmt.Println("error while parsing length")
			os.Exit(1)
		}

		trackerRequest := TrackerRequest{
			AnnounceURL: announce,
			InfoHash:    hash,
			PeerID:      [20]byte(peerID),
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
			fmt.Printf("error while sending the request: %v", err)
			os.Exit(1)
		}

		decoded, err = decodeBencode(string(responseData))
		if err != nil {
			fmt.Printf("error while decoding the response: %v", err)
			os.Exit(1)
		}
		trackerResponse, ok := decoded.(map[string]interface{})
		if !ok {
			fmt.Println("error while decoding the response")
			os.Exit(1)
		}
		peers, ok := trackerResponse["peers"].(string)
		if !ok {
			fmt.Println("tracker peers must be a byte string")
			os.Exit(1)
		}

		if len(peers)%6 != 0 {
			fmt.Println("compact peer data has invalid length")
			os.Exit(1)
		}
		for i := 0; i < len(peers); i += 6 {
			ip := net.IP([]byte(peers[i : i+4]))
			port := binary.BigEndian.Uint16([]byte(peers[i+4 : i+6]))

			fmt.Printf("%s:%d\n", ip, port)
		}

	default:
		fmt.Println("Unknown command: " + command)
		os.Exit(1)
	}
}
