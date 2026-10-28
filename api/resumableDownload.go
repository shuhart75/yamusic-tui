package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// A download owns its cancellation context. Close interrupts reads and retries.
type resumableDownload struct {
	client           *http.Client
	ctx              context.Context
	cancel           context.CancelFunc
	token, url, mime string
	body             io.ReadCloser
	requestCancel    context.CancelFunc
	offset, size     int64
	etag             string
	retries          int
	idle             time.Duration
	backoff          time.Duration
}

func downloadRequest(token, url, mime string) (io.ReadCloser, int64, error) {
	return newResumableDownload(&httpClient, token, url, mime, 15*time.Second, 250*time.Millisecond)
}

func newResumableDownload(client *http.Client, token, url, mime string, idle, backoff time.Duration) (*resumableDownload, int64, error) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &resumableDownload{client: client, ctx: ctx, cancel: cancel,
		token: token, url: url, mime: mime, size: -1, idle: idle, backoff: backoff}
	err := r.open()
	for err != nil && ctx.Err() == nil && r.retries < 4 {
		if err = r.waitRetry(); err == nil {
			err = r.open()
		}
	}
	if err != nil {
		r.Close()
		return nil, 0, err
	}
	return r, r.size, nil
}

func (r *resumableDownload) waitRetry() error {
	r.retries++
	timer := time.NewTimer(time.Duration(r.retries) * r.backoff)
	defer timer.Stop()
	select {
	case <-r.ctx.Done():
		return r.ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (r *resumableDownload) open() error {
	ctx, cancel := context.WithCancel(r.ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.url, nil)
	if err != nil {
		cancel()
		return err
	}
	req.Header.Set("Accept", r.mime)
	req.Header.Set("Authorization", "OAuth "+r.token)
	req.Header.Set("Accept-Encoding", "identity")
	if r.offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", r.offset))
		if r.etag != "" {
			req.Header.Set("If-Range", r.etag)
		}
	}
	resp, err := r.client.Do(req)
	if err != nil {
		cancel()
		return err
	}
	valid := false
	defer func() {
		if !valid {
			resp.Body.Close()
			cancel()
		}
	}()
	switch resp.StatusCode {
	case http.StatusOK:
		if r.size >= 0 && resp.ContentLength != r.size {
			return errors.New("track size changed during retry")
		}
		if resp.ContentLength <= 0 {
			return errors.New("missing track content length")
		}
		if r.etag != "" && resp.Header.Get("ETag") != r.etag {
			return errors.New("track changed during retry")
		}
		r.size = resp.ContentLength
	case http.StatusPartialContent:
		var start, end, total int64
		if _, err := fmt.Sscanf(resp.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &total); err != nil || start != r.offset || total != r.size || end != total-1 || resp.ContentLength != total-start {
			return errors.New("invalid track Content-Range")
		}
		if r.etag != "" && resp.Header.Get("ETag") != r.etag {
			return errors.New("track changed during retry")
		}
	default:
		return fmt.Errorf("track download HTTP %d", resp.StatusCode)
	}
	if r.etag == "" {
		if tag := resp.Header.Get("ETag"); !strings.HasPrefix(tag, "W/") {
			r.etag = tag
		}
	}
	r.body, r.requestCancel = resp.Body, cancel
	valid = true
	// Some CDNs ignore Range. Discard the already delivered prefix, never replay it.
	if resp.StatusCode == http.StatusOK && r.offset > 0 {
		left := r.offset
		buf := make([]byte, 32*1024)
		for left > 0 {
			n, err := r.readBody(buf[:min(int64(len(buf)), left)])
			left -= int64(n)
			if err != nil || n == 0 {
				r.dropBody()
				if err != nil {
					return err
				}
				return io.ErrNoProgress
			}
		}
	}
	return nil
}

// Start the timeout only during a blocking read, not during playback or pause.
func (r *resumableDownload) readBody(p []byte) (int, error) {
	done := make(chan struct{})
	timer := time.AfterFunc(r.idle, func() { r.requestCancel(); close(done) })
	n, err := r.body.Read(p)
	if !timer.Stop() {
		<-done
	}
	return n, err
}

func (r *resumableDownload) dropBody() {
	if r.body != nil {
		r.requestCancel()
		r.body.Close()
		r.body = nil
	}
}

func (r *resumableDownload) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if err := r.ctx.Err(); err != nil {
			return 0, err
		}
		if r.offset == r.size {
			r.dropBody()
			return 0, io.EOF
		}
		if r.body == nil {
			if r.retries >= 4 {
				return 0, errors.New("track download failed after " + strconv.Itoa(r.retries) + " retries")
			}
			if err := r.waitRetry(); err != nil {
				return 0, err
			}
			if err := r.open(); err != nil {
				if r.retries >= 4 {
					return 0, err
				}
				continue
			}
		}
		n, err := r.readBody(p[:min(int64(len(p)), r.size-r.offset)])
		r.offset += int64(n)
		if err != nil || n == 0 {
			r.dropBody()
		}
		// Preserve partial data before retrying, including data returned with EOF.
		if n > 0 {
			return n, nil
		}
	}
}

func (r *resumableDownload) Close() error {
	// Request cancellation closes the HTTP body without racing a concurrent Read.
	r.cancel()
	return nil
}
