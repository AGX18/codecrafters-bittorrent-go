package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

type TrackerRequest struct {
	AnnounceURL string
	InfoHash    [20]byte
	PeerID      [20]byte
	Port        uint16
	Uploaded    int64
	Downloaded  int64
	Left        int64
	Compact     bool
}

func requestPeers(ctx context.Context, client *http.Client, tr TrackerRequest) ([]byte, error) {
	trackerURL, err := url.Parse(tr.AnnounceURL)
	if err != nil {
		return nil, err
	}

	query := trackerURL.Query()
	query.Set("info_hash", string(tr.InfoHash[:]))
	query.Set("peer_id", string(tr.PeerID[:]))
	query.Set("port", strconv.FormatUint(uint64(tr.Port), 10))
	query.Set("uploaded", strconv.FormatInt(tr.Uploaded, 10))
	query.Set("downloaded", strconv.FormatInt(tr.Downloaded, 10))
	query.Set("left", strconv.FormatInt(tr.Left, 10))
	if tr.Compact {
		query.Set("compact", "1")
	} else {
		query.Set("compact", "0")
	}
	trackerURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		trackerURL.String(),
		nil,
	)

	if err != nil {
		return nil, fmt.Errorf("create tracker request: %w", err)
	}

	response, err := client.Do(req)

	if err != nil {
		return nil, fmt.Errorf("request tracker: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tracker returned status %s", response.Status)
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read tracker response: %w", err)
	}

	return body, nil
}
