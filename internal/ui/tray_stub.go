//go:build !(darwin || windows) || notray

package ui

// This build has no tray: either the platform has none, or it was compiled with
// the `notray` tag for a pure background daemon.

// Supported reports whether this build has a tray.
func Supported() bool { return false }

// Tray is a no-op stand-in so the caller needs no build tags of its own.
type Tray struct{ done chan struct{} }

// New returns a Tray that does nothing.
func New(string, func(bool)) *Tray { return &Tray{done: make(chan struct{})} }

// Run reports immediately that there is nothing to run.
func (t *Tray) Run(func()) { close(t.done) }

// Done is closed once Run has returned.
func (t *Tray) Done() <-chan struct{} { return t.done }

// Quit does nothing.
func (t *Tray) Quit() {}

// Update discards the status.
func (t *Tray) Update(Status) {}
