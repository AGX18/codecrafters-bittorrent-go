package main

import (
	"flag"
	"fmt"
)

func pieceIndexes(metadata torrentMetadata) ([]int, error) {
	pieces, ok := metadata.Info["pieces"].(string)
	if !ok {
		return nil, fmt.Errorf("pieces must be a byte string")
	}
	if len(pieces)%20 != 0 {
		return nil, fmt.Errorf("malformed pieces hashes")
	}

	count := len(pieces) / 20
	indexes := make([]int, 0, count)

	for i := 0; i < count; i++ {
		indexes = append(indexes, i)
	}

	return indexes, nil
}

func parseDownloadArgs(args []string) (string, string, error) {
	flags := flag.NewFlagSet("download", flag.ContinueOnError)

	outputPath := flags.String("o", "", "output file path")

	if err := flags.Parse(args[2:]); err != nil {
		return "", "", fmt.Errorf("parse download flags: %w", err)
	}

	remaining := flags.Args()
	if *outputPath == "" {
		return "", "", fmt.Errorf("output path is required")
	}
	if len(remaining) != 1 {
		return "", "", fmt.Errorf("expected torrent path")
	}

	return *outputPath, remaining[0], nil
}

func bitfieldHasPiece(bitfield []byte, pieceIndex int) bool {
	byteIndex := pieceIndex / 8
	bitIndex := 7 - (pieceIndex % 8)

	if byteIndex >= len(bitfield) {
		return false
	}

	return bitfield[byteIndex]&(1<<bitIndex) != 0
}
