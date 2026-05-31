package acme

import "path/filepath"

// resolveStoragePath returns the storage directory for ACME data.
// If configured is empty, defaults to "acme/" relative to configDir.
// Relative paths are resolved relative to configDir.
func resolveStoragePath(configured, configDir string) string {
	if configured != "" {
		if filepath.IsAbs(configured) {
			return configured
		}
		return filepath.Join(configDir, configured)
	}
	return filepath.Join(configDir, "acme")
}
