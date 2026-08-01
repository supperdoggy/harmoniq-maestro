package db

import (
	"context"
	"encoding/hex"
	"reflect"
	"testing"
	"time"

	models "github.com/supperdoggy/spot-models"
	"go.mongodb.org/mongo-driver/bson"
)

func TestBuildClaimFilter_IncludesLegacyAndExpiredWork(t *testing.T) {
	const now int64 = 1_000

	want := bson.M{
		"active": true,
		"$and": bson.A{
			bson.M{"$or": bson.A{
				bson.M{"state": bson.M{"$exists": false}},
				bson.M{"state": nil},
				bson.M{"state": ""},
				bson.M{"state": bson.M{"$in": bson.A{
					models.DownloadRequestStatePending,
					models.DownloadRequestStateClaimed,
					models.DownloadRequestStateResolving,
					models.DownloadRequestStateDownloading,
					models.DownloadRequestStateValidating,
					models.DownloadRequestStateImported,
					models.DownloadRequestStateRetryWait,
				}}},
			}},
			bson.M{"$or": bson.A{
				bson.M{"lease_expires_at": bson.M{"$exists": false}},
				bson.M{"lease_expires_at": nil},
				bson.M{"lease_expires_at": bson.M{"$lte": now}},
			}},
			bson.M{"$or": bson.A{
				bson.M{"state": bson.M{"$ne": models.DownloadRequestStateRetryWait}},
				bson.M{"next_attempt_at": bson.M{"$exists": false}},
				bson.M{"next_attempt_at": nil},
				bson.M{"next_attempt_at": bson.M{"$lte": now}},
			}},
			bson.M{"$or": bson.A{
				bson.M{"backend": bson.M{"$exists": false}},
				bson.M{"backend": nil},
				bson.M{"backend": ""},
				bson.M{"backend": "yt-dlp"},
			}},
		},
	}

	if got := buildClaimFilter(now, "yt-dlp"); !reflect.DeepEqual(got, want) {
		t.Fatalf("claim filter mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestAlreadySyncedFilter_SuppressesInFlightCompletedAndSuccessfulLegacyJobs(t *testing.T) {
	inFlightOrSuccessfulStates := bson.A{
		models.DownloadRequestStatePending,
		models.DownloadRequestStateClaimed,
		models.DownloadRequestStateResolving,
		models.DownloadRequestStateDownloading,
		models.DownloadRequestStateValidating,
		models.DownloadRequestStateImported,
		models.DownloadRequestStateRetryWait,
		models.DownloadRequestStateNeedsReview,
		models.DownloadRequestStateCompleted,
	}
	want := bson.M{
		"spotify_url": "spotify:test",
		"$or": bson.A{
			bson.M{"state": bson.M{"$in": inFlightOrSuccessfulStates}},
			bson.M{"$and": bson.A{
				bson.M{"$or": bson.A{
					bson.M{"state": bson.M{"$exists": false}},
					bson.M{"state": nil},
					bson.M{"state": ""},
				}},
				bson.M{"$or": bson.A{
					bson.M{"active": true},
					bson.M{"$and": bson.A{
						bson.M{"active": false},
						bson.M{"errored": bson.M{"$ne": true}},
					}},
				}},
			}},
		},
	}

	if got := alreadySyncedFilter("spotify:test"); !reflect.DeepEqual(got, want) {
		t.Fatalf("already-synced filter mismatch:\n got: %#v\nwant: %#v", got, want)
	}

	encoded, err := bson.Marshal(alreadySyncedFilter("spotify:test"))
	if err != nil {
		t.Fatalf("marshal already-synced filter: %v", err)
	}
	for _, excludedState := range []models.DownloadRequestState{
		models.DownloadRequestStateFailed,
		models.DownloadRequestStateCancelled,
	} {
		if containsBSONString(encoded, string(excludedState)) {
			t.Errorf("already-synced filter unexpectedly contains excluded state %q", excludedState)
		}
	}
}

func containsBSONString(encoded []byte, value string) bool {
	var document bson.M
	if err := bson.Unmarshal(encoded, &document); err != nil {
		return false
	}
	return containsValue(document, value)
}

func containsValue(value any, target string) bool {
	switch typed := value.(type) {
	case string:
		return typed == target
	case bson.M:
		for _, child := range typed {
			if containsValue(child, target) {
				return true
			}
		}
	case bson.A:
		for _, child := range typed {
			if containsValue(child, target) {
				return true
			}
		}
	}
	return false
}

func TestBuildClaimFilter_ExcludesPausedTerminalAndOtherBackendStates(t *testing.T) {
	filter := buildClaimFilter(1_000, "yt-dlp")
	andConditions := filter["$and"].(bson.A)
	stateConditions := andConditions[0].(bson.M)["$or"].(bson.A)
	inCondition := stateConditions[3].(bson.M)["state"].(bson.M)["$in"].(bson.A)

	for _, excluded := range []models.DownloadRequestState{
		models.DownloadRequestStateNeedsReview,
		models.DownloadRequestStateCompleted,
		models.DownloadRequestStateFailed,
		models.DownloadRequestStateCancelled,
	} {
		for _, included := range inCondition {
			if included == excluded {
				t.Errorf("state %q must not be automatically claimable", excluded)
			}
		}
	}

	backendConditions := andConditions[3].(bson.M)["$or"].(bson.A)
	for _, condition := range backendConditions {
		backend, exists := condition.(bson.M)["backend"]
		if exists && backend == "spotdl" {
			t.Fatal("claim filter unexpectedly accepts work assigned to another backend")
		}
	}
}

func TestBuildClaimUpdate_InitializesLegacyStateAndLease(t *testing.T) {
	got := buildClaimUpdate("worker-1", "claim-1", "yt-dlp", 100, 200)
	want := bson.M{
		"$set": bson.M{
			"active":           true,
			"state":            models.DownloadRequestStateClaimed,
			"worker_id":        "worker-1",
			"claim_id":         "claim-1",
			"lease_expires_at": int64(200),
			"backend":          "yt-dlp",
			"updated_at":       int64(100),
		},
		"$unset": bson.M{"next_attempt_at": ""},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("claim update mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestClaimSort_PreservesLegacyPriority(t *testing.T) {
	want := bson.D{
		{Key: "errored", Value: 1},
		{Key: "created_at", Value: 1},
		{Key: "_id", Value: 1},
	}
	if got := claimSort(); !reflect.DeepEqual(got, want) {
		t.Fatalf("claim sort = %#v, want %#v", got, want)
	}
}

func TestOwnedLeaseFilter_RequiresSameWorkerClaimAndLiveLease(t *testing.T) {
	want := bson.M{
		"_id":              "request-1",
		"worker_id":        "worker-1",
		"claim_id":         "claim-1",
		"lease_expires_at": bson.M{"$gt": int64(100)},
	}
	if got := ownedLeaseFilter("request-1", "worker-1", "claim-1", 100); !reflect.DeepEqual(got, want) {
		t.Fatalf("owned lease filter = %#v, want %#v", got, want)
	}
}

func TestNewClaimID_IsRandomHexToken(t *testing.T) {
	first, err := newClaimID()
	if err != nil {
		t.Fatalf("newClaimID() error = %v", err)
	}
	second, err := newClaimID()
	if err != nil {
		t.Fatalf("newClaimID() second error = %v", err)
	}
	if first == second {
		t.Fatal("newClaimID() returned the same token twice")
	}
	if len(first) != 64 {
		t.Fatalf("claim ID length = %d, want 64", len(first))
	}
	if _, err := hex.DecodeString(first); err != nil {
		t.Fatalf("claim ID is not hexadecimal: %v", err)
	}
}

func TestLeaseOperations_RequireBackendAndClaimID(t *testing.T) {
	database := &db{}
	ctx := context.Background()

	if _, err := database.ClaimNextActiveRequest(ctx, "worker-1", " ", time.Minute); err == nil {
		t.Fatal("ClaimNextActiveRequest() accepted an empty backend")
	}
	if err := database.RenewRequestLease(
		ctx,
		"request-1",
		"worker-1",
		"",
		time.Minute,
	); err == nil {
		t.Fatal("RenewRequestLease() accepted an empty claim ID")
	}
	if err := database.UpdateClaimedRequest(
		ctx,
		models.DownloadQueueRequest{
			ID:    "request-1",
			State: models.DownloadRequestStateClaimed,
		},
		"worker-1",
		"",
	); err == nil {
		t.Fatal("UpdateClaimedRequest() accepted an empty claim ID")
	}
	if err := database.ReleaseRequestLease(
		ctx,
		"request-1",
		"worker-1",
		"",
		models.DownloadRequestStateRetryWait,
	); err == nil {
		t.Fatal("ReleaseRequestLease() accepted an empty claim ID")
	}
}

func TestLeaseExpiryUnix_RoundsShortLeaseUp(t *testing.T) {
	now := time.Unix(100, 0)
	if got := leaseExpiryUnix(now, time.Millisecond); got != 101 {
		t.Fatalf("short lease expiry = %d, want 101", got)
	}
	if got := leaseExpiryUnix(now, 2*time.Minute); got != 220 {
		t.Fatalf("two-minute lease expiry = %d, want 220", got)
	}
}

func TestLegacyFlagsForState(t *testing.T) {
	tests := []struct {
		state   models.DownloadRequestState
		active  bool
		errored bool
	}{
		{models.DownloadRequestStatePending, true, false},
		{models.DownloadRequestStateClaimed, true, false},
		{models.DownloadRequestStateResolving, true, false},
		{models.DownloadRequestStateDownloading, true, false},
		{models.DownloadRequestStateValidating, true, false},
		{models.DownloadRequestStateImported, true, false},
		{models.DownloadRequestStateRetryWait, true, true},
		{models.DownloadRequestStateNeedsReview, true, true},
		{models.DownloadRequestStateCompleted, false, false},
		{models.DownloadRequestStateFailed, false, true},
		{models.DownloadRequestStateCancelled, false, false},
	}

	for _, test := range tests {
		t.Run(string(test.state), func(t *testing.T) {
			active, errored := legacyFlagsForState(test.state)
			if active != test.active || errored != test.errored {
				t.Fatalf(
					"legacyFlagsForState(%q) = (%v, %v), want (%v, %v)",
					test.state,
					active,
					errored,
					test.active,
					test.errored,
				)
			}
		})
	}
}

func TestClaimedRequestUpdateFields_DualWritesState(t *testing.T) {
	request := models.DownloadQueueRequest{
		State:         models.DownloadRequestStateRetryWait,
		Backend:       "yt-dlp",
		NextAttemptAt: 500,
		LastError: &models.DownloadRequestError{
			Code:      "temporary",
			Retryable: true,
		},
	}

	fields := claimedRequestUpdateFields(request, 100)
	if fields["active"] != true || fields["errored"] != true {
		t.Fatalf("retry state did not dual-write legacy flags: %#v", fields)
	}
	if fields["updated_at"] != int64(100) {
		t.Fatalf("zero updated_at was not initialized: %#v", fields)
	}
	if fields["state"] != models.DownloadRequestStateRetryWait ||
		fields["next_attempt_at"] != int64(500) {
		t.Fatalf("state metadata missing from update: %#v", fields)
	}
	for _, immutableOwnershipField := range []string{"backend", "worker_id", "claim_id", "lease_expires_at"} {
		if _, exists := fields[immutableOwnershipField]; exists {
			t.Errorf("progress update unexpectedly rewrites %q: %#v", immutableOwnershipField, fields)
		}
	}
}

func TestFindMusicFiles_ValidatesPairsBeforeDatabaseAccess(t *testing.T) {
	database := &db{}

	files, err := database.FindMusicFiles(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("empty FindMusicFiles() error = %v", err)
	}
	if files == nil || len(files) != 0 {
		t.Fatalf("empty FindMusicFiles() = %#v, want non-nil empty result", files)
	}

	if _, err := database.FindMusicFiles(
		context.Background(),
		[]string{"artist"},
		nil,
	); err == nil {
		t.Fatal("FindMusicFiles() accepted mismatched artist/title pairs")
	}
}

func TestMusicFileIdentityFilter_PrefersStableIdentity(t *testing.T) {
	tests := []struct {
		name string
		file models.MusicFile
		want bson.M
	}{
		{
			name: "spotify before isrc and path",
			file: models.MusicFile{SpotifyID: "spotify-1", ISRC: "isrc-1", Path: "/music/one.opus"},
			want: bson.M{"spotify_id": "spotify-1"},
		},
		{
			name: "isrc before path",
			file: models.MusicFile{ISRC: "isrc-1", Path: "/music/one.opus"},
			want: bson.M{"isrc": "isrc-1"},
		},
		{
			name: "path fallback",
			file: models.MusicFile{Path: "/music/one.opus"},
			want: bson.M{"path": "/music/one.opus"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := musicFileIdentityFilter(test.file)
			if err != nil {
				t.Fatalf("musicFileIdentityFilter() error = %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("identity filter = %#v, want %#v", got, test.want)
			}
		})
	}

	if _, err := musicFileIdentityFilter(models.MusicFile{}); err == nil {
		t.Fatal("identity filter accepted a file without spotify ID, ISRC, or path")
	}
}

func TestMusicFileUpsertUpdate_PreservesInsertIdentityAndMutableMetadata(t *testing.T) {
	file := models.MusicFile{
		Artist:         "Artist",
		Album:          "Album",
		Title:          "Title",
		Path:           "/music/title.opus",
		SpotifyID:      "spotify-1",
		ISRC:           "isrc-1",
		SourceProvider: "youtube",
		SourceID:       "video-1",
		Checksum:       "sha256:abc",
		Format:         "opus",
		MatchScore:     0.99,
	}

	update := musicFileUpsertUpdate(file, "catalog-1", 100)
	setOnInsert := update["$setOnInsert"].(bson.M)
	if setOnInsert["_id"] != "catalog-1" || setOnInsert["created_at"] != int64(100) {
		t.Fatalf("unexpected setOnInsert: %#v", setOnInsert)
	}
	set := update["$set"].(bson.M)
	expectedFields := bson.M{
		"artist":          "Artist",
		"path":            "/music/title.opus",
		"spotify_id":      "spotify-1",
		"isrc":            "isrc-1",
		"source_provider": "youtube",
		"source_id":       "video-1",
		"checksum":        "sha256:abc",
		"format":          "opus",
		"match_score":     0.99,
		"updated_at":      int64(100),
	}
	for key, want := range expectedFields {
		if got := set[key]; got != want {
			t.Errorf("$set[%q] = %#v, want %#v", key, got, want)
		}
	}
}
