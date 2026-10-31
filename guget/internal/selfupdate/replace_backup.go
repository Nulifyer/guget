package selfupdate

import (
	"fmt"
	"os"
)

// Keep the old executable beside the new one until it can be removed.
func replaceWithBackup(stage, target string, rename func(string, string) error) error {
	backup := target + ".old"
	if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove previous binary; close other guget sessions and retry: %w", err)
	}
	if err := rename(target, backup); err != nil {
		return err
	}
	if err := rename(stage, target); err != nil {
		if rollback := rename(backup, target); rollback != nil {
			return fmt.Errorf("%w; recovery failed: %v; original binary remains at %s", err, rollback, backup)
		}
		return err
	}
	// Usually still running. A future update retries cleanup if removal fails.
	_ = os.Remove(backup)
	return nil
}
