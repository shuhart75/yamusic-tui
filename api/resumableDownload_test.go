package api

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDownloadRecoversWithoutDuplicatingBytes(t *testing.T) {
	for _, mode := range []string{"range", "ignore-range", "bad-range", "changed"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				call := calls.Add(1)
				if call == 1 {
					conn, _, _ := w.(http.Hijacker).Hijack()
					fmt.Fprint(conn, "HTTP/1.1 200 OK\r\nContent-Length: 10\r\nETag: \"track\"\r\n\r\nabc")
					conn.Close()
					return
				}
				if req.Header.Get("Range") != "bytes=3-" || req.Header.Get("If-Range") != "\"track\"" {
					t.Errorf("unexpected resume headers: %v", req.Header)
				}
				w.Header().Set("ETag", "\"track\"")
				if mode == "ignore-range" {
					fmt.Fprint(w, "abcdefghij")
					return
				}
				if mode == "changed" {
					w.Header().Set("ETag", "\"other\"")
				}
				w.Header().Set("Content-Length", "7")
				w.Header().Set("Content-Range", "bytes 3-9/10")
				if mode == "bad-range" {
					w.Header().Set("Content-Range", "bytes 2-8/10")
				}
				w.WriteHeader(http.StatusPartialContent)
				fmt.Fprint(w, "defghij")
			}))
			defer server.Close()
			r, size, err := newResumableDownload(server.Client(), "", server.URL, "audio/mpeg", time.Second, time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			got, err := io.ReadAll(r)
			if mode == "bad-range" || mode == "changed" {
				if err == nil || string(got) != "abc" || calls.Load() != 5 {
					t.Fatalf("got %q, err %v, calls %d", got, err, calls.Load())
				}
			} else if err != nil || string(got) != "abcdefghij" || size != 10 || calls.Load() != 2 {
				t.Fatalf("got %q, size %d, err %v, calls %d", got, size, err, calls.Load())
			}
		})
	}
}

func TestDownloadIdleBetweenReadsDoesNotCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "10")
		fmt.Fprint(w, "abcdefghij")
	}))
	defer server.Close()
	r, _, err := newResumableDownload(server.Client(), "", server.URL, "audio/mpeg", 20*time.Millisecond, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	time.Sleep(60 * time.Millisecond)
	got, err := io.ReadAll(r)
	if err != nil || string(got) != "abcdefghij" || r.retries != 0 {
		t.Fatalf("got %q, err %v", got, err)
	}
}

func TestDownloadTimeoutAndClose(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(fmt.Sprint(cancel), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Length", "10")
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				<-req.Context().Done()
			}))
			defer server.Close()
			r, _, err := newResumableDownload(server.Client(), "", server.URL, "audio/mpeg", 20*time.Millisecond, time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			done := make(chan error, 1)
			go func() { _, err := io.ReadAll(r); done <- err }()
			if cancel {
				r.Close()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("expected error")
				}
				if !cancel && (!strings.Contains(err.Error(), "4 retries") || calls.Load() != 5) {
					t.Fatalf("%v; calls %d", err, calls.Load())
				}
			case <-time.After(3 * time.Second):
				t.Fatal("read did not terminate")
			}
		})
	}
}

func TestMetadataAllowsChunkedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		fmt.Fprint(w, `{"host":"example.test"}`)
	}))
	defer server.Close()
	r, size, err := metadataDownloadRequest("", server.URL, "application/json")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil || size != -1 || string(got) != `{"host":"example.test"}` {
		t.Fatalf("got %q size %d err %v", got, size, err)
	}
}
