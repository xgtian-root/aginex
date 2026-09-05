//go:build !darwin && !linux && !windows

package cli

func publishNewPath(source, destination string) error {
	return publishNewPathFallback(source, destination)
}
