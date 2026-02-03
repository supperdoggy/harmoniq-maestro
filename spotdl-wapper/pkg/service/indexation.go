package service

import (
	"context"
	"os"
	"os/exec"
)

func (s *service) IndexDownloadedFiles(ctx context.Context) error {
	// Run the "spotdl --sync {url}" command
	cmd := exec.Command("sh /home/maks/run_music_indexer.sh")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Execute the command
	if err := cmd.Run(); err != nil {
		return err
	}
	s.log.Info("indexing completed")
	return nil
}
