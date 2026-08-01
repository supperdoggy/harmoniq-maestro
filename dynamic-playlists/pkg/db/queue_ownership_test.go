package db

import (
	"reflect"
	"testing"

	models "github.com/supperdoggy/spot-models"
	"github.com/supperdoggy/spot-models/spotify"
	"go.mongodb.org/mongo-driver/bson"
)

func TestNewDownloadQueueRequest_StartsPending(t *testing.T) {
	request := newDownloadQueueRequest(
		"request-1",
		"https://open.spotify.com/track/one",
		"Artist - Title",
		42,
		spotify.SpotifyObjectTypeTrack,
		100,
	)

	if request.State != models.DownloadRequestStatePending {
		t.Fatalf("state = %q, want pending", request.State)
	}
	if !request.Active || request.Errored {
		t.Fatalf("legacy flags = active:%v errored:%v, want true/false", request.Active, request.Errored)
	}
	if request.WorkerID != "" || request.ClaimID != "" || request.LeaseExpiresAt != 0 {
		t.Fatalf("new request unexpectedly has lease ownership: %+v", request)
	}
	if request.CreatedAt != 100 || request.UpdatedAt != 100 {
		t.Fatalf("timestamps = %d/%d, want 100/100", request.CreatedAt, request.UpdatedAt)
	}
}

func TestAlreadySyncedFilter_IgnoresCancelledAndFailedJobs(t *testing.T) {
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
}
