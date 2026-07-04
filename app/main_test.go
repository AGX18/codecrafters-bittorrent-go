package main

import (
	"reflect"
	"testing"
)

func TestDecodeBencodedList(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []interface{}
		wantErr bool
	}{
		{
			name:  "empty list",
			input: "le",
			want:  []interface{}{},
		},
		{
			name:  "strings and integers",
			input: "l5:helloi52ee",
			want:  []interface{}{"hello", 52},
		},
		{
			name:  "nested list",
			input: "ll3:one3:twoei3ee",
			want:  []interface{}{[]interface{}{"one", "two"}, 3},
		},
		{
			name:    "missing terminator",
			input:   "l5:hello",
			wantErr: true,
		},
		{
			name:    "not a list",
			input:   "5:hello",
			wantErr: true,
		},
		{
			name:    "trailing data",
			input:   "leextra",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeBencodedList(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("decodeBencodedList() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("decodeBencodedList() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("decodeBencodedList() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
