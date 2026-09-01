//go:build !darwin && !windows

package player

// New reports that this platform has no way to read Apple Music's state.
// Apple Music ships for macOS and Windows only; the daemon builds elsewhere so
// that `go vet ./...` and CI stay useful, but it cannot run.
func New() (Player, error) {
	return nil, ErrUnsupported
}
