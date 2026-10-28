package mainpage

import (
	"testing"

	"github.com/dece2183/yamusic-tui/api"
	"github.com/dece2183/yamusic-tui/config"
	"github.com/dece2183/yamusic-tui/ui/components/playlist"
	"github.com/dece2183/yamusic-tui/ui/components/tracklist"
)

// A pinned album is stored as a playlist.Item with Kind == ALBUMS but with
// Tracks filled and Albums left empty, and thus SelectedAlbum keeps its zero
// value (0). displayPlaylist must not index into the empty Albums slice.
func TestDisplayPlaylistPinnedAlbumDoesNotPanic(t *testing.T) {
	if err := config.InitialLoad(); err != nil {
		t.Fatalf("config init: %v", err)
	}

	likes := make(map[string]bool)
	cache := make(map[string]bool)

	m := &Model{
		tracklist: tracklist.New(nil, &likes, &cache),
	}

	pl := &playlist.Item{
		Name:    "Pinned Album",
		Kind:    playlist.ALBUMS,
		Active:  true,
		Subitem: true,
		Tracks:  []api.Track{{Id: "1", Title: "track"}},
	}

	m.displayPlaylist(pl)
}

func TestShuffleAfterCurrentTrackRemoval(t *testing.T) {
	if err := config.InitialLoad(); err != nil {
		t.Fatal(err)
	}
	likes, cache := map[string]bool{}, map[string]bool{}
	m := &Model{playlists: playlist.New(nil, "test"), tracklist: tracklist.New(nil, &likes, &cache), currentPlaylistIndex: -1}
	pl := &playlist.Item{Name: "test", Kind: playlist.USER, Active: true, CurrentTrack: -1, SelectedTrack: 1, Tracks: []api.Track{{Id: "1"}, {Id: "2"}, {Id: "3"}}}
	m.playlists.SetItems([]*playlist.Item{pl})
	m.shufflePlaylist(pl)
	if pl.CurrentTrack != -1 {
		t.Fatal("removed current track was restored")
	}
	if pl.Tracks[pl.SelectedTrack].Id != "2" {
		t.Fatal("selection lost")
	}
}

func TestEmptyPlaylistActions(t *testing.T) {
	if err := config.InitialLoad(); err != nil {
		t.Fatal(err)
	}
	likes, cache := map[string]bool{}, map[string]bool{}
	m := &Model{playlists: playlist.New(nil, "test"), tracklist: tracklist.New(nil, &likes, &cache), currentPlaylistIndex: -1}
	m.playlists.SetItems([]*playlist.Item{{Name: "empty", Kind: playlist.USER, Active: true}})
	for _, action := range []tracklist.Control{tracklist.ADD_TO_PLAYLIST, tracklist.REMOVE_FROM_PLAYLIST, tracklist.SHARE, tracklist.SHUFFLE, tracklist.SHOW_ARTIST, tracklist.LIKE} {
		m.Update(action)
	}
	if m.isAddPlaylistActive {
		t.Fatal("add dialog opened without a track")
	}
}
