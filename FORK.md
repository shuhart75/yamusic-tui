# Maintained desktop fork

This fork is based on DECE2183/yamusic-tui main at
4d543ae (2026-10-26), including the upstream empty-playlist and pinned-album fixes.
The Go module path is retained to avoid rewriting internal imports.

Changes:
- A single background reader fills the audio buffer without holding its state
  mutex during network I/O. Cached audio remains readable during a stall;
  Close cancels the download and wakes pending readers.
- Audio downloads resume with validated Range/ETag checks and bounded retries.
  Metadata retains a separate reader compatible with chunked JSON responses.
- Truncated downloads retain their received bytes but are not marked complete
  or written to the music cache. Manual save while buffering reports a message
  instead of blocking the UI. Cache write errors are reported.
- Label IDs accept both string and numeric API encodings without precision loss.
  This fixes live library/playlist/radio decoding failures.
- Empty user playlists remain selectable and can receive tracks. Removing the
  final track preserves the playlist. Shuffle handles a removed current track,
  invalid selection and duplicate track IDs without losing the selected entry.
- Creating the first user playlist preserves the sidebar and playback indices.
- Empty track-list actions are guarded. Incomplete playlist API responses and
  malformed JSON are reported instead of panicking or silently appearing empty.
- Metadata API requests have an overall 15-second deadline.
- New installations have additional Tab/Shift+Tab playlist navigation, `/`
  search, `i` artist and Shift+Left/Right seek bindings. Existing config files
  retain their explicit bindings; see the workstation installer below.

Build/test: `go test -race ./...` and `go build -o yamusic-tui .`.
Tests use local HTTP servers; they need loopback socket access.

For the CachyOS/niri workstation, use the installer in
`~/reustarsnest-system/scripts/install-yamusic-fork.sh`. It keeps binary/config
backups and installs a conflict-free key preset without copying account tokens
into either repository. Plain upstream `go install ...@latest` replaces the fork.

Remaining work: playlist mutations still await the API in the UI handler (now
bounded by a deadline). A fully asynchronous request/state model needs separate
coverage for reload, switching playlists and out-of-order results. This version
is not a claim that every upstream UI/concurrency issue has been resolved.
