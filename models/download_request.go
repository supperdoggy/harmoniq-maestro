package models

import "github.com/supperdoggy/spot-models/spotify"

// DownloadRequestState is the durable lifecycle state of a download request.
//
// State is intentionally additive to the legacy Active and Errored fields.
// Documents created before the state machine was introduced have an empty
// state and are treated as pending by the queue repository.
type DownloadRequestState string

const (
	DownloadRequestStatePending     DownloadRequestState = "pending"
	DownloadRequestStateClaimed     DownloadRequestState = "claimed"
	DownloadRequestStateResolving   DownloadRequestState = "resolving"
	DownloadRequestStateDownloading DownloadRequestState = "downloading"
	DownloadRequestStateValidating  DownloadRequestState = "validating"
	DownloadRequestStateImported    DownloadRequestState = "imported"
	DownloadRequestStateCompleted   DownloadRequestState = "completed"
	DownloadRequestStateRetryWait   DownloadRequestState = "retry_wait"
	DownloadRequestStateNeedsReview DownloadRequestState = "needs_review"
	DownloadRequestStateFailed      DownloadRequestState = "failed"
	DownloadRequestStateCancelled   DownloadRequestState = "cancelled"
)

// IsTerminal reports whether no further worker processing is expected.
func (s DownloadRequestState) IsTerminal() bool {
	switch s {
	case DownloadRequestStateCompleted, DownloadRequestStateFailed, DownloadRequestStateCancelled:
		return true
	default:
		return false
	}
}

// DownloadRequestError is a machine-readable failure associated with the
// latest attempt. Message remains suitable for operators while Code, Stage,
// and Retryable drive retry and review decisions.
type DownloadRequestError struct {
	Code       string            `json:"code" bson:"code"`
	Stage      string            `json:"stage" bson:"stage"`
	Message    string            `json:"message" bson:"message"`
	Retryable  bool              `json:"retryable" bson:"retryable"`
	OccurredAt int64             `json:"occurred_at" bson:"occurred_at"`
	Details    map[string]string `json:"details,omitempty" bson:"details,omitempty"`
}

// DownloadRequestResult identifies the artifact produced by an acquisition
// backend. It deliberately stores the final path and stable source identity so
// completion does not have to be inferred from subprocess logs.
type DownloadRequestResult struct {
	Provider   string  `json:"provider" bson:"provider"`
	SourceID   string  `json:"source_id" bson:"source_id"`
	SourceURL  string  `json:"source_url,omitempty" bson:"source_url,omitempty"`
	FinalPath  string  `json:"final_path" bson:"final_path"`
	Format     string  `json:"format" bson:"format"`
	Checksum   string  `json:"checksum" bson:"checksum"`
	MatchScore float64 `json:"match_score" bson:"match_score"`
	CatalogID  string  `json:"catalog_id,omitempty" bson:"catalog_id,omitempty"`
	ImportedAt int64   `json:"imported_at,omitempty" bson:"imported_at,omitempty"`
}

type DownloadQueueRequest struct {
	ID        string `json:"id" bson:"_id"`
	CreatorID int64  `json:"creator_id" bson:"creator_id"`

	SpotifyURL string                    `json:"spotify_url" bson:"spotify_url"`
	ObjectType spotify.SpotifyObjectType `json:"object_type" bson:"object_type"`
	Name       string                    `json:"name" bson:"name"`
	Active     bool                      `json:"active" bson:"active"`
	Errored    bool                      `json:"errored" bson:"errored"`

	CreatedAt  int64 `json:"created_at" bson:"created_at"`
	UpdatedAt  int64 `json:"updated_at" bson:"updated_at"`
	SyncCount  int   `json:"sync_count" bson:"sync_count"`
	RetryCount int   `json:"retry_count" bson:"retry_count"`

	// Transitional state-machine fields. Omitempty keeps legacy documents and
	// API responses unchanged until a worker starts using the new lifecycle.
	State          DownloadRequestState   `json:"state,omitempty" bson:"state,omitempty"`
	WorkerID       string                 `json:"worker_id,omitempty" bson:"worker_id,omitempty"`
	ClaimID        string                 `json:"claim_id,omitempty" bson:"claim_id,omitempty"`
	LeaseExpiresAt int64                  `json:"lease_expires_at,omitempty" bson:"lease_expires_at,omitempty"`
	NextAttemptAt  int64                  `json:"next_attempt_at,omitempty" bson:"next_attempt_at,omitempty"`
	Backend        string                 `json:"backend,omitempty" bson:"backend,omitempty"`
	LastError      *DownloadRequestError  `json:"last_error,omitempty" bson:"last_error,omitempty"`
	Result         *DownloadRequestResult `json:"result,omitempty" bson:"result,omitempty"`

	// Track tracking fields
	ExpectedTrackCount int                     `json:"expected_track_count" bson:"expected_track_count"`
	FoundTrackCount    int                     `json:"found_track_count" bson:"found_track_count"`
	TrackMetadata      []spotify.TrackMetadata `json:"track_metadata" bson:"track_metadata"`
}

type PlaylistRequest struct {
	ID         string `json:"id" bson:"_id"`
	CreatorID  int64  `json:"creator_id" bson:"creator_id"`
	SpotifyURL string `json:"spotify_url" bson:"spotify_url"`

	Active     bool `json:"active" bson:"active"`
	Errored    bool `json:"errored" bson:"errored"`
	RetryCount int  `json:"retry_count" bson:"retry_count"`
	// NoPull indicates that the playlist missing songs should not be pulled from Spotify
	NoPull bool `json:"no_pull" bson:"no_pull"`

	CreatedAt int64 `json:"created_at" bson:"created_at"`
	UpdatedAt int64 `json:"updated_at" bson:"updated_at"`
}
