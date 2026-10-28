package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestPlaylistResponseValidation(t *testing.T) {
	old := httpClient
	defer func() { httpClient = old }()
	for _, tc := range []struct {
		name, body string
		fail       bool
	}{
		{"empty", `{"result":[{"trackCount":0,"tracks":[]}]}`, false},
		{"truncated-json", `{"result":`, true},
		{"missing-tracks", `{"result":[{"trackCount":2,"tracks":[]}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			httpClient = http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if _, ok := r.Context().Deadline(); !ok {
					t.Error("metadata request has no deadline")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
			})}
			c := &YaMusicClient{}
			_, err := c.PlaylistTracks(1000, 1, false)
			if (err != nil) != tc.fail {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestLabelIDFormats(t *testing.T) {
	for _, raw := range []string{`{"id":9007199254740993,"name":"label"}`, `{"id":"9007199254740993","name":"label"}`} {
		var label Label
		if err := json.Unmarshal([]byte(raw), &label); err != nil {
			t.Fatal(err)
		}
		if label.Id != "9007199254740993" {
			t.Fatalf("id=%s", label.Id)
		}
	}
	var label Label
	if err := json.Unmarshal([]byte(`{"id":true}`), &label); err == nil {
		t.Fatal("boolean ID accepted")
	}
}
