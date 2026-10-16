//go:build !darwin

package jman

import "fmt"

type platformWatcher interface {
	events() <-chan string
	errors() <-chan error
	close() error
}

func newPlatformWatcher([]string, ...[]string) (platformWatcher, error) {
	return nil, fmt.Errorf("native filesystem watcher is unavailable on this platform")
}
