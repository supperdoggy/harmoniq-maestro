package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	mongoURI              = "mongodb://root:SssnI5NMth5OeedbPmbQ49DxEbT726@100.111.149.52:27017/"
	dbName                = "music-services"
	collectionName        = "music-files"
	ollamaURL             = "http://100.79.119.60:11434/api/generate"
	ollamaModel           = "tinyllama:1.1b"
	batchSize             = 300
	maxRetries            = 3
	playlistItemsPerBatch = 5
	totalTargetItems      = 50
)

type Song map[string]interface{}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type OllamaRequest struct {
	Model    string    `json:"model"`
	Stream   bool      `json:"stream"`
	Messages []Message `json:"messages"`
}

func getAllSongs(ctx context.Context, client *mongo.Client) ([]Song, error) {
	collection := client.Database(dbName).Collection(collectionName)

	// Exclude _id and meta_data
	projection := bson.D{
		{"_id", 0},
		{"meta_data", 0},
	}

	cursor, err := collection.Find(ctx, bson.D{}, options.Find().SetProjection(projection))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var songs []Song
	for cursor.Next(ctx) {
		var song Song
		if err := cursor.Decode(&song); err != nil {
			fmt.Printf("⚠️ Skipping bad doc: %v\n", err)
			continue
		}
		songs = append(songs, song)
	}
	return songs, nil
}

func askOllamaWithRetry(songsBatch []Song, playlistRequest string) (string, error) {
	attempt := 0
	for attempt < maxRetries {
		fmt.Printf("📤 Sending batch of %d songs (attempt %d)...\n", len(songsBatch), attempt+1)

		var reqBody []byte
		var err error

		// Format the prompt manually (plain string for non-chat models)
		songsJSON, err := json.MarshalIndent(songsBatch, "", "  ")
		if err != nil {
			return "", err
		}

		prompt := fmt.Sprintf(`Here is a list of songs in JSON format:

%s

Pick %d songs from this list for the theme:
"%s"

Only pick from songs I gave you. Return just a JSON array like:
[
  { "title": "Song Title", "artist": "Artist" },
  ...
]
`, string(songsJSON), playlistItemsPerBatch, playlistRequest)

		// Use "prompt" field instead of "messages" for non-chat models
		payload := map[string]interface{}{
			"model":  ollamaModel,
			"prompt": prompt,
			"stream": false,
		}

		reqBody, err = json.Marshal(payload)
		if err != nil {
			return "", err
		}

		// Send the request
		resp, err := http.Post(ollamaURL, "application/json", bytes.NewBuffer(reqBody))
		if err != nil {
			fmt.Printf("⚠️ Error sending request: %v\n", err)
			attempt++
			time.Sleep(time.Duration(1<<attempt) * time.Second)
			continue
		}
		defer resp.Body.Close()

		respData, err := io.ReadAll(resp.Body)
		if err != nil {
			fmt.Printf("⚠️ Error reading response: %v\n", err)
			attempt++
			time.Sleep(time.Duration(1<<attempt) * time.Second)
			continue
		}

		var result map[string]interface{}
		if err := json.Unmarshal(respData, &result); err != nil {
			fmt.Printf("⚠️ Invalid JSON: %v\n", err)
			attempt++
			time.Sleep(time.Duration(1<<attempt) * time.Second)
			continue
		}

		if content, ok := result["response"].(string); ok && strings.TrimSpace(content) != "" {
			return content, nil
		}

		fmt.Printf("⚠️ Empty or invalid response from Ollama: %s\n", string(respData))
		attempt++
		time.Sleep(time.Duration(1<<attempt) * time.Second)
	}
	return "", fmt.Errorf("ollama failed after %d attempts", maxRetries)
}

func parsePlaylist(rawResponse string) ([]Song, error) {
	var playlist []Song
	dec := json.NewDecoder(strings.NewReader(rawResponse))
	dec.DisallowUnknownFields()
	err := dec.Decode(&playlist)
	if err != nil {
		return nil, err
	}
	return playlist, nil
}

func collectFullPlaylist(allSongs []Song, playlistRequest string) ([]Song, error) {
	fullPlaylist := make([]Song, 0, totalTargetItems)
	seen := make(map[string]struct{})

	// Shuffle songs - simple shuffle
	for i := range allSongs {
		j := i + int(time.Now().UnixNano())%(len(allSongs)-i)
		allSongs[i], allSongs[j] = allSongs[j], allSongs[i]
	}

	for i := 0; i < len(allSongs); i += batchSize {
		end := i + batchSize
		if end > len(allSongs) {
			end = len(allSongs)
		}
		batch := allSongs[i:end]

		raw, err := askOllamaWithRetry(batch, playlistRequest)
		if err != nil {
			fmt.Printf("⚠️ Skipping batch due to error: %v\n", err)
			continue
		}

		fmt.Printf("🔍 Raw response from Ollama:\n%s\n", raw) // <-- add this

		partial, err := parsePlaylist(raw)
		if err != nil {
			fmt.Printf("⚠️ Could not parse playlist: %v\n", err)
			continue
		}

		for _, song := range partial {
			title, _ := song["title"].(string)
			artist, _ := song["artist"].(string)
			key := strings.ToLower(strings.TrimSpace(title)) + "___" + strings.ToLower(strings.TrimSpace(artist))
			if _, exists := seen[key]; !exists {
				fullPlaylist = append(fullPlaylist, song)
				seen[key] = struct{}{}
			}
			if len(fullPlaylist) >= totalTargetItems {
				return fullPlaylist, nil
			}
		}
	}

	return fullPlaylist, nil
}

func prettyJSON(v interface{}) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "[]"
	}
	return string(b)
}

func main() {
	ctx := context.Background()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		panic(err)
	}
	defer client.Disconnect(ctx)

	fmt.Println("🎼 Loading all songs from MongoDB...")
	allSongs, err := getAllSongs(ctx, client)
	if err != nil {
		panic(err)
	}
	fmt.Printf("🎶 Total songs: %d\n", len(allSongs))

	playlistRequest := "Energetic highschool rock anthems from the 80s and 90s, with a focus on guitar solos and catchy choruses."

	playlist, err := collectFullPlaylist(allSongs, playlistRequest)
	if err != nil {
		fmt.Printf("❌ Failed to generate playlist: %v\n", err)
		return
	}

	if len(playlist) == 0 {
		fmt.Println("❌ No playlist generated.")
		return
	}

	fmt.Printf("\n✅ Final Playlist (%d songs):\n", len(playlist))
	for i, song := range playlist {
		title, _ := song["title"].(string)
		artist, _ := song["artist"].(string)
		fmt.Printf("%d. %s – %s\n", i+1, title, artist)
	}
}
