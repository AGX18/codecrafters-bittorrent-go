package main

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
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
