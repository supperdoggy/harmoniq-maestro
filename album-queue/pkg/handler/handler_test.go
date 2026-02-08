package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/supperdoggy/SmartHomeServer/harmoniq-maestro/album-queue/pkg/db"
	models "github.com/supperdoggy/spot-models"
	"github.com/supperdoggy/spot-models/spotify"
	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"
	"gopkg.in/tucnak/telebot.v2"
)

type newRequestCall struct {
	url                string
	name               string
	creatorID          int64
	objectType         spotify.SpotifyObjectType
	expectedTrackCount int
	trackMetadata      []spotify.TrackMetadata
}

type fakeDatabase struct {
	unresolvedTracks []db.FailedTrack
	activeByURL      map[string]bool

	newRequestErr error
	newRequests   []newRequestCall
}

func (f *fakeDatabase) NewDownloadRequest(_ context.Context, url, name string, creatorID int64, objectType spotify.SpotifyObjectType, expectedTrackCount int, trackMetadata []spotify.TrackMetadata) error {
	f.newRequests = append(f.newRequests, newRequestCall{
		url:                url,
		name:               name,
		creatorID:          creatorID,
		objectType:         objectType,
		expectedTrackCount: expectedTrackCount,
		trackMetadata:      trackMetadata,
	})
	return f.newRequestErr
}

func (f *fakeDatabase) GetActiveRequests(context.Context) ([]models.DownloadQueueRequest, error) {
	return nil, nil
}

func (f *fakeDatabase) GetUnresolvedFailedTracks(context.Context) ([]db.FailedTrack, error) {
	return f.unresolvedTracks, nil
}

func (f *fakeDatabase) GetUnresolvedFailedTrackByURL(_ context.Context, trackURL string) (db.FailedTrack, error) {
	for _, track := range f.unresolvedTracks {
		if track.SpotifyURL == trackURL {
			return track, nil
		}
	}

	return db.FailedTrack{}, mongo.ErrNoDocuments
}

func (f *fakeDatabase) HasActiveRequestByURL(_ context.Context, trackURL string) (bool, error) {
	return f.activeByURL[trackURL], nil
}

func (f *fakeDatabase) DeactivateRequest(context.Context, string) error {
	return nil
}

func (f *fakeDatabase) NewPlaylistRequest(context.Context, string, int64, bool) error {
	return nil
}

func (f *fakeDatabase) GetActivePlaylists(context.Context) ([]models.PlaylistRequest, error) {
	return nil, nil
}

func (f *fakeDatabase) FindMusicFiles(context.Context, []string, []string) ([]models.MusicFile, error) {
	return nil, nil
}

func (f *fakeDatabase) UpdateDownloadRequest(context.Context, models.DownloadQueueRequest) error {
	return nil
}

func (f *fakeDatabase) Close(context.Context) error {
	return nil
}

func (f *fakeDatabase) Ping(context.Context) error {
	return nil
}

func (f *fakeDatabase) GetStats(context.Context) (*db.Stats, error) {
	return &db.Stats{}, nil
}

type pageEvent struct {
	text   string
	markup *telebot.ReplyMarkup
}

type callbackEvent struct {
	text      string
	showAlert bool
}

type testSinks struct {
	replies      []string
	sentPages    []pageEvent
	editedPages  []pageEvent
	callbackAcks []callbackEvent
	webhookCalls int
}

func createTestHandler(database *fakeDatabase, sinks *testSinks) *handler {
	return &handler{
		db:        database,
		log:       zap.NewNop(),
		whiteList: []int64{1},
		replyFunc: func(_ *telebot.Message, text string) error {
			sinks.replies = append(sinks.replies, text)
			return nil
		},
		sendWebhookFn: func() error {
			sinks.webhookCalls++
			return nil
		},
		sendFailedPageFn: func(_ *telebot.Message, text string, markup *telebot.ReplyMarkup) error {
			sinks.sentPages = append(sinks.sentPages, pageEvent{text: text, markup: markup})
			return nil
		},
		editFailedPageFn: func(_ *telebot.Message, text string, markup *telebot.ReplyMarkup) error {
			sinks.editedPages = append(sinks.editedPages, pageEvent{text: text, markup: markup})
			return nil
		},
		respondCallbackFn: func(_ *telebot.Callback, text string, showAlert bool) error {
			sinks.callbackAcks = append(sinks.callbackAcks, callbackEvent{text: text, showAlert: showAlert})
			return nil
		},
	}
}

func testMessage(text string) *telebot.Message {
	return &telebot.Message{Text: text, Sender: &telebot.User{ID: 1}}
}

func TestHandleFailedReturnsEmptyState(t *testing.T) {
	db := &fakeDatabase{activeByURL: map[string]bool{}}
	sinks := &testSinks{}
	h := createTestHandler(db, sinks)

	h.HandleFailed(testMessage("/failed"))

	if len(sinks.replies) != 1 {
		t.Fatalf("expected exactly one reply, got %d", len(sinks.replies))
	}
	if !strings.Contains(sinks.replies[0], "нема невирішених") {
		t.Fatalf("unexpected empty-state reply: %q", sinks.replies[0])
	}
	if len(sinks.sentPages) != 0 {
		t.Fatalf("expected no paged sends, got %d", len(sinks.sentPages))
	}
}

func TestHandleFailedFormatsFlatList(t *testing.T) {
	db := &fakeDatabase{
		activeByURL: map[string]bool{},
		unresolvedTracks: []db.FailedTrack{
			{SpotifyURL: "https://open.spotify.com/track/aaa", Artist: "a", Title: "one", FailedAttempts: 3, SourceCount: 2},
			{SpotifyURL: "https://open.spotify.com/track/bbb", Artist: "b", Title: "two", FailedAttempts: 4, SourceCount: 1},
		},
	}
	sinks := &testSinks{}
	h := createTestHandler(db, sinks)

	h.HandleFailed(testMessage("/failed"))

	if len(sinks.sentPages) != 1 {
		t.Fatalf("expected one paged send, got %d", len(sinks.sentPages))
	}
	body := sinks.sentPages[0].text
	checks := []string{
		"Невирішені фейли на скачування: 2",
		"Сторінка 1/1",
		"1. a - one",
		"https://open.spotify.com/track/aaa",
		"2. b - two",
		"https://open.spotify.com/track/bbb",
	}
	for _, check := range checks {
		if !strings.Contains(body, check) {
			t.Fatalf("reply does not contain %q: %q", check, body)
		}
	}
	if sinks.sentPages[0].markup != nil {
		t.Fatalf("expected no inline keyboard for single page")
	}
}

func TestHandleFailedUsesInlineKeyboardPagination(t *testing.T) {
	tracks := make([]db.FailedTrack, 0, failedPageSize+5)
	for i := 0; i < failedPageSize+5; i++ {
		tracks = append(tracks, db.FailedTrack{
			SpotifyURL:     "https://open.spotify.com/track/track" + strings.Repeat("x", 8) + string(rune('a'+(i%20))),
			Artist:         "artist",
			Title:          "title",
			FailedAttempts: 3,
			SourceCount:    1,
		})
	}

	db := &fakeDatabase{activeByURL: map[string]bool{}, unresolvedTracks: tracks}
	sinks := &testSinks{}
	h := createTestHandler(db, sinks)

	h.HandleFailed(testMessage("/failed"))

	if len(sinks.sentPages) != 1 {
		t.Fatalf("expected one paged send, got %d", len(sinks.sentPages))
	}
	if !strings.Contains(sinks.sentPages[0].text, "Сторінка 1/2") {
		t.Fatalf("expected first page header, got: %q", sinks.sentPages[0].text)
	}
	markup := sinks.sentPages[0].markup
	if markup == nil {
		t.Fatal("expected inline keyboard markup")
	}
	if len(markup.InlineKeyboard) == 0 || len(markup.InlineKeyboard[0]) == 0 {
		t.Fatal("expected non-empty InlineKeyboard")
	}
	if markup.InlineKeyboard[0][0].Unique != failedPageCallbackUnique {
		t.Fatalf("expected callback unique %q, got %q", failedPageCallbackUnique, markup.InlineKeyboard[0][0].Unique)
	}
}

func TestHandleFailedPageEditsMessageAndResponds(t *testing.T) {
	tracks := make([]db.FailedTrack, 0, failedPageSize+3)
	for i := 0; i < failedPageSize+3; i++ {
		tracks = append(tracks, db.FailedTrack{
			SpotifyURL:     "https://open.spotify.com/track/id" + string(rune('a'+(i%20))),
			Artist:         "artist",
			Title:          "title",
			FailedAttempts: 4,
			SourceCount:    1,
		})
	}

	db := &fakeDatabase{activeByURL: map[string]bool{}, unresolvedTracks: tracks}
	sinks := &testSinks{}
	h := createTestHandler(db, sinks)

	cb := &telebot.Callback{
		Data:    "1",
		Sender:  &telebot.User{ID: 1},
		Message: &telebot.Message{ID: 100},
	}
	h.HandleFailedPage(cb)

	if len(sinks.editedPages) != 1 {
		t.Fatalf("expected one edited page, got %d", len(sinks.editedPages))
	}
	if !strings.Contains(sinks.editedPages[0].text, "Сторінка 2/2") {
		t.Fatalf("expected second page content, got: %q", sinks.editedPages[0].text)
	}
	if len(sinks.callbackAcks) != 1 {
		t.Fatalf("expected one callback ack, got %d", len(sinks.callbackAcks))
	}
	if sinks.callbackAcks[0].showAlert {
		t.Fatal("expected callback ack without alert")
	}
}

func TestHandleRedownloadRejectsBadSyntax(t *testing.T) {
	db := &fakeDatabase{activeByURL: map[string]bool{}}
	sinks := &testSinks{}
	h := createTestHandler(db, sinks)

	h.HandleRedownload(testMessage("/redownload"))

	if len(sinks.replies) != 1 {
		t.Fatalf("expected one reply, got %d", len(sinks.replies))
	}
	if !strings.Contains(sinks.replies[0], "/redownload <spotify_track_url>") {
		t.Fatalf("unexpected reply: %q", sinks.replies[0])
	}
}

func TestHandleRedownloadRejectsNonTrackURL(t *testing.T) {
	db := &fakeDatabase{activeByURL: map[string]bool{}}
	sinks := &testSinks{}
	h := createTestHandler(db, sinks)

	h.HandleRedownload(testMessage("/redownload https://open.spotify.com/album/123"))

	if len(sinks.replies) != 1 {
		t.Fatalf("expected one reply, got %d", len(sinks.replies))
	}
	if !strings.Contains(sinks.replies[0], "https://open.spotify.com/track/") {
		t.Fatalf("unexpected reply: %q", sinks.replies[0])
	}
}

func TestHandleRedownloadRejectsTrackNotInUnresolvedFailures(t *testing.T) {
	db := &fakeDatabase{activeByURL: map[string]bool{}}
	sinks := &testSinks{}
	h := createTestHandler(db, sinks)

	h.HandleRedownload(testMessage("/redownload https://open.spotify.com/track/missing"))

	if len(sinks.replies) != 1 {
		t.Fatalf("expected one reply, got %d", len(sinks.replies))
	}
	if !strings.Contains(sinks.replies[0], "не знайдений") {
		t.Fatalf("unexpected reply: %q", sinks.replies[0])
	}
}

func TestHandleRedownloadSkipsWhenActiveExists(t *testing.T) {
	trackURL := "https://open.spotify.com/track/dup"
	db := &fakeDatabase{activeByURL: map[string]bool{trackURL: true}}
	sinks := &testSinks{}
	h := createTestHandler(db, sinks)

	h.HandleRedownload(testMessage("/redownload " + trackURL))

	if len(sinks.replies) != 1 {
		t.Fatalf("expected one reply, got %d", len(sinks.replies))
	}
	if !strings.Contains(sinks.replies[0], "вже є в активній черзі") {
		t.Fatalf("unexpected reply: %q", sinks.replies[0])
	}
	if len(db.newRequests) != 0 {
		t.Fatalf("expected no new request insertion, got %d", len(db.newRequests))
	}
}

func TestHandleRedownloadCreatesTrackRequestWithCallerID(t *testing.T) {
	trackURL := "https://open.spotify.com/track/retry"
	db := &fakeDatabase{
		activeByURL: map[string]bool{},
		unresolvedTracks: []db.FailedTrack{
			{SpotifyURL: trackURL, Artist: "artist", Title: "title", FailedAttempts: 3, SourceCount: 1},
		},
	}
	sinks := &testSinks{}
	h := createTestHandler(db, sinks)

	h.HandleRedownload(testMessage("/redownload " + trackURL))

	if len(db.newRequests) != 1 {
		t.Fatalf("expected one inserted request, got %d", len(db.newRequests))
	}
	inserted := db.newRequests[0]
	if inserted.creatorID != 1 {
		t.Fatalf("expected creator_id=1, got %d", inserted.creatorID)
	}
	if inserted.objectType != spotify.SpotifyObjectTypeTrack {
		t.Fatalf("expected object type track, got %q", inserted.objectType)
	}
	if inserted.expectedTrackCount != 1 {
		t.Fatalf("expected expected_track_count=1, got %d", inserted.expectedTrackCount)
	}
	if len(inserted.trackMetadata) != 1 {
		t.Fatalf("expected one track metadata entry, got %d", len(inserted.trackMetadata))
	}
	trackMeta := inserted.trackMetadata[0]
	if trackMeta.SpotifyURL != trackURL {
		t.Fatalf("expected track metadata URL %q, got %q", trackURL, trackMeta.SpotifyURL)
	}
	if trackMeta.FailedAttempts != 0 || trackMeta.Skipped || trackMeta.Found {
		t.Fatalf("expected reset track metadata state, got %+v", trackMeta)
	}
	if sinks.webhookCalls != 1 {
		t.Fatalf("expected webhook call count 1, got %d", sinks.webhookCalls)
	}
	if len(sinks.replies) != 1 || !strings.Contains(sinks.replies[0], "редовнлоад створено") {
		t.Fatalf("unexpected success reply: %#v", sinks.replies)
	}
}
