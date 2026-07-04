package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
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
			return nil, 0, fmt.Errorf("decode dect item: %w", err)
		}
		key, ok := decodedKey.(string)
		if !ok {
			return nil, 0, fmt.Errorf("decode dictionary key: %w", err)
		}
		offset += consumed

		value, consumed, err := decodeBencodeValue(bencodedString[offset:])
		if err != nil {
			return nil, 0, fmt.Errorf("decode dictionary key: %w", err)
		}
		offset += consumed

		values[key] = value
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

		fmt.Printf("Tracker URL: %s", announce)
		fmt.Printf("Length: %d", info["length"])

	default:
		fmt.Println("Unknown command: " + command)
		os.Exit(1)
	}
}
