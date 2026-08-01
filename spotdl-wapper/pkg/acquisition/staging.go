package acquisition

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ensureOutputParent(pathOrTemplate string) error {
	parent := filepath.Dir(pathOrTemplate)
	if strings.Contains(parent, "%(") {
		return fmt.Errorf(
			"output template directory must be concrete, got %q",
			parent,
		)
	}
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return fmt.Errorf("create acquisition staging directory %q: %w", parent, err)
	}
	return nil
}

func ensurePathWithinRoot(root, path string) error {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%q is outside %q", path, root)
	}
	return nil
}
