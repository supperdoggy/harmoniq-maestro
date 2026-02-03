package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/supperdoggy/spot-models"
	"go.mongodb.org/mongo-driver/bson"
	"go.uber.org/zap"
)

type Client struct {
	apiKey string
	log    *zap.Logger
	client *http.Client
}

type PlaylistDescriptionResponse struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func NewClient(apiKey string, log *zap.Logger) *Client {
	return &Client{
		apiKey: apiKey,
		log:    log,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) GeneratePlaylistDescription(ctx context.Context, query bson.M, sampleTracks []models.MusicFile) (string, string, error) {
	// Convert query to readable format
	queryJSON, err := json.MarshalIndent(query, "", "  ")
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal query: %w", err)
	}

	// Format sample tracks
	tracksInfo := make([]string, 0, len(sampleTracks))
	for i, track := range sampleTracks {
		if i >= 5 { // Limit to 5 sample tracks
			break
		}
		tracksInfo = append(tracksInfo, fmt.Sprintf("- %s by %s", track.Title, track.Artist))
	}
	tracksSample := strings.Join(tracksInfo, "\n")
	if tracksSample == "" {
		tracksSample = "No sample tracks available"
	}

	prompt := fmt.Sprintf(`Given this MongoDB query for a music playlist:
%s

And these sample tracks that match:
%s

Generate a creative playlist name and a 1-2 sentence description.
Return JSON: {"name": "...", "description": "..."}
Only return the JSON, no other text.`, string(queryJSON), tracksSample)

	response, err := c.callOpenAI(ctx, prompt)
	if err != nil {
		return "", "", err
	}

	// Parse JSON response
	var descResp PlaylistDescriptionResponse
	// Try to extract JSON from response (in case there's extra text)
	jsonStart := strings.Index(response, "{")
	jsonEnd := strings.LastIndex(response, "}")
	if jsonStart >= 0 && jsonEnd > jsonStart {
		response = response[jsonStart : jsonEnd+1]
	}

	if err := json.Unmarshal([]byte(response), &descResp); err != nil {
		return "", "", fmt.Errorf("failed to parse OpenAI response: %w", err)
	}

	return descResp.Name, descResp.Description, nil
}

func (c *Client) ClassifyGenre(ctx context.Context, artist, title, album string) (string, error) {
	prompt := fmt.Sprintf(`Classify the genre for this song. Return a single general genre category.

Artist: %s
Title: %s
Album: %s

Choose from: rock, pop, electronic, hip-hop, jazz, classical, metal, country, r&b, folk, blues, reggae, latin, world, soundtrack, other

Return only the genre name, nothing else.`, artist, title, album)

	response, err := c.callOpenAI(ctx, prompt)
	if err != nil {
		return "", err
	}

	// Clean up response - remove quotes, whitespace, etc.
	genre := strings.TrimSpace(response)
	genre = strings.Trim(genre, "\"'")
	genre = strings.ToLower(genre)

	return genre, nil
}

func (c *Client) callOpenAI(ctx context.Context, prompt string) (string, error) {
	url := "https://api.openai.com/v1/chat/completions"

	payload := map[string]interface{}{
		"model": "gpt-4o-mini",
		"messages": []map[string]string{
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"temperature": 0.7,
		"max_tokens":  500,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("OpenAI API error: %d - %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	choices, ok := result["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return "", fmt.Errorf("invalid response format: no choices")
	}

	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("invalid response format: choice is not a map")
	}

	message, ok := choice["message"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("invalid response format: message is not a map")
	}

	content, ok := message["content"].(string)
	if !ok {
		return "", fmt.Errorf("invalid response format: content is not a string")
	}

	return content, nil
}
