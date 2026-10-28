package stream

import (
	"errors"
	"io"
	"sync"
)

const _BUFFERING_AMOUNT = 32 * 1024

var errOutOfSize = errors.New("position is out of data size")

// A single producer owns source.Read. Network I/O never holds mux, so cached
// audio, seeking and cancellation remain available while the network stalls.
// source.Close must unblock a pending Read (as HTTP response bodies do).
type BufferedStream struct {
	source               io.ReadCloser
	mux                  sync.Mutex
	changed              *sync.Cond
	closeOnce            sync.Once
	closeError           error
	readBuffer           []byte
	readIndex, totalSize int64
	lastError            error
	finished, closed     bool
}

func NewBufferedStream(source io.ReadCloser, totalSize int64) *BufferedStream {
	h := &BufferedStream{source: source, totalSize: totalSize}
	h.changed = sync.NewCond(&h.mux)
	go h.bufferFrames()
	return h
}
func (h *BufferedStream) Length() int64 { return h.totalSize }
func (h *BufferedStream) closeSource() error {
	h.closeOnce.Do(func() { h.closeError = h.source.Close() })
	return h.closeError
}
func (h *BufferedStream) Close() error {
	h.mux.Lock()
	h.closed = true
	h.changed.Broadcast()
	h.mux.Unlock()
	return h.closeSource()
}
func (h *BufferedStream) Read(p []byte) (int, error) {
	h.mux.Lock()
	defer h.mux.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	for !h.closed && !h.finished && h.readIndex >= int64(len(h.readBuffer)) {
		h.changed.Wait()
	}
	if h.closed {
		return 0, io.EOF
	}
	if h.readIndex < int64(len(h.readBuffer)) {
		n := copy(p, h.readBuffer[h.readIndex:])
		h.readIndex += int64(n)
		return n, nil
	}
	if h.lastError != nil {
		return 0, h.lastError
	}
	return 0, io.EOF
}
func (h *BufferedStream) Seek(offset int64, whence int) (int64, error) {
	h.mux.Lock()
	defer h.mux.Unlock()
	pos := offset
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		pos += h.readIndex
	case io.SeekEnd:
		pos += h.totalSize
	default:
		return h.readIndex, errors.New("invalid seek whence")
	}
	if pos < 0 || pos > h.totalSize {
		return h.readIndex, errOutOfSize
	}
	h.readIndex = pos
	return pos, nil
}
func (h *BufferedStream) IsDone() bool {
	if h == nil {
		return false
	}
	h.mux.Lock()
	defer h.mux.Unlock()
	return !h.closed && h.readIndex >= h.totalSize
}
func (h *BufferedStream) IsBuffered() bool {
	if h == nil {
		return false
	}
	h.mux.Lock()
	defer h.mux.Unlock()
	return h.finished && h.lastError == nil && int64(len(h.readBuffer)) == h.totalSize
}
func (h *BufferedStream) Progress() float64 {
	if h == nil || h.totalSize <= 0 {
		return 0
	}
	h.mux.Lock()
	defer h.mux.Unlock()
	return float64(h.readIndex) / float64(h.totalSize)
}
func (h *BufferedStream) BufferingProgress() float64 {
	if h == nil || h.totalSize <= 0 {
		return 0
	}
	h.mux.Lock()
	defer h.mux.Unlock()
	return float64(len(h.readBuffer)) / float64(h.totalSize)
}
func (h *BufferedStream) BufferAll() {
	h.mux.Lock()
	defer h.mux.Unlock()
	for !h.finished && !h.closed {
		h.changed.Wait()
	}
}
func (h *BufferedStream) WriteTo(dest io.Writer) (int64, error) {
	h.mux.Lock()
	if !h.finished || h.lastError != nil || int64(len(h.readBuffer)) != h.totalSize {
		err := h.lastError
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		h.mux.Unlock()
		return 0, err
	}
	// The producer is finished: this slice is immutable, even after Close.
	data := h.readBuffer
	h.mux.Unlock()
	n, err := dest.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return int64(n), err
}
func (h *BufferedStream) Error() error {
	h.mux.Lock()
	defer h.mux.Unlock()
	return h.lastError
}
func (h *BufferedStream) bufferFrames() {
	defer h.closeSource()
	buf := make([]byte, _BUFFERING_AMOUNT)
	emptyReads := 0
	for {
		h.mux.Lock()
		remaining := h.totalSize - int64(len(h.readBuffer))
		if h.closed || remaining <= 0 {
			h.finished = true
			h.changed.Broadcast()
			h.mux.Unlock()
			return
		}
		h.mux.Unlock()
		n, err := h.source.Read(buf[:min(int64(len(buf)), remaining)])
		h.mux.Lock()
		if h.closed {
			h.mux.Unlock()
			return
		}
		h.readBuffer = append(h.readBuffer, buf[:n]...)
		if n == 0 {
			emptyReads++
		} else {
			emptyReads = 0
		}
		if emptyReads >= 100 && err == nil {
			err = io.ErrNoProgress
		}
		if int64(len(h.readBuffer)) == h.totalSize {
			h.finished = true
		} else if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			h.lastError = err
			h.finished = true
		}
		finished := h.finished
		h.changed.Broadcast()
		h.mux.Unlock()
		if finished {
			return
		}
	}
}
