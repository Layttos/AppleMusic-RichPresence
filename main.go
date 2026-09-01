// Command applemusic-rp publishes the track playing in Apple Music to Discord
// as a Rich Presence activity.
//
// It is meant to sit in the background for weeks at a time, so it is built
// around three rules: never assume Discord is reachable, never block forever on
// anything, and do no work when nothing is playing.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Layttos/AppleMusic-RichPresence/internal/artwork"
	"github.com/Layttos/AppleMusic-RichPresence/internal/discord"
	"github.com/Layttos/AppleMusic-RichPresence/internal/player"
	"github.com/Layttos/AppleMusic-RichPresence/internal/ui"
)

// defaultClientID is the Discord application whose name and assets the presence
// is shown under.
const defaultClientID = "1457120161911013437"

const (
	// idleInterval applies while Apple Music is closed. The check is a single
	// sysctl on macOS, so this is nearly free; it only governs how quickly the
	// presence appears once playback starts.
	idleInterval = 5 * time.Second

	// activeInterval applies while Apple Music is open, and sets how quickly a
	// track change is noticed.
	activeInterval = 3 * time.Second

	// pollTimeout bounds one read of the player.
	pollTimeout = 15 * time.Second

	// seekTolerance is how far the computed start time may drift before we
	// treat it as a seek worth republishing. Ordinary clock jitter stays well
	// inside it, so a track that simply plays on is published exactly once.
	seekTolerance = 3 * time.Second

	// fallbackImage is the asset key used when no cover art is found. It must
	// exist in the Discord application's art assets.
	fallbackImage = "music"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	var (
		clientID = flag.String("client-id", defaultClientID, "Discord application ID")
		headless = flag.Bool("headless", false, "run without a menu bar or tray icon")
		verbose  = flag.Bool("v", false, "log every poll, not just changes")
		showVer  = flag.Bool("version", false, "print the version and exit")
	)
	flag.Parse()

	if *showVer {
		fmt.Println("applemusic-rp", version)
		return
	}

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	// A background daemon should give memory back rather than sit on whatever
	// it once peaked at. A tighter GC target costs nothing at this heap size,
	// and the limit is a backstop against a runaway allocation.
	debug.SetGCPercent(50)
	debug.SetMemoryLimit(128 << 20)

	var err error
	if *headless || !ui.Supported() {
		err = runHeadless(log, *clientID)
	} else {
		err = runWithTray(log, *clientID)
	}
	if err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// runHeadless drives the daemon with no user interface, for launchd, Task
// Scheduler, or a terminal.
func runHeadless(log *slog.Logger, clientID string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var paused atomic.Bool
	// No tray: run reports status only when there is something to report to.
	return run(ctx, log, clientID, nil, &paused)
}

// runWithTray puts the daemon behind a menu bar or notification-area icon.
//
// The tray owns the main goroutine because macOS requires its event loop to run
// on the main thread, so the daemon moves to a goroutine of its own.
func runWithTray(log *slog.Logger, clientID string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var paused atomic.Bool
	tray := ui.New(version, func(p bool) {
		paused.Store(p)
		log.Info("presence publishing toggled", "paused", p)
	})

	// A signal has to unblock the tray's event loop too, or the process would
	// sit in the menu bar with nothing behind it.
	go func() {
		<-ctx.Done()
		tray.Quit()
	}()

	done := make(chan error, 1)
	tray.Run(func() {
		go func() {
			done <- run(runCtx, log, clientID, tray, &paused)
			// If the daemon stops on its own, take the icon down with it.
			tray.Quit()
		}()
	})

	// The tray has exited: the user chose Quit, or a signal arrived. Let the
	// daemon finish clearing the presence before returning.
	cancel()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		return nil
	}
}

// run is the daemon proper: poll the player, publish to Discord, repeat.
func run(ctx context.Context, log *slog.Logger, clientID string, tray *ui.Tray, paused *atomic.Bool) error {
	p, err := player.New()
	if err != nil {
		return fmt.Errorf("starting the player: %w", err)
	}
	defer p.Close()

	client := discord.New(clientID)
	// Clear the presence on the way out, so quitting does not leave a phantom
	// track on the profile.
	defer func() {
		if client.Connected() {
			if err := client.ClearActivity(); err != nil {
				log.Debug("could not clear the presence on shutdown", "err", err)
			}
		}
		client.Close()
	}()

	log.Info("started", "version", version, "platform", platformName())

	d := &daemon{
		log:      log,
		player:   p,
		client:   client,
		artwork:  artwork.New(),
		reporter: newReporter(log),
		tray:     tray,
		paused:   paused,
	}

	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("shutting down")
			return nil
		case <-timer.C:
			timer.Reset(d.tick(ctx))
		}
	}
}

// daemon holds the state that survives between polls.
type daemon struct {
	log      *slog.Logger
	player   player.Player
	client   *discord.Client
	artwork  *artwork.Resolver
	reporter *reporter
	tray     *ui.Tray
	paused   *atomic.Bool

	// published describes what Discord is currently showing, so that an
	// unchanged track is not republished every few seconds. Discord animates
	// the progress bar from the timestamps by itself, so a playing track needs
	// exactly one update.
	published   presence
	hasPresence bool

	// wasActive tracks the previous poll's outcome, used to notice the moment
	// Apple Music closes.
	wasActive bool
}

// presence is the fingerprint of a published activity.
type presence struct {
	key   string
	state player.State
	start int64
	end   int64
	image string
}

// tick performs one poll and returns how long to wait before the next.
func (d *daemon) tick(ctx context.Context) time.Duration {
	pollCtx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()

	snap, err := d.player.Poll(pollCtx)
	if err != nil {
		if ctx.Err() != nil {
			return idleInterval
		}
		d.reporter.report("player", err)
		d.report(ui.Status{Connected: d.client.Connected(), Paused: d.isPaused()})
		return idleInterval
	}
	d.reporter.clear("player")

	// A suspended presence still polls, so the menu keeps showing what is
	// playing; it just stops publishing.
	if d.isPaused() {
		d.clear()
		d.report(ui.Status{
			Track:     snap.Track.Title,
			Artist:    snap.Track.Artist,
			Playing:   snap.Track.State == player.StatePlaying,
			MusicOpen: snap.Running,
			Connected: d.client.Connected(),
			Paused:    true,
		})
		if snap.Running {
			return activeInterval
		}
		return idleInterval
	}

	if !snap.Running {
		d.goIdle()
		return idleInterval
	}

	if snap.Track.Title == "" || snap.Track.State == player.StateStopped {
		// Apple Music is open but nothing is loaded. The old daemon treated
		// this as an error and left a stale presence behind.
		d.clear()
		d.report(ui.Status{MusicOpen: true, Connected: d.client.Connected()})
		return activeInterval
	}

	d.publish(pollCtx, snap.Track)
	return activeInterval
}

func (d *daemon) isPaused() bool { return d.paused != nil && d.paused.Load() }

// report hands the current state to the tray, if there is one.
func (d *daemon) report(s ui.Status) {
	if d.tray != nil {
		d.tray.Update(s)
	}
}

// goIdle clears the presence and, on the transition, hands memory back.
func (d *daemon) goIdle() {
	d.clear()
	d.report(ui.Status{Connected: d.client.Connected()})
	if d.wasActive {
		d.log.Debug("apple music closed")
		// Return the poll-time peak to the OS instead of holding it for the
		// hours the app may now spend idle.
		debug.FreeOSMemory()
		d.wasActive = false
	}
}

func (d *daemon) clear() {
	if err := d.client.ClearActivity(); err != nil {
		d.reportDiscord(err)
		return
	}
	d.reporter.clear("discord")
	if d.hasPresence {
		d.log.Info("presence cleared")
	}
	d.hasPresence = false
	d.published = presence{}
}

// publish sends the track to Discord, skipping the call when nothing that
// Discord displays has actually changed.
func (d *daemon) publish(ctx context.Context, track player.Track) {
	d.wasActive = true

	now := time.Now()
	var start, end int64
	if track.State == player.StatePlaying && track.Duration > 0 {
		startTime := now.Add(-track.Position)
		start = startTime.Unix()
		end = startTime.Add(track.Duration).Unix()
	}

	key := track.Key()
	image := d.published.image
	// The fallback is retried rather than kept: landing on it usually means the
	// lookup failed while the machine was offline, and the resolver does not
	// cache network failures. A genuine "no such track" is cached there, so the
	// retry costs a map lookup.
	if key != d.published.key || image == "" || image == fallbackImage {
		image = d.artwork.Lookup(ctx, track.Title, track.Artist, track.Album)
		if image == "" {
			image = fallbackImage
		}
	}

	next := presence{key: key, state: track.State, start: start, end: end, image: image}
	if d.hasPresence && !d.changed(next) {
		d.log.Debug("presence unchanged", "track", track.Title)
		d.report(d.status(track))
		return
	}

	activity := discord.Activity{
		Type:    2, // Listening to
		Details: track.Title,
		State:   "by " + track.Artist,
		Assets: &discord.Assets{
			LargeImage: image,
			LargeText:  track.Album,
		},
	}
	if start != 0 {
		activity.Timestamps = &discord.Timestamps{Start: start, End: end}
	}

	if err := d.client.SetActivity(activity); err != nil {
		d.reportDiscord(err)
		d.report(d.status(track))
		return
	}
	d.reporter.clear("discord")

	d.published = next
	d.hasPresence = true
	d.report(d.status(track))
	d.log.Info("presence updated",
		"track", track.Title,
		"artist", track.Artist,
		"state", track.State.String(),
		"position", track.Position.Round(time.Second),
		"duration", track.Duration.Round(time.Second),
	)
}

func (d *daemon) status(track player.Track) ui.Status {
	return ui.Status{
		Track:     track.Title,
		Artist:    track.Artist,
		Playing:   track.State == player.StatePlaying,
		MusicOpen: true,
		Connected: d.client.Connected(),
		Paused:    d.isPaused(),
	}
}

// changed reports whether a new presence differs from the published one in a
// way Discord would show.
func (d *daemon) changed(next presence) bool {
	cur := d.published
	if next.key != cur.key || next.state != cur.state || next.image != cur.image {
		return true
	}
	// Position advances with the wall clock, so an untouched track keeps the
	// same start time. A jump means the listener seeked.
	if abs(next.start-cur.start) > int64(seekTolerance/time.Second) {
		return true
	}
	return abs(next.end-cur.end) > int64(seekTolerance/time.Second)
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// reportDiscord logs Discord problems, treating "not running" as routine.
func (d *daemon) reportDiscord(err error) {
	if errors.Is(err, discord.ErrNotConnected) {
		d.reporter.reportLevel("discord", err, slog.LevelDebug)
		return
	}
	d.reporter.report("discord", err)
}

// reporter collapses repeated errors into a single line.
//
// The previous daemon printed several lines per poll; six days of that left a
// two megabyte log file made almost entirely of the same sentence. Here a
// recurring fault is logged once, and again only when it changes or clears.
type reporter struct {
	log  *slog.Logger
	last map[string]string
}

func newReporter(log *slog.Logger) *reporter {
	return &reporter{log: log, last: make(map[string]string, 2)}
}

func (r *reporter) report(scope string, err error) {
	r.reportLevel(scope, err, slog.LevelWarn)
}

func (r *reporter) reportLevel(scope string, err error, level slog.Level) {
	msg := err.Error()
	if r.last[scope] == msg {
		return
	}
	r.last[scope] = msg
	r.log.Log(context.Background(), level, "problem", "scope", scope, "err", msg)
}

// clear notes that a scope is healthy again, logging the recovery only if
// something had gone wrong.
func (r *reporter) clear(scope string) {
	if _, bad := r.last[scope]; !bad {
		return
	}
	delete(r.last, scope)
	r.log.Info("recovered", "scope", scope)
}
