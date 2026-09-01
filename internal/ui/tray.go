//go:build (darwin || windows) && !notray

package ui

import (
	"sync"

	"fyne.io/systray"
)

// Supported reports whether this build has a tray.
func Supported() bool { return true }

// Tray drives the menu bar or notification-area item.
type Tray struct {
	version string
	onPause func(paused bool)

	// updates carries one pending status. It holds a single slot and is
	// written without blocking, so a daemon that polls faster than the menu
	// redraws simply overwrites the value nobody has read yet.
	updates chan Status

	done     chan struct{}
	doneOnce sync.Once

	status *systray.MenuItem
	artist *systray.MenuItem
	pause  *systray.MenuItem
	quit   *systray.MenuItem
}

// New returns a Tray. onPause is called when the user toggles the suspend item.
func New(version string, onPause func(paused bool)) *Tray {
	return &Tray{
		version: version,
		onPause: onPause,
		updates: make(chan Status, 1),
		done:    make(chan struct{}),
	}
}

// Run starts the platform event loop and blocks until the tray exits. It must
// be called from the main goroutine, which systray pins to the main OS thread.
// onReady runs once the menu exists.
func (t *Tray) Run(onReady func()) {
	systray.Run(func() {
		t.build()
		go t.loop()
		if onReady != nil {
			onReady()
		}
	}, t.signalDone)
}

// Done is closed once the tray has exited, whether from the menu or from Quit.
func (t *Tray) Done() <-chan struct{} { return t.done }

// Quit tears the tray down, unblocking Run.
func (t *Tray) Quit() { systray.Quit() }

func (t *Tray) signalDone() { t.doneOnce.Do(func() { close(t.done) }) }

// Update queues a status for display. It is safe from any goroutine and never
// blocks: if an update is already pending it is replaced, since only the most
// recent one is worth drawing.
func (t *Tray) Update(s Status) {
	select {
	case t.updates <- s:
		return
	default:
	}
	// A status is already queued and has not been drawn yet. Drop it in favour
	// of this newer one; drawing a stale status would be worse than skipping it.
	select {
	case <-t.updates:
	default:
	}
	select {
	case t.updates <- s:
	default:
	}
}

func (t *Tray) build() {
	setIcon()
	systray.SetTooltip("Apple Music Rich Presence")

	t.status = systray.AddMenuItem("Starting…", "")
	t.status.Disable()
	t.artist = systray.AddMenuItem("", "")
	t.artist.Disable()
	t.artist.Hide()

	systray.AddSeparator()
	t.pause = systray.AddMenuItemCheckbox("Suspend presence", "Stop publishing to Discord without quitting", false)

	systray.AddSeparator()
	version := systray.AddMenuItem("Version "+t.version, "")
	version.Disable()
	t.quit = systray.AddMenuItem("Quit", "Clear the presence and exit")
}

// loop owns every menu mutation, so the daemon never touches systray directly.
func (t *Tray) loop() {
	for {
		select {
		case s := <-t.updates:
			t.apply(s)

		case <-t.pause.ClickedCh:
			paused := !t.pause.Checked()
			if paused {
				t.pause.Check()
			} else {
				t.pause.Uncheck()
			}
			if t.onPause != nil {
				t.onPause(paused)
			}

		case <-t.quit.ClickedCh:
			systray.Quit()
			return

		case <-t.done:
			return
		}
	}
}

func (t *Tray) apply(s Status) {
	t.status.SetTitle(s.summary())

	if detail := s.detail(); detail != "" {
		t.artist.SetTitle(detail)
		t.artist.Show()
	} else {
		t.artist.Hide()
	}

	tooltip := "Apple Music Rich Presence"
	if s.Track != "" {
		tooltip = s.summary()
	}
	systray.SetTooltip(tooltip)
}
