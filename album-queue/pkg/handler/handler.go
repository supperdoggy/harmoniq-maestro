package handler

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/supperdoggy/SmartHomeServer/harmoniq-maestro/album-queue/pkg/db"
	"github.com/supperdoggy/SmartHomeServer/harmoniq-maestro/album-queue/pkg/utils"
	models "github.com/supperdoggy/spot-models"
	"github.com/supperdoggy/spot-models/spotify"
	"go.uber.org/zap"
	"gopkg.in/tucnak/telebot.v2"
)

type Handler interface {
	Start(m *telebot.Message)
	HandleText(m *telebot.Message)
	HandleQueue(m *telebot.Message)
	HandleDeactivate(m *telebot.Message)
	HandlePlaylist(m *telebot.Message)
	HandlePlaylistNoPull(m *telebot.Message)
}

type handler struct {
	db             db.Database
	spotifyService spotify.SpotifyService
	whiteList      []int64
	bot            *telebot.Bot
	log            *zap.Logger
	doneWebhook    string
}

func NewHandler(db db.Database, spotifyService spotify.SpotifyService, log *zap.Logger, bot *telebot.Bot, doneWebhook string, whiteList []int64) Handler {
	return &handler{
		db:             db,
		spotifyService: spotifyService,
		log:            log,
		bot:            bot,
		whiteList:      whiteList,
		doneWebhook:    doneWebhook,
	}
}

func (h *handler) reply(m *telebot.Message, text string) {
	if _, err := h.bot.Reply(m, text); err != nil {
		h.log.Error("Failed to send reply", zap.Error(err))
	}
}

func (h *handler) sendWebhook() {
	if err := utils.SendDoneWebhook(h.doneWebhook); err != nil {
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
