package stream

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type stalledSource struct {
	first            bool
	entered, release chan struct{}
	once             sync.Once
}

func (s *stalledSource) Read(p []byte) (int, error) {
	if !s.first {
		s.first = true
		return copy(p, "cached"), nil
	}
	close(s.entered)
	<-s.release
	return 0, io.EOF
}
func (s *stalledSource) Close() error { s.once.Do(func() { close(s.release) }); return nil }
func TestCachedReadAndCloseDuringNetworkStall(t *testing.T) {
	s := &stalledSource{entered: make(chan struct{}), release: make(chan struct{})}
	h := NewBufferedStream(s, 100)
	defer h.Close()
	<-s.entered
	result := make(chan string, 1)
	go func() {
		b := make([]byte, 6)
		n, err := h.Read(b)
		if err != nil {
			result <- err.Error()
		} else {
			result <- string(b[:n])
		}
	}()
	select {
	case got := <-result:
		if got != "cached" {
			t.Fatal(got)
		}
	case <-time.After(time.Second):
		t.Fatal("cached audio blocked by network")
	}
	waiting := make(chan struct{})
	go func() { h.Read(make([]byte, 1)); close(waiting) }()
	closed := make(chan struct{})
	go func() { h.Close(); close(closed) }()
	for _, ch := range []chan struct{}{closed, waiting} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatal("Close did not unblock pending read")
		}
	}
}
func TestFinalPartialBlockAndRewind(t *testing.T) {
	want := strings.Repeat("x", 32768) + "tail"
	h := NewBufferedStream(io.NopCloser(strings.NewReader(want)), int64(len(want)))
	defer h.Close()
	got, err := io.ReadAll(h)
	if err != nil || string(got) != want {
		t.Fatalf("length=%d err=%v", len(got), err)
	}
	if !h.IsDone() || !h.IsBuffered() {
		t.Fatal("completion state incorrect")
	}
	h.Seek(-4, io.SeekEnd)
	got, err = io.ReadAll(h)
	if err != nil || string(got) != "tail" {
		t.Fatalf("rewind=%q err=%v", got, err)
	}
	var out bytes.Buffer
	if _, err = h.WriteTo(&out); err != nil || out.String() != want {
		t.Fatalf("cache: %v", err)
	}
}
func TestTruncatedDownloadIsNotCacheable(t *testing.T) {
	h := NewBufferedStream(io.NopCloser(strings.NewReader("partial")), 100)
	defer h.Close()
	got, err := io.ReadAll(h)
	if string(got) != "partial" || err != io.ErrUnexpectedEOF {
		t.Fatalf("%q %v", got, err)
	}
	if h.IsBuffered() {
		t.Fatal("truncation reported as complete")
	}
	var out bytes.Buffer
	if _, err = h.WriteTo(&out); err == nil || out.Len() != 0 {
		t.Fatal("truncated audio cached")
	}
}
func TestSeekAheadAndInvalidWhence(t *testing.T) {
	h := NewBufferedStream(io.NopCloser(strings.NewReader("0123456789")), 10)
	defer h.Close()
	if _, err := h.Seek(8, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(h)
	if string(got) != "89" || err != nil {
		t.Fatalf("%q %v", got, err)
	}
	if _, err = h.Seek(0, 999); err == nil {
		t.Fatal("invalid whence accepted")
	}
}
