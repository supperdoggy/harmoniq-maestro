package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/supperdoggy/SmartHomeServer/harmoniq-maestro/spotdl-wapper/pkg/acquisition"
)

type mediaRunner struct {
	duration string
	calls    []string
}

func (r *mediaRunner) Run(
	ctx context.Context,
	name string,
	args ...string,
) (acquisition.CommandResult, error) {
	if _, ok := ctx.Deadline(); !ok {
		return acquisition.CommandResult{}, errors.New("command context has no deadline")
	}
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))

	switch name {
	case "ffmpeg-test":
		var source string
		for index := range args {
			if args[index] == "-i" && index+1 < len(args) {
				source = args[index+1]
			}
		}
		if source == "" || len(args) == 0 {
			return acquisition.CommandResult{}, errors.New("missing ffmpeg paths")
		}
		data, err := os.ReadFile(source)
		if err != nil {
			return acquisition.CommandResult{}, err
		}
		if err := os.WriteFile(args[len(args)-1], data, 0o640); err != nil {
			return acquisition.CommandResult{}, err
		}
		return acquisition.CommandResult{}, nil
	case "ffprobe-test":
		duration := r.duration
		if duration == "" {
			duration = "180.0"
		}
		return acquisition.CommandResult{Stdout: []byte(
			`{"streams":[{"codec_name":"mp3"}],"format":{"duration":"` +
				duration +
				`","format_name":"mp3"}}`,
		)}, nil
	default:
		return acquisition.CommandResult{}, errors.New("unexpected binary " + name)
	}
}

func newStagedAsset(t *testing.T, stagingRoot, name string, data []byte) string {
	t.Helper()
	if err := os.MkdirAll(stagingRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	attempt, err := os.MkdirTemp(stagingRoot, acquisition.AttemptDirectoryPrefix+"test-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(attempt, acquisition.AttemptMarkerFilename),
		nil,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(attempt, name)
	if err := os.WriteFile(source, data, 0o640); err != nil {
		t.Fatal(err)
	}
	return source
}

func TestImporterPublishesCanonicalCatalogItem(t *testing.T) {
	libraryRoot := t.TempDir()
	stagingRoot := filepath.Join(libraryRoot, ".staging")
	source := newStagedAsset(t, stagingRoot, "source.mp3", []byte("audio bytes"))

	runner := &mediaRunner{}
	importer, err := NewImporter(ImporterConfig{
		LibraryRoot:       libraryRoot,
		StagingRoot:       stagingRoot,
		OutputTemplate:    filepath.Join(libraryRoot, "downloads", "{artists}-{title}.{output-ext}"),
		FFmpegBinary:      "ffmpeg-test",
		FFprobeBinary:     "ffprobe-test",
		CommandTimeout:    time.Second,
		DurationTolerance: 5 * time.Second,
	}, runner)
	if err != nil {
		t.Fatalf("NewImporter() error = %v", err)
	}

	track := acquisition.TrackSpec{
		ID:       "spotify-id",
		URL:      "https://open.spotify.com/track/spotify-id",
		ISRC:     "TESTISRC",
		Title:    "Song / Name",
		Artists:  []string{"Artist"},
		Album:    "Album",
		Duration: 180 * time.Second,
		Explicit: true,
	}
	asset := acquisition.AssetResult{
		Provider:   acquisition.ProviderYTDLP,
		SourceID:   "video-id",
		SourceURL:  "https://www.youtube.com/watch?v=video-id",
		FinalPath:  source,
		Format:     "mp3",
		MatchScore: 0.96,
	}

	file, err := importer.Import(context.Background(), track, asset)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	expectedPath := filepath.Join(libraryRoot, "downloads", "Artist-Song _ Name.mp3")
	if file.Path != expectedPath {
		t.Fatalf("Path = %q, want %q", file.Path, expectedPath)
	}
	if file.SpotifyID != track.ID || file.ISRC != track.ISRC {
		t.Fatalf("canonical identity not preserved: %#v", file)
	}
	if file.SourceProvider != "yt-dlp" || file.SourceID != "video-id" {
		t.Fatalf("source provenance not preserved: %#v", file)
	}
	if file.Checksum == "" || file.DurationMS != 180000 {
		t.Fatalf("validation result not preserved: %#v", file)
	}
	if _, err := os.Stat(file.Path); err != nil {
		t.Fatalf("published file missing: %v", err)
	}
	published, err := os.Stat(file.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := published.Mode().Perm(); got != publishedFileMode {
		t.Fatalf("published mode = %#o, want %#o", got, publishedFileMode)
	}
	if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged source still exists or unexpected error: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(source)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned attempt directory still exists or unexpected error: %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("runner calls = %d, want ffmpeg and ffprobe", len(runner.calls))
	}
	if !strings.Contains(runner.calls[0], "-fflags +bitexact") {
		t.Fatalf("ffmpeg args are not deterministic: %q", runner.calls[0])
	}
}

func TestImporterRejectsProviderChecksumMismatch(t *testing.T) {
	libraryRoot := t.TempDir()
	stagingRoot := filepath.Join(libraryRoot, ".staging")
	source := newStagedAsset(t, stagingRoot, "source.mp3", []byte("mutated bytes"))

	runner := &mediaRunner{}
	importer, err := NewImporter(ImporterConfig{
		LibraryRoot:    libraryRoot,
		StagingRoot:    stagingRoot,
		OutputTemplate: filepath.Join(libraryRoot, "{title}.{output-ext}"),
	}, runner)
	if err != nil {
		t.Fatal(err)
	}

	_, err = importer.Import(context.Background(), acquisition.TrackSpec{Title: "Song"}, acquisition.AssetResult{
		FinalPath: source,
		Format:    "mp3",
		Checksum:  strings.Repeat("0", sha256.Size*2),
	})
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("Import() error = %v, want ErrChecksumMismatch", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("media commands ran after checksum mismatch: %v", runner.calls)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source should remain owned by caller after rejection: %v", err)
	}
}

func TestImporterRejectsDurationMismatchWithoutRemovingSource(t *testing.T) {
	libraryRoot := t.TempDir()
	stagingRoot := filepath.Join(libraryRoot, ".staging")
	source := newStagedAsset(t, stagingRoot, "source.mp3", []byte("audio bytes"))

	importer, err := NewImporter(ImporterConfig{
		LibraryRoot:       libraryRoot,
		StagingRoot:       stagingRoot,
		OutputTemplate:    filepath.Join(libraryRoot, "{title}.{output-ext}"),
		FFmpegBinary:      "ffmpeg-test",
		FFprobeBinary:     "ffprobe-test",
		DurationTolerance: 5 * time.Second,
	}, &mediaRunner{duration: "260.0"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = importer.Import(context.Background(), acquisition.TrackSpec{
		Title:    "Song",
		Duration: 180 * time.Second,
	}, acquisition.AssetResult{
		Provider:  acquisition.ProviderYTDLP,
		SourceID:  "source",
		FinalPath: source,
		Format:    "mp3",
	})
	if !errors.Is(err, ErrDurationMismatch) {
		t.Fatalf("Import() error = %v, want ErrDurationMismatch", err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source should remain retryable: %v", err)
	}
}

func TestImporterRejectsOutputOutsideLibrary(t *testing.T) {
	parent := t.TempDir()
	libraryRoot := filepath.Join(parent, "library")
	stagingRoot := filepath.Join(libraryRoot, ".staging")
	source := newStagedAsset(t, stagingRoot, "source.mp3", []byte("audio"))

	importer, err := NewImporter(ImporterConfig{
		LibraryRoot:    libraryRoot,
		StagingRoot:    stagingRoot,
		OutputTemplate: filepath.Join(parent, "escaped", "{title}.{output-ext}"),
	}, &mediaRunner{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = importer.Import(context.Background(), acquisition.TrackSpec{Title: "Song"}, acquisition.AssetResult{
		FinalPath: source,
		Format:    "mp3",
	})
	if !errors.Is(err, ErrUnsafeOutputPath) {
		t.Fatalf("Import() error = %v, want ErrUnsafeOutputPath", err)
	}
}

func TestImporterRejectsSymlinkedStagingAsset(t *testing.T) {
	libraryRoot := t.TempDir()
	stagingRoot := filepath.Join(libraryRoot, ".staging")
	outsideRoot := t.TempDir()
	outsideSource := filepath.Join(outsideRoot, "source.mp3")
	if err := os.WriteFile(outsideSource, []byte("audio"), 0o640); err != nil {
		t.Fatal(err)
	}
	source := newStagedAsset(t, stagingRoot, "source.mp3", []byte("placeholder"))
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideSource, source); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}

	importer, err := NewImporter(ImporterConfig{
		LibraryRoot:    libraryRoot,
		StagingRoot:    stagingRoot,
		OutputTemplate: filepath.Join(libraryRoot, "{title}.{output-ext}"),
	}, &mediaRunner{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = importer.Import(context.Background(), acquisition.TrackSpec{Title: "Song"}, acquisition.AssetResult{
		FinalPath: source,
		Format:    "mp3",
	})
	if !errors.Is(err, ErrInvalidAsset) {
		t.Fatalf("Import() error = %v, want ErrInvalidAsset", err)
	}
}

func TestImporterRejectsSymlinkedOutputDirectoryEscape(t *testing.T) {
	libraryRoot := t.TempDir()
	stagingRoot := filepath.Join(libraryRoot, ".staging")
	outsideRoot := t.TempDir()
	if err := os.Symlink(outsideRoot, filepath.Join(libraryRoot, "downloads")); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	source := newStagedAsset(t, stagingRoot, "source.mp3", []byte("audio"))

	importer, err := NewImporter(ImporterConfig{
		LibraryRoot:    libraryRoot,
		StagingRoot:    stagingRoot,
		OutputTemplate: filepath.Join(libraryRoot, "downloads", "{title}.{output-ext}"),
	}, &mediaRunner{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = importer.Import(context.Background(), acquisition.TrackSpec{Title: "Song"}, acquisition.AssetResult{
		FinalPath: source,
		Format:    "mp3",
	})
	if !errors.Is(err, ErrUnsafeOutputPath) {
		t.Fatalf("Import() error = %v, want ErrUnsafeOutputPath", err)
	}
}

func TestImporterIsIdempotentForMatchingContent(t *testing.T) {
	libraryRoot := t.TempDir()
	stagingRoot := filepath.Join(libraryRoot, ".staging")
	if err := os.MkdirAll(stagingRoot, 0o750); err != nil {
		t.Fatal(err)
	}

	importer, err := NewImporter(ImporterConfig{
		LibraryRoot:    libraryRoot,
		StagingRoot:    stagingRoot,
		OutputTemplate: filepath.Join(libraryRoot, "{spotify-id}.{output-ext}"),
		FFmpegBinary:   "ffmpeg-test",
		FFprobeBinary:  "ffprobe-test",
	}, &mediaRunner{})
	if err != nil {
		t.Fatal(err)
	}

	track := acquisition.TrackSpec{ID: "spotify-id", Title: "Song", Duration: 180 * time.Second}
	var firstPath string
	for attempt := 0; attempt < 2; attempt++ {
		source := newStagedAsset(
			t,
			stagingRoot,
			"source-"+string(rune('a'+attempt))+".mp3",
			[]byte("same audio"),
		)
		file, err := importer.Import(context.Background(), track, acquisition.AssetResult{
			FinalPath: source,
			Format:    "mp3",
		})
		if err != nil {
			t.Fatalf("Import() attempt %d error = %v", attempt+1, err)
		}
		if attempt == 0 {
			firstPath = file.Path
		} else if file.Path != firstPath {
			t.Fatalf("idempotent path = %q, want %q", file.Path, firstPath)
		}
	}
}

func TestPublishMediaAcceptsChecksumFallbackAndConcurrentWinner(t *testing.T) {
	root := t.TempDir()
	tagged := filepath.Join(root, "tagged.mp3")
	content := []byte("canonical audio")
	if err := os.WriteFile(tagged, content, publishedFileMode); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	checksum := hex.EncodeToString(sum[:])
	desired := filepath.Join(root, "song.mp3")

	for _, occupied := range []string{
		desired,
		appendFilenameSuffix(desired, "spotify-id"),
	} {
		if err := os.WriteFile(occupied, []byte("different "+occupied), publishedFileMode); err != nil {
			t.Fatal(err)
		}
	}
	checksumPath := appendFilenameSuffix(desired, checksum[:12])
	if err := os.WriteFile(checksumPath, content, publishedFileMode); err != nil {
		t.Fatal(err)
	}

	path, alreadyPresent, err := publishMedia(tagged, desired, "spotify-id", "source-id", checksum)
	if err != nil {
		t.Fatalf("publishMedia() error = %v", err)
	}
	if path != checksumPath || !alreadyPresent {
		t.Fatalf("publishMedia() = (%q, %t), want (%q, true)", path, alreadyPresent, checksumPath)
	}

	concurrentDesired := filepath.Join(root, "concurrent.mp3")
	const workers = 8
	results := make(chan string, workers)
	errorsFound := make(chan error, workers)
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			path, _, err := publishMedia(
				tagged,
				concurrentDesired,
				"spotify-id",
				"source-id",
				checksum,
			)
			if err != nil {
				errorsFound <- err
				return
			}
			results <- path
		}()
	}
	wait.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("concurrent publishMedia() error = %v", err)
	}
	for path := range results {
		if path != concurrentDesired {
			t.Errorf("concurrent publish path = %q, want %q", path, concurrentDesired)
		}
	}
}

func TestPublishMediaRejectsSymlinkCollision(t *testing.T) {
	root := t.TempDir()
	tagged := filepath.Join(root, "tagged.mp3")
	outside := filepath.Join(root, "outside.mp3")
	desired := filepath.Join(root, "song.mp3")
	if err := os.WriteFile(tagged, []byte("canonical"), publishedFileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("outside"), publishedFileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, desired); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	sum := sha256.Sum256([]byte("canonical"))

	_, _, err := publishMedia(tagged, desired, "spotify-id", "source-id", hex.EncodeToString(sum[:]))
	if err == nil {
		t.Fatal("publishMedia() error = nil, want symbolic-link rejection")
	}
}

func TestDiscardAndCleanupOrphansStayWithinStaging(t *testing.T) {
	root := t.TempDir()
	stagingRoot := filepath.Join(root, ".staging")
	if err := os.MkdirAll(stagingRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	importer, err := NewImporter(ImporterConfig{
		LibraryRoot:    root,
		StagingRoot:    stagingRoot,
		OutputTemplate: filepath.Join(root, "{title}.{output-ext}"),
	}, &mediaRunner{})
	if err != nil {
		t.Fatal(err)
	}

	source := newStagedAsset(t, stagingRoot, "source.mp3", []byte("audio"))
	attempt := filepath.Dir(source)
	if err := importer.Discard(acquisition.AssetResult{FinalPath: source}); err != nil {
		t.Fatalf("Discard() error = %v", err)
	}
	if _, err := os.Stat(attempt); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("attempt directory still exists or unexpected error: %v", err)
	}

	oldAttempt := filepath.Join(stagingRoot, acquisition.AttemptDirectoryPrefix+"old")
	newAttempt := filepath.Join(stagingRoot, acquisition.AttemptDirectoryPrefix+"new")
	for _, directory := range []string{oldAttempt, newAttempt} {
		if err := os.Mkdir(directory, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(directory, acquisition.AttemptMarkerFilename),
			nil,
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "source.mp3"), []byte("audio"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	unownedOldDirectory := filepath.Join(stagingRoot, "user-media")
	if err := os.Mkdir(unownedOldDirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-4 * time.Hour)
	if err := os.Chtimes(oldAttempt, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(unownedOldDirectory, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	removed, err := importer.CleanupOrphans(time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("CleanupOrphans() error = %v", err)
	}
	if removed != 1 {
		t.Fatalf("CleanupOrphans() removed = %d, want 1", removed)
	}
	if _, err := os.Stat(oldAttempt); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old attempt still exists or unexpected error: %v", err)
	}
	if _, err := os.Stat(newAttempt); err != nil {
		t.Fatalf("new attempt was removed: %v", err)
	}
	if _, err := os.Stat(unownedOldDirectory); err != nil {
		t.Fatalf("unowned directory was removed: %v", err)
	}
}

func TestDiscardRejectsSymlinkedAttemptWithoutDeletingOutsideFile(t *testing.T) {
	root := t.TempDir()
	stagingRoot := filepath.Join(root, ".staging")
	outsideRoot := t.TempDir()
	if err := os.MkdirAll(stagingRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	outsideAttempt := filepath.Join(outsideRoot, acquisition.AttemptDirectoryPrefix+"outside")
	if err := os.Mkdir(outsideAttempt, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(outsideAttempt, acquisition.AttemptMarkerFilename),
		nil,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	outsideSource := filepath.Join(outsideAttempt, "source.mp3")
	if err := os.WriteFile(outsideSource, []byte("outside audio"), 0o640); err != nil {
		t.Fatal(err)
	}
	symlinkedAttempt := filepath.Join(stagingRoot, acquisition.AttemptDirectoryPrefix+"linked")
	if err := os.Symlink(outsideAttempt, symlinkedAttempt); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}

	importer, err := NewImporter(ImporterConfig{
		LibraryRoot:    root,
		StagingRoot:    stagingRoot,
		OutputTemplate: filepath.Join(root, "{title}.{output-ext}"),
	}, &mediaRunner{})
	if err != nil {
		t.Fatal(err)
	}
	err = importer.Discard(acquisition.AssetResult{
		FinalPath: filepath.Join(symlinkedAttempt, "source.mp3"),
	})
	if !errors.Is(err, ErrInvalidAsset) {
		t.Fatalf("Discard() error = %v, want ErrInvalidAsset", err)
	}
	if _, err := os.Stat(outsideSource); err != nil {
		t.Fatalf("outside file was deleted: %v", err)
	}
}

func TestNewImporterRejectsStagingOutputOverlap(t *testing.T) {
	root := t.TempDir()
	for _, test := range []struct {
		name       string
		staging    string
		outputPath string
	}{
		{
			name:       "staging equals library",
			staging:    root,
			outputPath: filepath.Join(root, "song.{output-ext}"),
		},
		{
			name:       "output beneath staging",
			staging:    filepath.Join(root, ".staging"),
			outputPath: filepath.Join(root, ".staging", "song.{output-ext}"),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewImporter(ImporterConfig{
				LibraryRoot:    root,
				StagingRoot:    test.staging,
				OutputTemplate: test.outputPath,
			}, &mediaRunner{})
			if err == nil {
				t.Fatal("NewImporter() error = nil, want overlap rejection")
			}
		})
	}
}

func TestPortableComponentAndFormatNormalization(t *testing.T) {
	if got := safeComponent("CON"); got != "_CON" {
		t.Fatalf("safeComponent(CON) = %q, want _CON", got)
	}
	if got := safeComponent(`bad<>:"/\\|?*name`); strings.ContainsAny(got, `<>:"/\\|?*`) {
		t.Fatalf("safeComponent() retained a reserved character: %q", got)
	}
	if got := safeComponent(strings.Repeat("é", 200)); len([]byte(got)) > 180 {
		t.Fatalf("safeComponent() bytes = %d, want <= 180", len([]byte(got)))
	}
	if got := normalizedOutputFormat("/music/song.m4a", "mov"); got != "m4a" {
		t.Fatalf("normalizedOutputFormat() = %q, want m4a", got)
	}
	for _, extension := range []string{".mp3", ".flac", ".m4a"} {
		if !supportsEmbeddedArtwork(extension) {
			t.Errorf("supportsEmbeddedArtwork(%q) = false", extension)
		}
	}
	for _, extension := range []string{".ogg", ".opus", ".wav"} {
		if supportsEmbeddedArtwork(extension) {
			t.Errorf("supportsEmbeddedArtwork(%q) = true", extension)
		}
	}
}

func TestOutputPathBoundsExpandedFilenameAndCollisionSuffix(t *testing.T) {
	root := t.TempDir()
	importer, err := NewImporter(ImporterConfig{
		LibraryRoot:    root,
		StagingRoot:    filepath.Join(root, ".staging"),
		OutputTemplate: filepath.Join(root, "{artists}-{title}.{output-ext}"),
	}, &mediaRunner{})
	if err != nil {
		t.Fatal(err)
	}

	output, err := importer.outputPath(
		acquisition.TrackSpec{
			Artists: []string{strings.Repeat("artist", 80)},
			Title:   strings.Repeat("title", 80),
		},
		acquisition.AssetResult{Format: "mp3"},
		"/unused/source.mp3",
	)
	if err != nil {
		t.Fatalf("outputPath() error = %v", err)
	}
	if got := len([]byte(filepath.Base(output))); got > maxCollisionReadyFilenameBytes {
		t.Fatalf("expanded filename bytes = %d, want <= %d", got, maxCollisionReadyFilenameBytes)
	}

	withCollision := appendFilenameSuffix(output, strings.Repeat("f", sha256.Size*2))
	if got := len([]byte(filepath.Base(withCollision))); got > maxFilesystemComponentBytes {
		t.Fatalf("collision filename bytes = %d, want <= %d", got, maxFilesystemComponentBytes)
	}
}

func TestImporterIsChecksumIdempotentForOggContainers(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe is not installed")
	}

	tests := []struct {
		format string
		codec  string
	}{
		{format: "ogg", codec: "libvorbis"},
		{format: "opus", codec: "libopus"},
	}
	for _, test := range tests {
		t.Run(test.format, func(t *testing.T) {
			root := t.TempDir()
			baseSource := filepath.Join(root, "base."+test.format)
			command := exec.Command(
				ffmpeg,
				"-nostdin",
				"-hide_banner",
				"-loglevel", "error",
				"-f", "lavfi",
				"-i", "sine=frequency=1000:duration=1",
				"-c:a", test.codec,
				"-fflags", "+bitexact",
				baseSource,
			)
			if output, err := command.CombinedOutput(); err != nil {
				t.Skipf("%s encoder unavailable: %v: %s", test.codec, err, output)
			}
			sourceBytes, err := os.ReadFile(baseSource)
			if err != nil {
				t.Fatal(err)
			}

			stagingRoot := filepath.Join(root, ".staging")
			importer, err := NewImporter(ImporterConfig{
				LibraryRoot:       root,
				StagingRoot:       stagingRoot,
				OutputTemplate:    filepath.Join(root, "{spotify-id}.{output-ext}"),
				FFmpegBinary:      ffmpeg,
				FFprobeBinary:     ffprobe,
				CommandTimeout:    15 * time.Second,
				DurationTolerance: time.Second,
			}, acquisition.ExecCommandRunner{})
			if err != nil {
				t.Fatal(err)
			}

			track := acquisition.TrackSpec{
				ID:       "spotify-id",
				URL:      "https://open.spotify.com/track/spotify-id",
				Title:    "Deterministic",
				Artists:  []string{"Artist"},
				Album:    "Album",
				Duration: time.Second,
			}
			var firstPath, firstChecksum string
			for attempt := 0; attempt < 2; attempt++ {
				source := newStagedAsset(
					t,
					stagingRoot,
					"source."+test.format,
					sourceBytes,
				)
				file, err := importer.Import(context.Background(), track, acquisition.AssetResult{
					Provider:  acquisition.ProviderYTDLP,
					SourceID:  "source-id",
					SourceURL: "https://example.test/source",
					FinalPath: source,
					Format:    test.format,
				})
				if err != nil {
					t.Fatalf("Import() attempt %d error = %v", attempt+1, err)
				}
				if attempt == 0 {
					firstPath, firstChecksum = file.Path, file.Checksum
					continue
				}
				if file.Path != firstPath || file.Checksum != firstChecksum {
					t.Fatalf(
						"second import = (%q, %q), want (%q, %q)",
						file.Path,
						file.Checksum,
						firstPath,
						firstChecksum,
					)
				}
			}
		})
	}
}
