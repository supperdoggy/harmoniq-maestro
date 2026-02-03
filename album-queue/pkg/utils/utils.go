package utils

import (
	"net/http"
	"slices"
	"strings"
)

func IsValidSpotifyURL(url string) bool {
	// Check if the URL starts with "https://open.spotify.com/album/"
	return strings.HasPrefix(url, "https://open.spotify.com/")
}

func InWhiteList(url int64, whitelist []int64) bool {
	return slices.Contains(whitelist, url)
}

func SendDoneWebhook(webhookURL string) error {
	// This function would send a GET request to the webhook URL with the message
	resp, err := http.Get(webhookURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}
