package handler

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/supperdoggy/SmartHomeServer/harmoniq-maestro/album-queue/pkg/db"
	"github.com/supperdoggy/SmartHomeServer/harmoniq-maestro/album-queue/pkg/utils"
	models "github.com/supperdoggy/spot-models"
	"github.com/supperdoggy/spot-models/spotify"
	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"
	"gopkg.in/tucnak/telebot.v2"
)

type Handler interface {
	Start(m *telebot.Message)
	HandleText(m *telebot.Message)
	HandleQueue(m *telebot.Message)
	HandleFailed(m *telebot.Message)
	HandleFailedPage(c *telebot.Callback)
	HandleRedownload(m *telebot.Message)
	HandleDeactivate(m *telebot.Message)
	HandlePlaylist(m *telebot.Message)
	HandlePlaylistNoPull(m *telebot.Message)
}

const (
	failedPageCallbackUnique = "failed_page"
	failedPageSize           = 5
)

var failedPageCallbackEndpoint = &telebot.InlineButton{Unique: failedPageCallbackUnique}

func FailedPageCallbackEndpoint() telebot.CallbackEndpoint {
	return failedPageCallbackEndpoint
}

type handler struct {
	db                db.Database
	spotifyService    spotify.SpotifyService
	whiteList         []int64
	bot               *telebot.Bot
	log               *zap.Logger
	doneWebhook       string
	replyFunc         func(m *telebot.Message, text string) error
	sendWebhookFn     func() error
	sendFailedPageFn  func(m *telebot.Message, text string, markup *telebot.ReplyMarkup) error
	editFailedPageFn  func(m *telebot.Message, text string, markup *telebot.ReplyMarkup) error
	respondCallbackFn func(c *telebot.Callback, text string, showAlert bool) error
}

func NewHandler(db db.Database, spotifyService spotify.SpotifyService, log *zap.Logger, bot *telebot.Bot, doneWebhook string, whiteList []int64) Handler {
	return &handler{
		db:             db,
		spotifyService: spotifyService,
		log:            log,
		bot:            bot,
		whiteList:      whiteList,
		doneWebhook:    doneWebhook,
		replyFunc: func(m *telebot.Message, text string) error {
			_, err := bot.Reply(m, text)
			return err
		},
		sendWebhookFn: func() error {
			return utils.SendDoneWebhook(doneWebhook)
		},
		sendFailedPageFn: func(m *telebot.Message, text string, markup *telebot.ReplyMarkup) error {
			if markup != nil {
				_, err := bot.Reply(m, text, markup)
				return err
			}
			_, err := bot.Reply(m, text)
			return err
		},
		editFailedPageFn: func(m *telebot.Message, text string, markup *telebot.ReplyMarkup) error {
			if markup != nil {
				_, err := bot.Edit(m, text, markup)
				return err
			}
			_, err := bot.Edit(m, text)
			return err
		},
		respondCallbackFn: func(c *telebot.Callback, text string, showAlert bool) error {
			resp := &telebot.CallbackResponse{
				Text:      text,
				ShowAlert: showAlert,
			}
			return bot.Respond(c, resp)
		},
	}
}

func (h *handler) reply(m *telebot.Message, text string) {
	if err := h.replyFunc(m, text); err != nil {
		h.log.Error("Failed to send reply", zap.Error(err))
	}
}

func (h *handler) sendWebhook() {
	if err := h.sendWebhookFn(); err != nil {
		h.log.Error("Failed to send webhook", zap.Error(err))
	}
}

func (h *handler) Start(m *telebot.Message) {
	if !utils.InWhiteList(m.Sender.ID, h.whiteList) {
		h.log.Info("Unauthorized user", zap.Int64("user_id", m.Sender.ID))
		return
	}

	h.reply(m, "Привіііііііііт, я бот який кочає музіку на сєрвер, скинь мені урлу на спотік і я додам в чергу на скачування ❤️")
}

func (h *handler) HandleText(m *telebot.Message) {
	if !utils.InWhiteList(m.Sender.ID, h.whiteList) {
		h.log.Info("Unauthorized user", zap.Int64("user_id", m.Sender.ID))
		return
	}

	h.log.Info("Received message", zap.Any("message", m.Text))

	// Check if the message is a valid Spotify URL
	if !utils.IsValidSpotifyURL(m.Text) {
		h.reply(m, "о ніііііі, це не посилання на спотіфай.... 💔😭")
		return
	}

	ctx := context.Background()

	// Get object type, name and track count from Spotify API
	objectType, err := h.spotifyService.GetObjectType(ctx, m.Text)
	if err != nil {
		h.log.Error("Failed to get object type from Spotify", zap.Error(err))
		h.reply(m, "не получилось отримати тип об'єкта зі спотіфай, спробуй ще раз...")
		return
	}

	name, err := h.spotifyService.GetObjectName(ctx, m.Text)
	if err != nil {
		h.log.Error("Failed to get object name from Spotify", zap.Error(err))
		h.reply(m, "не получилось отримати інформацію зі спотіфай, спробуй ще раз...")
		return
	}

	trackCount, trackMetadata, err := h.spotifyService.GetTrackCount(ctx, m.Text)
	if err != nil {
		h.log.Error("Failed to get track count from Spotify", zap.Error(err))
		h.reply(m, "не получилось отримати кількість треків, але додав в чергу...")
		// Continue with empty track data
		trackCount = 0
		trackMetadata = nil
	}

	// Add the download request to the database
	err = h.db.NewDownloadRequest(ctx, m.Text, name, m.Sender.ID, objectType, trackCount, trackMetadata)
	if err != nil {
		h.log.Error("Failed to add download request to database", zap.Error(err))
		h.reply(m, "не получилось додати в чергу, скажи максиму шо шось не так...")
		return
	}

	h.sendWebhook()

	h.reply(m, fmt.Sprintf("Ураураура успішно додали %s в чергу! (Треків: %d) ❤️", name, trackCount))
}

func (h *handler) HandleQueue(m *telebot.Message) {
	if !utils.InWhiteList(m.Sender.ID, h.whiteList) {
		h.log.Info("Unauthorized user", zap.Int64("user_id", m.Sender.ID))
		return
	}

	ctx := context.Background()
	requests, err := h.db.GetActiveRequests(ctx)
	if err != nil {
		h.log.Error("Failed to get active download requests", zap.Error(err))
		h.reply(m, "не получилося дістати чергу... 💔😭")
		return
	}

	playlists, err := h.db.GetActivePlaylists(ctx)
	if err != nil {
		h.log.Error("Failed to get active playlist requests", zap.Error(err))
		// Continue anyway, just log the error
	}

	if len(requests) == 0 && len(playlists) == 0 {
		h.reply(m, "немає активних запитів на скачування...")
		return
	}

	// Update found track count for each request and save to database
	for i := range requests {
		if requests[i].ExpectedTrackCount > 0 && len(requests[i].TrackMetadata) > 0 {
			foundCount, err := h.compareTracks(ctx, requests[i])
			if err != nil {
				h.log.Error("Failed to compare tracks", zap.Error(err), zap.String("request_id", requests[i].ID))
				continue
			}
			// Always update the count when /queue is called
			requests[i].FoundTrackCount = foundCount
			requests[i].UpdatedAt = time.Now().Unix()

			// Mark as completed if all tracks are found
			if foundCount == requests[i].ExpectedTrackCount && requests[i].Active {
				requests[i].Active = false
				h.log.Info("Marking request as completed",
					zap.String("request_id", requests[i].ID),
					zap.String("name", requests[i].Name),
					zap.Int("found", foundCount),
					zap.Int("expected", requests[i].ExpectedTrackCount))
			}

			if err := h.db.UpdateDownloadRequest(ctx, requests[i]); err != nil {
				h.log.Error("Failed to update found track count", zap.Error(err))
			}
		}
	}

	response := "Активні запити на скачування:\n\n"
	for _, r := range requests {
		response += fmt.Sprintf("📀 %s\n", r.Name)
		if r.ExpectedTrackCount > 0 {
			downloaded := r.FoundTrackCount
			percentage := float64(downloaded) / float64(r.ExpectedTrackCount) * 100

			response += fmt.Sprintf("   ✅ Завантажено: %d/%d (%.0f%%)\n", downloaded, r.ExpectedTrackCount, percentage)

			// Calculate missing tracks (not found and not skipped)
			missingTracks := []spotify.TrackMetadata{}
			skippedCount := 0
			if len(r.TrackMetadata) > 0 {
				for _, track := range r.TrackMetadata {
					if !track.Found && !track.Skipped {
						missingTracks = append(missingTracks, track)
					} else if track.Skipped {
						skippedCount++
					}
				}
			}

			remaining := len(missingTracks)

			if remaining > 0 {
				response += fmt.Sprintf("   ⏳ Залишилось: %d треків\n", remaining)

				// Show first 5 tracks that need to be downloaded
				displayCount := remaining
				if displayCount > 5 {
					displayCount = 5
				}

				response += "   📋 Треки для завантаження:\n"
				for i := 0; i < displayCount; i++ {
					track := missingTracks[i]
					response += fmt.Sprintf("      • %s - %s\n", track.Artist, track.Title)
				}
				if remaining > 5 {
					response += fmt.Sprintf("      ... та ще %d треків\n", remaining-5)
				}
			} else {
				response += "   🎉 Всі треки завантажені!\n"
			}

			if skippedCount > 0 {
				response += fmt.Sprintf("   ⚠️ Пропущено: %d треків\n", skippedCount)
			}
		} else {
			response += "   ⏳ Очікування завантаження...\n"
		}

		if r.Errored {
			response += fmt.Sprintf("   ⚠️ Помилки: %d\n", r.RetryCount)
		}
		response += "\n"
	}

	// Add playlist requests
	if len(playlists) > 0 {
		if len(requests) > 0 {
			response += "---\n\n"
		}
		response += "Активні запити на плейлисти:\n\n"
		for _, p := range playlists {
			// Try to get playlist name
			playlistName, err := h.spotifyService.GetObjectName(ctx, p.SpotifyURL)
			if err != nil {
				h.log.Error("Failed to get playlist name", zap.Error(err))
				playlistName = p.SpotifyURL
			}

			response += fmt.Sprintf("🎵 %s\n", playlistName)
			response += fmt.Sprintf("   📎 URL: %s\n", p.SpotifyURL)
			if p.NoPull {
				response += "   ⚠️ NoPull: true (не завантажувати відсутні треки)\n"
			} else {
				response += "   ✅ Завантажувати відсутні треки\n"
			}
			if p.Errored {
				response += fmt.Sprintf("   ⚠️ Помилки: %d\n", p.RetryCount)
			}
			response += "\n"
		}
	}

	h.reply(m, response)
}

func (h *handler) HandleFailed(m *telebot.Message) {
	if !utils.InWhiteList(m.Sender.ID, h.whiteList) {
		h.log.Info("Unauthorized user", zap.Int64("user_id", m.Sender.ID))
		return
	}

	failedTracks, err := h.db.GetUnresolvedFailedTracks(context.Background())
	if err != nil {
		h.log.Error("Failed to get unresolved failed tracks", zap.Error(err))
		h.reply(m, "не получилось дістати список фейлів на скачування...")
		return
	}

	if len(failedTracks) == 0 {
		h.reply(m, "нема невирішених фейлів на скачування.")
		return
	}

	text, markup, err := renderFailedTracksPage(failedTracks, 0)
	if err != nil {
		h.log.Error("Failed to render failed tracks page", zap.Error(err))
		h.reply(m, "не получилось зібрати сторінку фейлів...")
		return
	}

	if err := h.sendFailedPageFn(m, text, markup); err != nil {
		h.log.Error("Failed to send paginated failed tracks message", zap.Error(err))
	}
}

func (h *handler) HandleFailedPage(c *telebot.Callback) {
	if c == nil || c.Sender == nil || !utils.InWhiteList(c.Sender.ID, h.whiteList) {
		if c != nil {
			_ = h.respondCallbackFn(c, "доступ заборонено", true)
		}
		return
	}

	page, err := strconv.Atoi(strings.TrimSpace(c.Data))
	if err != nil {
		_ = h.respondCallbackFn(c, "невірний номер сторінки", true)
		return
	}

	failedTracks, err := h.db.GetUnresolvedFailedTracks(context.Background())
	if err != nil {
		h.log.Error("Failed to get unresolved failed tracks", zap.Error(err))
		_ = h.respondCallbackFn(c, "не получилось оновити список фейлів", true)
		return
	}

	if len(failedTracks) == 0 {
		if c.Message != nil {
			if err := h.editFailedPageFn(c.Message, "нема невирішених фейлів на скачування.", nil); err != nil {
				h.log.Error("Failed to edit failed page to empty state", zap.Error(err))
			}
		}
		_ = h.respondCallbackFn(c, "", false)
		return
	}

	text, markup, err := renderFailedTracksPage(failedTracks, page)
	if err != nil {
		_ = h.respondCallbackFn(c, "невірний номер сторінки", true)
		return
	}

	if c.Message != nil {
		if err := h.editFailedPageFn(c.Message, text, markup); err != nil {
			h.log.Error("Failed to edit failed tracks page", zap.Error(err))
		}
	}

	_ = h.respondCallbackFn(c, "", false)
}

func (h *handler) HandleRedownload(m *telebot.Message) {
	if !utils.InWhiteList(m.Sender.ID, h.whiteList) {
		h.log.Info("Unauthorized user", zap.Int64("user_id", m.Sender.ID))
		return
	}

	msg := strings.Fields(m.Text)
	if len(msg) != 2 {
		h.reply(m, "не розумію цю команду. Пліз юзай /redownload <spotify_track_url>.")
		return
	}

	trackURL := strings.TrimSpace(msg[1])
	if !isValidSpotifyTrackURL(trackURL) {
		h.reply(m, "це має бути лінк на трек Spotify у форматі https://open.spotify.com/track/...")
		return
	}

	ctx := context.Background()

	hasActive, err := h.db.HasActiveRequestByURL(ctx, trackURL)
	if err != nil {
		h.log.Error("Failed to check existing active request", zap.Error(err), zap.String("track_url", trackURL))
		h.reply(m, "не получилось перевірити активні запити, спробуй ще раз...")
		return
	}
	if hasActive {
		h.reply(m, "цей трек вже є в активній черзі, редовнлоад пропущено.")
		return
	}

	failedTrack, err := h.db.GetUnresolvedFailedTrackByURL(ctx, trackURL)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			h.reply(m, "цей трек не знайдений серед невирішених фейлів.")
			return
		}

		h.log.Error("Failed to get unresolved failed track by url", zap.Error(err), zap.String("track_url", trackURL))
		h.reply(m, "не получилось перевірити фейл цього треку, спробуй ще раз...")
		return
	}

	requestName := strings.TrimSpace(strings.TrimSpace(failedTrack.Artist) + " - " + strings.TrimSpace(failedTrack.Title))
	if requestName == "-" || requestName == "" {
		requestName = failedTrack.SpotifyURL
	}

	trackMetadata := []spotify.TrackMetadata{
		{
			SpotifyURL:     failedTrack.SpotifyURL,
			Artist:         failedTrack.Artist,
			Title:          failedTrack.Title,
			Found:          false,
			FailedAttempts: 0,
			Skipped:        false,
		},
	}

	err = h.db.NewDownloadRequest(
		ctx,
		failedTrack.SpotifyURL,
		requestName,
		m.Sender.ID,
		spotify.SpotifyObjectTypeTrack,
		1,
		trackMetadata,
	)
	if err != nil {
		h.log.Error("Failed to create redownload request", zap.Error(err), zap.String("track_url", trackURL))
		h.reply(m, "не получилось створити редовнлоад запит, спробуй ще раз...")
		return
	}

	h.sendWebhook()

	h.reply(m, fmt.Sprintf("редовнлоад створено ✅\n%s\n%s - %s", failedTrack.SpotifyURL, failedTrack.Artist, failedTrack.Title))
}

func isValidSpotifyTrackURL(url string) bool {
	return utils.IsValidSpotifyURL(url) && strings.HasPrefix(url, "https://open.spotify.com/track/")
}

func renderFailedTracksPage(failedTracks []db.FailedTrack, page int) (string, *telebot.ReplyMarkup, error) {
	if len(failedTracks) == 0 {
		return "", nil, errors.New("failed tracks list is empty")
	}

	totalPages := (len(failedTracks) + failedPageSize - 1) / failedPageSize
	if page < 0 || page >= totalPages {
		return "", nil, errors.New("page out of range")
	}

	start := page * failedPageSize
	end := start + failedPageSize
	if end > len(failedTracks) {
		end = len(failedTracks)
	}

	var response strings.Builder
	response.WriteString(fmt.Sprintf("Невирішені фейли на скачування: %d\n", len(failedTracks)))
	response.WriteString(fmt.Sprintf("Сторінка %d/%d\n\n", page+1, totalPages))

	for i := start; i < end; i++ {
		track := failedTracks[i]

		artist := strings.TrimSpace(track.Artist)
		if artist == "" {
			artist = "невідомий артист"
		}

		title := strings.TrimSpace(track.Title)
		if title == "" {
			title = "невідома назва"
		}

		response.WriteString(fmt.Sprintf("%d. %s - %s\n", i+1, artist, title))
		response.WriteString(fmt.Sprintf("   🔗 %s\n", track.SpotifyURL))
		response.WriteString(fmt.Sprintf("   ⚠️ Спроб: %d | Джерел: %d\n\n", track.FailedAttempts, track.SourceCount))
	}

	if totalPages == 1 {
		return response.String(), nil, nil
	}

	markup := &telebot.ReplyMarkup{}
	row := make([]telebot.Btn, 0, 2)

	if page > 0 {
		row = append(row, markup.Data("⬅️ Назад", failedPageCallbackUnique, strconv.Itoa(page-1)))
	}
	if page < totalPages-1 {
		row = append(row, markup.Data("Вперед ➡️", failedPageCallbackUnique, strconv.Itoa(page+1)))
	}

	if len(row) > 0 {
		markup.Inline(markup.Row(row...))
	}

	return response.String(), markup, nil
}

// compareTracks compares expected tracks with indexed files and returns the count of found tracks
func (h *handler) compareTracks(ctx context.Context, request models.DownloadQueueRequest) (int, error) {
	if len(request.TrackMetadata) == 0 {
		return 0, nil
	}

	// Extract artists and titles from track metadata
	artists := make([]string, 0, len(request.TrackMetadata))
	titles := make([]string, 0, len(request.TrackMetadata))
	for _, track := range request.TrackMetadata {
		artists = append(artists, track.Artist)
		titles = append(titles, track.Title)
	}

	// Find matching music files in the database
	foundMusic, err := h.db.FindMusicFiles(ctx, artists, titles)
	if err != nil {
		return 0, err
	}

	// Create a map for quick lookup (case-insensitive)
	foundMap := make(map[string]bool)
	for _, music := range foundMusic {
		key := strings.ToLower(music.Artist) + " " + strings.ToLower(music.Title)
		foundMap[key] = true
	}

	// Count how many expected tracks were found (excluding skipped tracks)
	foundCount := 0
	for _, track := range request.TrackMetadata {
		// Skip counting skipped tracks
		if track.Skipped {
			continue
		}
		key := strings.ToLower(track.Artist) + " " + strings.ToLower(track.Title)
		if foundMap[key] {
			foundCount++
		}
	}

	return foundCount, nil
}

func (h *handler) HandleDeactivate(m *telebot.Message) {
	if !utils.InWhiteList(m.Sender.ID, h.whiteList) {
		h.log.Info("Unauthorized user", zap.Int64("user_id", m.Sender.ID))
		return
	}

	s := strings.Split(m.Text, " ")
	if len(s) != 2 {
		h.reply(m, "не розумію цю команду. Пліз юзай /deactivate <request_id>.")
		return
	}

	id := s[1]
	h.log.Info("Deactivating request", zap.String("id", id))

	err := h.db.DeactivateRequest(context.Background(), id)
	if err != nil {
		h.log.Error("Failed to deactivate request", zap.Error(err))
		h.reply(m, "не получилося деактивувати запит. Пліз спробуй ще раз пізніше.")
		return
	}

	h.reply(m, "Запит деактивовано, всьо капец.")
}

func (h *handler) HandlePlaylist(m *telebot.Message) {
	if !utils.InWhiteList(m.Sender.ID, h.whiteList) {
		h.log.Info("Unauthorized user", zap.Int64("user_id", m.Sender.ID))
		return
	}

	h.log.Info("Received playlist request", zap.Any("message", m.Text))

	msg := strings.Split(m.Text, " ")
	if len(msg) != 2 {
		h.reply(m, "не розумію цю команду. Пліз юзай /playlist <playlist_id>.")
		return
	}

	playlistURL := msg[1]

	if !utils.IsValidSpotifyURL(playlistURL) {
		h.reply(m, "о ніііііі, це не посилання на спотіфай.... 💔😭")
		return
	}

	if err := h.db.NewPlaylistRequest(context.Background(), playlistURL, m.Sender.ID, false); err != nil {
		h.log.Error("Failed to add playlist request to database", zap.Error(err))
		h.reply(m, "не получилось додати в чергу, скажи максиму шо шось не так...")
		return
	}

	h.sendWebhook()

	h.reply(m, "Ураураура успішно додали плейлист в чергу!!!!")
}

func (h *handler) HandlePlaylistNoPull(m *telebot.Message) {
	if !utils.InWhiteList(m.Sender.ID, h.whiteList) {
		h.log.Info("Unauthorized user", zap.Int64("user_id", m.Sender.ID))
		return
	}

	h.log.Info("Received playlist request", zap.Any("message", m.Text))

	msg := strings.Split(m.Text, " ")
	if len(msg) != 2 {
		h.reply(m, "не розумію цю команду. Пліз юзай /playlist <playlist_id>.")
		return
	}

	playlistURL := msg[1]

	if !utils.IsValidSpotifyURL(playlistURL) {
		h.reply(m, "о ніііііі, це не посилання на спотіфай.... 💔😭")
		return
	}

	if err := h.db.NewPlaylistRequest(context.Background(), playlistURL, m.Sender.ID, true); err != nil {
		h.log.Error("Failed to add playlist request to database", zap.Error(err))
		h.reply(m, "не получилось додати в чергу, скажи максиму шо шось не так...")
		return
	}

	h.sendWebhook()

	h.reply(m, "Ураураура успішно додали плейлист в чергу!!!!")
}
