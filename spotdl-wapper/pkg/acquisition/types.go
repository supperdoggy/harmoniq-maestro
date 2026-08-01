// Package acquisition defines provider-neutral music acquisition primitives.
//
// Providers deliberately separate candidate resolution from acquisition. This
// lets a caller inspect or persist a match before allowing a downloader to
// create a file.
package acquisition

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ProviderName identifies an acquisition implementation.
type ProviderName string

const (
	ProviderSpotDL ProviderName = "spotdl"
	ProviderYTDLP  ProviderName = "yt-dlp"

	// AttemptDirectoryPrefix and AttemptMarkerFilename identify private
	// provider staging directories owned by this worker. Import cleanup uses
	// both values so it never sweeps arbitrary user directories.
	AttemptDirectoryPrefix = ".harmoniq-attempt-"
	AttemptMarkerFilename  = ".harmoniq-owned-attempt"
)

// Variant describes a materially different recording or edit.
type Variant string

const (
	VariantLive         Variant = "live"
	VariantRemix        Variant = "remix"
	VariantCover        Variant = "cover"
	VariantNightcore    Variant = "nightcore"
	VariantSlowed       Variant = "slowed"
	VariantSpedUp       Variant = "sped-up"
	VariantAcoustic     Variant = "acoustic"
	VariantInstrumental Variant = "instrumental"
	VariantRemaster     Variant = "remaster"
	VariantDemo         Variant = "demo"
	VariantEdit         Variant = "edit"
)

// TrackSpec is the provider-neutral description of the wanted recording.
//
// ID and URL refer to the canonical metadata source (currently normally
// Spotify), not necessarily to the service from which a provider obtains the
// media. AllowedVariants is an explicit opt-in to alternate recordings whose
// marker is not already present in Title or Version.
type TrackSpec struct {
	ID              string
	ISRC            string
	URL             string
	Title           string
	Artists         []string
	Album           string
	Duration        time.Duration
	Version         string
	Explicit        bool
	AllowedVariants []Variant
}

// Candidate is a provider-specific source matched to a TrackSpec.
type Candidate struct {
	Provider  ProviderName
	SourceID  string
	SourceURL string
	Title     string
	Artists   []string
	Duration  time.Duration
	Format    string
	Uploader  string
	Score     float64
	Reasons   []string
}

// AssetResult is the structured result of a successful acquisition. Checksum
// is a lowercase hexadecimal SHA-256 digest when the provider can read the
// completed file, or empty when an external runner did not materialize it.
type AssetResult struct {
	Provider   ProviderName
	SourceID   string
	SourceURL  string
	FinalPath  string
	Format     string
	Checksum   string
	MatchScore float64
}

// Provider resolves and acquires tracks from one underlying implementation.
type Provider interface {
	Name() ProviderName
	Resolve(ctx context.Context, track TrackSpec) ([]Candidate, error)
	Acquire(ctx context.Context, track TrackSpec, candidate Candidate) (AssetResult, error)
}

var (
	// ErrNoCandidates means resolution completed but found no acceptable match.
	ErrNoCandidates = errors.New("no acceptable acquisition candidates")
	// ErrProviderNotFound means the requested provider was not supplied.
	ErrProviderNotFound = errors.New("acquisition provider not found")
	// ErrMissingFinalPath means a downloader succeeded without reporting a path.
	ErrMissingFinalPath = errors.New("downloader did not report a final path")
	// ErrUnsafeFinalPath means a downloader reported an artifact outside its
	// private staging attempt.
	ErrUnsafeFinalPath = errors.New("downloader reported an unsafe final path")
)

// SelectProvider makes provider choice explicit. It never falls back to a
// different implementation when the requested provider is unavailable.
func SelectProvider(name ProviderName, providers ...Provider) (Provider, error) {
	for _, provider := range providers {
		if provider != nil && provider.Name() == name {
			return provider, nil
		}
	}

	return nil, fmt.Errorf("%w: %s", ErrProviderNotFound, name)
}

func validateCandidate(provider ProviderName, candidate Candidate) error {
	if candidate.Provider != "" && candidate.Provider != provider {
		return fmt.Errorf(
			"candidate belongs to provider %q, not %q",
			candidate.Provider,
			provider,
		)
	}
	if strings.TrimSpace(candidate.SourceURL) == "" {
		return errors.New("candidate source URL is required")
	}
	return nil
}
