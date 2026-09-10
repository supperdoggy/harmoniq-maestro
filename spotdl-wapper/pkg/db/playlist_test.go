package db

import (
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

func TestActivePlaylistFilterIncludesLegacyAndDueRequests(t *testing.T) {
	const now int64 = 1_000
	want := bson.M{
		"active": true,
		"$or": bson.A{
			bson.M{"next_attempt_at": bson.M{"$exists": false}},
			bson.M{"next_attempt_at": nil},
			bson.M{"next_attempt_at": bson.M{"$lte": now}},
		},
	}

	if got := activePlaylistFilter(now); !reflect.DeepEqual(got, want) {
		t.Fatalf("playlist eligibility filter mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestPlaylistSortIsDeterministic(t *testing.T) {
	want := bson.D{
		{Key: "created_at", Value: 1},
		{Key: "_id", Value: 1},
	}
	if got := playlistSort(); !reflect.DeepEqual(got, want) {
		t.Fatalf("playlist sort = %#v, want %#v", got, want)
	}
}
