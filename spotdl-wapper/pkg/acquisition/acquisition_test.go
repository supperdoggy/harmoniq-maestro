package acquisition

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type commandCall struct {
	name        string
	args        []string
	hasDeadline bool
}

type fakeRunner struct {
	calls []commandCall
	run   func(ctx context.Context, name string, args ...string) (CommandResult, error)
}

func (f *fakeRunner) Run(
	ctx context.Context,
	name string,
	args ...string,
) (CommandResult, error) {
	_, hasDeadline := ctx.Deadline()
	f.calls = append(f.calls, commandCall{
		name:        name,
		args:        append([]string(nil), args...),
		hasDeadline: hasDeadline,
	})
	if f.run == nil {
		return CommandResult{}, nil
	}
	return f.run(ctx, name, args...)
}

func TestSelectProviderIsExplicit(t *testing.T) {
	spotProvider, err := NewSpotDLProvider(SpotDLConfig{
		OutputDirectory: t.TempDir(),
	}, &fakeRunner{})
	if err != nil {
		t.Fatalf("NewSpotDLProvider() error = %v", err)
	}
	ytProvider, err := NewYTDLPProvider(YTDLPConfig{
		OutputTemplate: filepath.Join(t.TempDir(), "%(id)s.%(ext)s"),
	}, &fakeRunner{})
	if err != nil {
		t.Fatalf("NewYTDLPProvider() error = %v", err)
	}

	selected, err := SelectProvider(ProviderYTDLP, spotProvider, ytProvider)
	if err != nil {
		t.Fatalf("SelectProvider() error = %v", err)
	}
	if selected != ytProvider {
		t.Fatalf("SelectProvider() selected %T, want YTDLPProvider", selected)
	}

	_, err = SelectProvider(ProviderName("missing"), spotProvider, ytProvider)
	if !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("SelectProvider() error = %v, want ErrProviderNotFound", err)
	}
}

func TestSpotDLProviderResolveAndAcquire(t *testing.T) {
	outputDirectory := filepath.Join(t.TempDir(), "new", "staging")
	runner := &fakeRunner{
		run: func(
			_ context.Context,
			_ string,
			args ...string,
		) (CommandResult, error) {
			if _, err := os.Stat(outputDirectory); err != nil {
				return CommandResult{}, fmt.Errorf(
					"output directory missing before command: %w",
					err,
				)
			}
			if !strings.HasSuffix(args[2], ".{output-ext}") {
				return CommandResult{}, fmt.Errorf("spotDL output is not a formatter template: %q", args[2])
			}
			concretePath := strings.ReplaceAll(args[2], "{output-ext}", "flac")
			if err := os.WriteFile(concretePath, []byte("audio bytes"), 0o640); err != nil {
				return CommandResult{}, err
			}
			return CommandResult{}, nil
		},
	}
	provider, err := NewSpotDLProvider(SpotDLConfig{
		Binary:          "spotdl-test",
		OutputDirectory: outputDirectory,
		AudioFormat:     "flac",
		UseConfig:       true,
		DisableCache:    true,
		CommandTimeout:  2 * time.Second,
	}, runner)
	if err != nil {
		t.Fatalf("NewSpotDLProvider() error = %v", err)
	}

	track := TrackSpec{
		ID:       "track;../../unsafe",
		URL:      "https://open.spotify.com/track/track-id?si=x;not-a-command",
		Title:    "A Song",
		Artists:  []string{"An Artist"},
		Duration: 3 * time.Minute,
	}
	candidates, err := provider.Resolve(context.Background(), track)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("Resolve() returned %d candidates, want 1", len(candidates))
	}

	result, err := provider.Acquire(context.Background(), track, candidates[0])
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}

	expectedPath := result.FinalPath
	attemptDirectory := filepath.Dir(expectedPath)
	if filepath.Dir(attemptDirectory) != outputDirectory {
		t.Fatalf("FinalPath = %q, want unique directory beneath %q", expectedPath, outputDirectory)
	}
	if !strings.HasPrefix(
		filepath.Base(attemptDirectory),
		AttemptDirectoryPrefix+"track_unsafe-",
	) {
		t.Fatalf("attempt directory = %q, want sanitized track prefix", attemptDirectory)
	}
	if _, err := os.Stat(filepath.Join(attemptDirectory, AttemptMarkerFilename)); err != nil {
		t.Fatalf("attempt marker missing: %v", err)
	}
	expectedTemplate := filepath.Join(attemptDirectory, "track_unsafe.{output-ext}")
	expectedArgs := []string{
		track.URL,
		"--output", expectedTemplate,
		"--format", "flac",
		"--config",
		"--no-cache",
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner call count = %d, want 1", len(runner.calls))
	}
	if runner.calls[0].name != "spotdl-test" {
		t.Errorf("runner binary = %q, want spotdl-test", runner.calls[0].name)
	}
	if !reflect.DeepEqual(runner.calls[0].args, expectedArgs) {
		t.Errorf("runner args = %#v, want %#v", runner.calls[0].args, expectedArgs)
	}
	if !runner.calls[0].hasDeadline {
		t.Error("runner context has no deadline")
	}

	expectedResult := AssetResult{
		Provider:   ProviderSpotDL,
		SourceID:   track.ID,
		SourceURL:  track.URL,
		FinalPath:  expectedPath,
		Format:     "flac",
		Checksum:   sha256Hex([]byte("audio bytes")),
		MatchScore: 0,
	}
	if !reflect.DeepEqual(result, expectedResult) {
		t.Errorf("Acquire() result = %#v, want %#v", result, expectedResult)
	}
}

func TestSpotDLProviderDerivesSourceID(t *testing.T) {
	provider, err := NewSpotDLProvider(SpotDLConfig{
		OutputDirectory: t.TempDir(),
	}, &fakeRunner{})
	if err != nil {
		t.Fatalf("NewSpotDLProvider() error = %v", err)
	}

	candidates, err := provider.Resolve(context.Background(), TrackSpec{
		URL: "https://open.spotify.com/track/4uLU6hMCjMI75M1A2tKUQC?si=test",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got := candidates[0].SourceID; got != "4uLU6hMCjMI75M1A2tKUQC" {
		t.Errorf("SourceID = %q, want Spotify track ID", got)
	}
}

func TestSpotDLProviderRequiresConcreteOutput(t *testing.T) {
	provider, err := NewSpotDLProvider(SpotDLConfig{
		OutputDirectory: t.TempDir(),
	}, &fakeRunner{})
	if err != nil {
		t.Fatalf("NewSpotDLProvider() error = %v", err)
	}

	_, err = provider.Acquire(context.Background(), TrackSpec{}, Candidate{
		Provider:  ProviderSpotDL,
		SourceID:  "id",
		SourceURL: "https://open.spotify.com/track/id",
	})
	if !errors.Is(err, ErrMissingFinalPath) {
		t.Fatalf("Acquire() error = %v, want ErrMissingFinalPath", err)
	}
}

func TestSpotDLProviderTimeout(t *testing.T) {
	runner := &fakeRunner{
		run: func(
			ctx context.Context,
			_ string,
			_ ...string,
		) (CommandResult, error) {
			<-ctx.Done()
			return CommandResult{}, ctx.Err()
		},
	}
	provider, err := NewSpotDLProvider(SpotDLConfig{
		OutputDirectory: t.TempDir(),
		CommandTimeout:  5 * time.Millisecond,
	}, runner)
	if err != nil {
		t.Fatalf("NewSpotDLProvider() error = %v", err)
	}

	_, err = provider.Acquire(context.Background(), TrackSpec{}, Candidate{
		Provider:  ProviderSpotDL,
		SourceID:  "id",
		SourceURL: "https://open.spotify.com/track/id",
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Acquire() error = %v, want context.DeadlineExceeded", err)
	}
}

func TestProviderCommandErrorPreservesDiagnostic(t *testing.T) {
	runner := &fakeRunner{
		run: func(
			_ context.Context,
			_ string,
			_ ...string,
		) (CommandResult, error) {
			return CommandResult{Stderr: []byte("decoder failed\n")}, errors.New("exit 1")
		},
	}
	provider, err := NewSpotDLProvider(SpotDLConfig{
		OutputDirectory: t.TempDir(),
	}, runner)
	if err != nil {
		t.Fatalf("NewSpotDLProvider() error = %v", err)
	}

	_, err = provider.Acquire(context.Background(), TrackSpec{}, Candidate{
		SourceURL: "https://open.spotify.com/track/id",
	})
	var commandError *CommandError
	if !errors.As(err, &commandError) {
		t.Fatalf("Acquire() error = %T %v, want *CommandError", err, err)
	}
	if commandError.Provider != ProviderSpotDL || commandError.Stderr != "decoder failed" {
		t.Errorf("CommandError = %#v", commandError)
	}
}

func TestYTDLPResolveUsesJSONAndRejectsVariantMismatches(t *testing.T) {
	searchJSON := `{
		"entries": [
			{
				"id": "best",
				"url": "https://www.youtube.com/watch?v=best",
				"title": "The Artist - Great Song (Official Audio)",
				"duration": 201,
				"uploader": "The Artist - Topic",
				"extractor": "youtube"
			},
			{
				"id": "close",
				"url": "https://www.youtube.com/watch?v=close",
				"title": "The Artist - Great Song",
				"duration": 209,
				"uploader": "The Artist"
			},
			{
				"id": "wrong-artist",
				"url": "https://www.youtube.com/watch?v=wrong-artist",
				"title": "Great Song",
				"duration": 201,
				"uploader": "Someone Else"
			},
			{
				"id": "wrong-duration",
				"url": "https://www.youtube.com/watch?v=wrong-duration",
				"title": "The Artist - Great Song",
				"duration": 280,
				"uploader": "The Artist"
			},
			{
				"id": "live",
				"url": "https://www.youtube.com/watch?v=live",
				"title": "The Artist - Great Song (Live)",
				"duration": 201,
				"uploader": "The Artist"
			},
			{
				"id": "remix",
				"url": "https://www.youtube.com/watch?v=remix",
				"title": "The Artist - Great Song Remix",
				"duration": 201,
				"uploader": "The Artist"
			},
			{
				"id": "cover",
				"url": "https://www.youtube.com/watch?v=cover",
				"title": "Great Song (Cover by The Artist Fan)",
				"duration": 201,
				"uploader": "The Artist Fan"
			},
			{
				"id": "nightcore",
				"url": "https://www.youtube.com/watch?v=nightcore",
				"title": "The Artist - Great Song Nightcore",
				"duration": 201,
				"uploader": "The Artist"
			},
			{
				"id": "slowed",
				"url": "https://www.youtube.com/watch?v=slowed",
				"title": "The Artist - Great Song (Slowed + Reverb)",
				"duration": 201,
				"uploader": "The Artist"
			},
			{
				"id": "sped",
				"url": "https://www.youtube.com/watch?v=sped",
				"title": "The Artist - Great Song (Sped Up)",
				"duration": 201,
				"uploader": "The Artist"
			}
		]
	}`
	runner := &fakeRunner{
		run: func(
			_ context.Context,
			_ string,
			_ ...string,
		) (CommandResult, error) {
			return CommandResult{Stdout: []byte(searchJSON)}, nil
		},
	}
	provider, err := NewYTDLPProvider(YTDLPConfig{
		Binary:         "yt-dlp-test",
		OutputTemplate: filepath.Join(t.TempDir(), "%(id)s.%(ext)s"),
		AudioFormat:    "flac",
		SearchLimit:    7,
		CommandTimeout: time.Second,
	}, runner)
	if err != nil {
		t.Fatalf("NewYTDLPProvider() error = %v", err)
	}

	track := TrackSpec{
		Title:    "Great Song",
		Artists:  []string{"The Artist"},
		Duration: 201 * time.Second,
	}
	candidates, err := provider.Resolve(context.Background(), track)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	gotIDs := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		gotIDs = append(gotIDs, candidate.SourceID)
	}
	if want := []string{"best", "close"}; !reflect.DeepEqual(gotIDs, want) {
		t.Errorf("candidate IDs = %#v, want %#v", gotIDs, want)
	}
	if candidates[0].Score <= candidates[1].Score {
		t.Errorf("scores are not descending: %#v", candidates)
	}

	expectedArgs := []string{
		"--dump-single-json",
		"--flat-playlist",
		"--no-warnings",
		"ytsearch7:The Artist Great Song audio",
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner call count = %d, want 1", len(runner.calls))
	}
	if runner.calls[0].name != "yt-dlp-test" {
		t.Errorf("runner binary = %q, want yt-dlp-test", runner.calls[0].name)
	}
	if !reflect.DeepEqual(runner.calls[0].args, expectedArgs) {
		t.Errorf("runner args = %#v, want %#v", runner.calls[0].args, expectedArgs)
	}
	if !runner.calls[0].hasDeadline {
		t.Error("runner context has no deadline")
	}
}

func TestYTDLPResolveAllowsExplicitlyRequestedVariant(t *testing.T) {
	runner := &fakeRunner{
		run: func(
			_ context.Context,
			_ string,
			_ ...string,
		) (CommandResult, error) {
			return CommandResult{Stdout: []byte(`{
				"entries": [{
					"id": "live",
					"url": "https://www.youtube.com/watch?v=live",
					"title": "The Artist - Great Song (Live)",
					"duration": 215,
					"uploader": "The Artist"
				}]
			}`)}, nil
		},
	}
	provider, err := NewYTDLPProvider(YTDLPConfig{
		OutputTemplate: filepath.Join(t.TempDir(), "%(id)s.%(ext)s"),
	}, runner)
	if err != nil {
		t.Fatalf("NewYTDLPProvider() error = %v", err)
	}

	candidates, err := provider.Resolve(context.Background(), TrackSpec{
		Title:           "Great Song",
		Artists:         []string{"The Artist"},
		Duration:        215 * time.Second,
		AllowedVariants: []Variant{VariantLive},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(candidates) != 1 || candidates[0].SourceID != "live" {
		t.Fatalf("Resolve() candidates = %#v, want requested live candidate", candidates)
	}
}

func TestYTDLPResolveBuildsYouTubeURLFromFlatEntry(t *testing.T) {
	runner := &fakeRunner{
		run: func(
			_ context.Context,
			_ string,
			_ ...string,
		) (CommandResult, error) {
			return CommandResult{Stdout: []byte(`{
				"entries": [{
					"id": "video-id",
					"url": "video-id",
					"title": "Artist Song",
					"uploader": "Artist",
					"extractor_key": "Youtube"
				}]
			}`)}, nil
		},
	}
	provider, err := NewYTDLPProvider(YTDLPConfig{
		OutputTemplate: filepath.Join(t.TempDir(), "%(id)s.%(ext)s"),
	}, runner)
	if err != nil {
		t.Fatalf("NewYTDLPProvider() error = %v", err)
	}

	candidates, err := provider.Resolve(context.Background(), TrackSpec{
		Title:   "Song",
		Artists: []string{"Artist"},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got, want := candidates[0].SourceURL,
		"https://www.youtube.com/watch?v=video-id"; got != want {
		t.Errorf("SourceURL = %q, want %q", got, want)
	}
}

func TestYTDLPResolveReturnsNoCandidatesForOnlyMismatch(t *testing.T) {
	runner := &fakeRunner{
		run: func(
			_ context.Context,
			_ string,
			_ ...string,
		) (CommandResult, error) {
			return CommandResult{Stdout: []byte(`{
				"entries": [{
					"id": "cover",
					"url": "https://www.youtube.com/watch?v=cover",
					"title": "A Completely Different Song (Cover)",
					"uploader": "Someone Else"
				}]
			}`)}, nil
		},
	}
	provider, err := NewYTDLPProvider(YTDLPConfig{
		OutputTemplate: filepath.Join(t.TempDir(), "%(id)s.%(ext)s"),
	}, runner)
	if err != nil {
		t.Fatalf("NewYTDLPProvider() error = %v", err)
	}

	_, err = provider.Resolve(context.Background(), TrackSpec{
		Title:   "Wanted Song",
		Artists: []string{"Wanted Artist"},
	})
	if !errors.Is(err, ErrNoCandidates) {
		t.Fatalf("Resolve() error = %v, want ErrNoCandidates", err)
	}
}

func TestYTDLPAcquireUsesAfterMovePath(t *testing.T) {
	stagingDirectory := filepath.Join(t.TempDir(), "new", "staging")
	runner := &fakeRunner{
		run: func(
			_ context.Context,
			_ string,
			args ...string,
		) (CommandResult, error) {
			if _, err := os.Stat(stagingDirectory); err != nil {
				return CommandResult{}, fmt.Errorf(
					"output directory missing before command: %w",
					err,
				)
			}
			outputTemplate := args[8]
			concretePath := filepath.Join(filepath.Dir(outputTemplate), "Artist - Song.ogg")
			if err := os.WriteFile(concretePath, []byte("audio bytes"), 0o640); err != nil {
				return CommandResult{}, err
			}
			return CommandResult{
				Stdout: []byte("\n" + concretePath + "\r\n"),
			}, nil
		},
	}
	outputTemplate := filepath.Join(stagingDirectory, "%(id)s.%(ext)s")
	provider, err := NewYTDLPProvider(YTDLPConfig{
		Binary:         "yt-dlp-test",
		OutputTemplate: outputTemplate,
		AudioFormat:    "ogg",
	}, runner)
	if err != nil {
		t.Fatalf("NewYTDLPProvider() error = %v", err)
	}
	candidate := Candidate{
		Provider:  ProviderYTDLP,
		SourceID:  "video-id",
		SourceURL: "https://www.youtube.com/watch?v=video-id&list=not-a-shell",
		Score:     0.982,
	}

	result, err := provider.Acquire(context.Background(), TrackSpec{}, candidate)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}

	attemptTemplate := runner.calls[0].args[8]
	if filepath.Dir(filepath.Dir(attemptTemplate)) != stagingDirectory {
		t.Fatalf("attempt template = %q, want private directory under staging", attemptTemplate)
	}
	if !strings.HasPrefix(
		filepath.Base(filepath.Dir(attemptTemplate)),
		AttemptDirectoryPrefix,
	) {
		t.Fatalf("attempt template = %q, want owned attempt prefix", attemptTemplate)
	}
	expectedArgs := []string{
		"--quiet",
		"--no-warnings",
		"--no-playlist",
		"--no-simulate",
		"--extract-audio",
		"--audio-format", "vorbis",
		"--output", attemptTemplate,
		"--print", "after_move:filepath",
		candidate.SourceURL,
	}
	if !reflect.DeepEqual(runner.calls[0].args, expectedArgs) {
		t.Errorf("runner args = %#v, want %#v", runner.calls[0].args, expectedArgs)
	}
	if got, want := result.FinalPath,
		filepath.Join(filepath.Dir(attemptTemplate), "Artist - Song.ogg"); got != want {
		t.Errorf("FinalPath = %q, want %q", got, want)
	}
	if result.Provider != ProviderYTDLP ||
		result.SourceID != candidate.SourceID ||
		result.Format != "ogg" ||
		result.MatchScore != candidate.Score {
		t.Errorf("Acquire() result = %#v", result)
	}
}

func TestYTDLPAcquireRequiresReportedPath(t *testing.T) {
	provider, err := NewYTDLPProvider(YTDLPConfig{
		OutputTemplate: filepath.Join(t.TempDir(), "%(id)s.%(ext)s"),
	}, &fakeRunner{})
	if err != nil {
		t.Fatalf("NewYTDLPProvider() error = %v", err)
	}

	_, err = provider.Acquire(context.Background(), TrackSpec{}, Candidate{
		SourceURL: "https://www.youtube.com/watch?v=id",
	})
	if !errors.Is(err, ErrMissingFinalPath) {
		t.Fatalf("Acquire() error = %v, want ErrMissingFinalPath", err)
	}
}

func TestYTDLPAcquireRejectsReportedPathOutsideAttempt(t *testing.T) {
	stagingDirectory := t.TempDir()
	outsidePath := filepath.Join(t.TempDir(), "outside.mp3")
	if err := os.WriteFile(outsidePath, []byte("audio"), 0o640); err != nil {
		t.Fatal(err)
	}
	provider, err := NewYTDLPProvider(YTDLPConfig{
		OutputTemplate: filepath.Join(stagingDirectory, "%(id)s.%(ext)s"),
	}, &fakeRunner{run: func(
		context.Context,
		string,
		...string,
	) (CommandResult, error) {
		return CommandResult{Stdout: []byte(outsidePath)}, nil
	}})
	if err != nil {
		t.Fatalf("NewYTDLPProvider() error = %v", err)
	}

	_, err = provider.Acquire(context.Background(), TrackSpec{}, Candidate{
		Provider:  ProviderYTDLP,
		SourceURL: "https://www.youtube.com/watch?v=id",
	})
	if !errors.Is(err, ErrUnsafeFinalPath) {
		t.Fatalf("Acquire() error = %v, want ErrUnsafeFinalPath", err)
	}
}

func TestYTDLPResolveRejectsMalformedJSON(t *testing.T) {
	runner := &fakeRunner{
		run: func(
			_ context.Context,
			_ string,
			_ ...string,
		) (CommandResult, error) {
			return CommandResult{Stdout: []byte("{not-json")}, nil
		},
	}
	provider, err := NewYTDLPProvider(YTDLPConfig{
		OutputTemplate: filepath.Join(t.TempDir(), "%(id)s.%(ext)s"),
	}, runner)
	if err != nil {
		t.Fatalf("NewYTDLPProvider() error = %v", err)
	}

	_, err = provider.Resolve(context.Background(), TrackSpec{Title: "Song"})
	if err == nil || !strings.Contains(err.Error(), "candidate JSON") {
		t.Fatalf("Resolve() error = %v, want JSON decoding error", err)
	}
}

func TestConstructorsValidateConfiguration(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "spotdl output directory",
			run: func() error {
				_, err := NewSpotDLProvider(SpotDLConfig{}, &fakeRunner{})
				return err
			},
		},
		{
			name: "spotdl format",
			run: func() error {
				_, err := NewSpotDLProvider(SpotDLConfig{
					OutputDirectory: t.TempDir(),
					AudioFormat:     "mp3;command",
				}, &fakeRunner{})
				return err
			},
		},
		{
			name: "yt-dlp output template",
			run: func() error {
				_, err := NewYTDLPProvider(YTDLPConfig{}, &fakeRunner{})
				return err
			},
		},
		{
			name: "yt-dlp search limit",
			run: func() error {
				_, err := NewYTDLPProvider(YTDLPConfig{
					OutputTemplate: "output",
					SearchLimit:    51,
				}, &fakeRunner{})
				return err
			},
		},
		{
			name: "yt-dlp score",
			run: func() error {
				_, err := NewYTDLPProvider(YTDLPConfig{
					OutputTemplate: "output",
					MinimumScore:   1.1,
				}, &fakeRunner{})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); err == nil {
				t.Fatal("constructor error = nil, want validation error")
			}
		})
	}
}

func TestNormalizeAudioFormatWhitelist(t *testing.T) {
	valid := map[string]string{
		"":      "mp3",
		" MP3 ": "mp3",
		".FLAC": "flac",
		"ogg":   "ogg",
		"opus":  "opus",
		"m4a":   "m4a",
		"wav":   "wav",
	}
	for input, want := range valid {
		t.Run("valid_"+want, func(t *testing.T) {
			got, err := normalizeAudioFormat(input)
			if err != nil {
				t.Fatalf("normalizeAudioFormat(%q) error = %v", input, err)
			}
			if got != want {
				t.Fatalf("normalizeAudioFormat(%q) = %q, want %q", input, got, want)
			}
		})
	}

	for _, input := range []string{"aac", "best", "vorbis", "mp3;command"} {
		t.Run("invalid_"+input, func(t *testing.T) {
			if _, err := normalizeAudioFormat(input); err == nil {
				t.Fatalf("normalizeAudioFormat(%q) error = nil, want unsupported format", input)
			}
		})
	}
}

func TestYTDLPProviderAllowsZeroMinimumScore(t *testing.T) {
	provider, err := NewYTDLPProvider(YTDLPConfig{
		OutputTemplate: filepath.Join(t.TempDir(), "%(id)s.%(ext)s"),
		MinimumScore:   0,
	}, &fakeRunner{})
	if err != nil {
		t.Fatalf("NewYTDLPProvider() error = %v", err)
	}
	if provider.minimumScore != 0 {
		t.Fatalf("minimumScore = %v, want explicit zero", provider.minimumScore)
	}
}

func TestCommandTimeoutWrapsParentCancellation(t *testing.T) {
	runner := &fakeRunner{
		run: func(
			ctx context.Context,
			_ string,
			_ ...string,
		) (CommandResult, error) {
			<-ctx.Done()
			return CommandResult{}, ctx.Err()
		},
	}
	provider, err := NewYTDLPProvider(YTDLPConfig{
		OutputTemplate: filepath.Join(t.TempDir(), "%(id)s.%(ext)s"),
	}, runner)
	if err != nil {
		t.Fatalf("NewYTDLPProvider() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = provider.Resolve(ctx, TrackSpec{Title: "Song"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Resolve() error = %v, want context.Canceled", err)
	}
}

func TestScoringRejectsStrictVersionMarkersUnlessRequested(t *testing.T) {
	strictVariants := []struct {
		variant Variant
		title   string
	}{
		{VariantLive, "Artist - Song (Live)"},
		{VariantRemix, "Artist - Song Remix"},
		{VariantCover, "Artist - Song Cover"},
		{VariantNightcore, "Artist - Song Nightcore"},
		{VariantSlowed, "Artist - Song Slowed"},
		{VariantSpedUp, "Artist - Song Sped Up"},
	}

	for _, test := range strictVariants {
		t.Run(string(test.variant), func(t *testing.T) {
			candidate := Candidate{
				Title:    test.title,
				Artists:  []string{"Artist"},
				Duration: 3 * time.Minute,
			}
			track := TrackSpec{
				Title:    "Song",
				Artists:  []string{"Artist"},
				Duration: 3 * time.Minute,
			}

			if score, rejection := scoreCandidate(track, candidate); score != 0 ||
				rejection == "" {
				t.Fatalf(
					"unrequested variant score = %v, rejection = %q",
					score,
					rejection,
				)
			}

			track.AllowedVariants = []Variant{test.variant}
			if score, rejection := scoreCandidate(track, candidate); score == 0 ||
				rejection != "" {
				t.Fatalf(
					"requested variant score = %v, rejection = %q",
					score,
					rejection,
				)
			}
		})
	}
}

func TestCommandErrorIsBounded(t *testing.T) {
	diagnostic := strings.Repeat("x", 5000)
	err := &CommandError{
		Provider: ProviderYTDLP,
		Err:      fmt.Errorf("exit"),
		Stderr:   boundedDiagnostic([]byte(diagnostic)),
	}
	if len(err.Stderr) > 4100 || !strings.HasSuffix(err.Stderr, "…") {
		t.Fatalf("bounded stderr length = %d, suffix missing", len(err.Stderr))
	}
}

func TestExecCommandRunnerCapsCapturedOutput(t *testing.T) {
	const helperEnvironment = "HARMONIQ_EXEC_RUNNER_CAP_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		if _, err := fmt.Fprint(
			os.Stdout,
			strings.Repeat("o", maxCommandStdoutBytes+1024),
		); err != nil {
			os.Exit(2)
		}
		if _, err := fmt.Fprint(
			os.Stderr,
			strings.Repeat("e", maxCommandStderrBytes+1024),
		); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}

	t.Setenv(helperEnvironment, "1")
	result, err := (ExecCommandRunner{}).Run(
		context.Background(),
		os.Args[0],
		"-test.run=^TestExecCommandRunnerCapsCapturedOutput$",
	)
	if err != nil {
		t.Fatalf("ExecCommandRunner.Run() error = %v", err)
	}
	if len(result.Stdout) != maxCommandStdoutBytes {
		t.Fatalf("stdout bytes = %d, want %d", len(result.Stdout), maxCommandStdoutBytes)
	}
	if len(result.Stderr) != maxCommandStderrBytes {
		t.Fatalf("stderr bytes = %d, want %d", len(result.Stderr), maxCommandStderrBytes)
	}
	if result.Stdout[0] != 'o' || result.Stdout[len(result.Stdout)-1] != 'o' {
		t.Fatal("stdout capture contains unexpected data")
	}
	if result.Stderr[0] != 'e' || result.Stderr[len(result.Stderr)-1] != 'e' {
		t.Fatal("stderr capture contains unexpected data")
	}
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
