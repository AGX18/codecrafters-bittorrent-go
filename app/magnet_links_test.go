package main

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestBuildExtensionHandshakeMessage(t *testing.T) {
	tests := []struct {
		name        string
		wantLength  uint32
		wantPayload []byte
	}{
		{
			name:        "advertises ut_metadata",
			wantLength:  27,
			wantPayload: []byte("d1:md11:ut_metadatai16eee"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message := buildExtensionHandshakeMessage()
			if len(message) < 6 {
				t.Fatalf("buildExtensionHandshakeMessage() length = %d, want at least 6", len(message))
			}
			if got := binary.BigEndian.Uint32(message[0:4]); got != tt.wantLength {
				t.Errorf("message length = %d, want %d", got, tt.wantLength)
			}
			if got := message[4]; got != extendedMessageID {
				t.Errorf("message id = %d, want %d", got, extendedMessageID)
			}
			if got := message[5]; got != extensionHandshakeID {
				t.Errorf("extension message id = %d, want %d", got, extensionHandshakeID)
			}
			if got := message[6:]; !bytes.Equal(got, tt.wantPayload) {
				t.Errorf("payload = %q, want %q", got, tt.wantPayload)
			}
		})
	}
}

func TestParseMetadataExtensionID(t *testing.T) {
	tests := []struct {
		name    string
		message PeerMessage
		want    int
		wantErr bool
	}{
		{
			name: "valid extension handshake",
			message: PeerMessage{
				ID:      extendedMessageID,
				Payload: append([]byte{extensionHandshakeID}, []byte("d1:md11:ut_metadatai3eee")...),
			},
			want: 3,
		},
		{
			name: "wrong peer message id",
			message: PeerMessage{
				ID:      5,
				Payload: []byte{extensionHandshakeID},
			},
			wantErr: true,
		},
		{
			name: "payload too short",
			message: PeerMessage{
				ID:      extendedMessageID,
				Payload: []byte{extensionHandshakeID},
			},
			wantErr: true,
		},
		{
			name: "wrong extension message id",
			message: PeerMessage{
				ID:      extendedMessageID,
				Payload: append([]byte{1}, []byte("de")...),
			},
			wantErr: true,
		},
		{
			name: "missing extension mapping",
			message: PeerMessage{
				ID:      extendedMessageID,
				Payload: append([]byte{extensionHandshakeID}, []byte("de")...),
			},
			wantErr: true,
		},
		{
			name: "missing ut_metadata",
			message: PeerMessage{
				ID:      extendedMessageID,
				Payload: append([]byte{extensionHandshakeID}, []byte("d1:mdee")...),
			},
			wantErr: true,
		},
		{
			name: "disabled ut_metadata",
			message: PeerMessage{
				ID:      extendedMessageID,
				Payload: append([]byte{extensionHandshakeID}, []byte("d1:md11:ut_metadatai0eee")...),
			},
			wantErr: true,
		},
		{
			name: "extension id exceeds one byte",
			message: PeerMessage{
				ID:      extendedMessageID,
				Payload: append([]byte{extensionHandshakeID}, []byte("d1:md11:ut_metadatai256eee")...),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseMetadataExtensionID(tt.message)
			if tt.wantErr {
				if err == nil {
					t.Fatal("parseMetadataExtensionID() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMetadataExtensionID() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("parseMetadataExtensionID() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestReadMetadataExtensionID(t *testing.T) {
	extensionPayload := append([]byte{extensionHandshakeID}, []byte("d1:md11:ut_metadatai7eee")...)
	extensionMessage := encodePeerMessageForTest(extendedMessageID, extensionPayload)
	bitfieldMessage := encodePeerMessageForTest(5, []byte{0xff})

	tests := []struct {
		name    string
		input   []byte
		want    int
		wantErr bool
	}{
		{
			name:  "reads extension handshake",
			input: extensionMessage,
			want:  7,
		},
		{
			name:  "ignores preceding peer messages",
			input: append(append([]byte{0, 0, 0, 0}, bitfieldMessage...), extensionMessage...),
			want:  7,
		},
		{
			name:    "truncated message",
			input:   []byte{0, 0, 0, 2, extendedMessageID},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readMetadataExtensionID(bytes.NewReader(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Fatal("readMetadataExtensionID() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("readMetadataExtensionID() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("readMetadataExtensionID() = %d, want %d", got, tt.want)
			}
		})
	}
}

func encodePeerMessageForTest(id byte, payload []byte) []byte {
	message := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(message[0:4], uint32(1+len(payload)))
	message[4] = id
	copy(message[5:], payload)
	return message
}
