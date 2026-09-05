package cli

import (
	"fmt"
	"io/fs"
	"os"
	"runtime"
)

func publishNewPathFallback(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode().IsRegular() {
		if err := os.Link(source, destination); err != nil {
			return err
		}
		_ = os.Remove(source)
		return nil
	}
	if !info.IsDir() {
		return &fs.PathError{Op: "publish", Path: source, Err: fs.ErrInvalid}
	}
	return fmt.Errorf(
		"exclusive directory publication is unavailable on %s: %w",
		runtime.GOOS,
		fs.ErrInvalid,
	)
}
