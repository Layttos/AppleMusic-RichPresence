package player

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/saltosystems/winrt-go"
	"github.com/saltosystems/winrt-go/windows/foundation"
	"github.com/saltosystems/winrt-go/windows/media/control"
	"golang.org/x/sys/windows"
)

// Windows has no AppleScript. Instead we read the System Media Transport
// Controls, the same source that feeds the volume-flyout media widget. Apple
// Music and iTunes both publish to it, so this works for either.
//
// The alternative — driving PowerShell to reach the same API — would cost a
// resident interpreter of roughly fifty megabytes. Calling WinRT directly keeps
// the daemon at a few megabytes, which is the whole point.

// appleMusicHints match the Application User Model ID of the media session.
// The Store build reports something like "AppleInc.AppleMusicWin_...!App" and
// the legacy desktop client reports "iTunes.exe".
var appleMusicHints = []string{"applemusic", "apple music", "itunes"}

// asyncTimeout bounds a WinRT async call, so a wedged media session cannot park
// the poll loop.
const asyncTimeout = 5 * time.Second

// hresult values that mean "the apartment is already usable".
const (
	sFalse              = 0x00000001
	rpcEChangedMode     = 0x80010106
	ticksPerNanosecUnit = 100
)

// windowsPlayer serialises every WinRT call onto one dedicated OS thread.
// COM apartments are thread-affine, so the goroutine that calls RoInitialize
// must also be the one that makes every subsequent call.
type windowsPlayer struct {
	requests chan pollRequest
	quit     chan struct{}
	stopped  chan struct{}
	initErr  chan error
}

type pollRequest struct {
	reply chan pollResult
}

type pollResult struct {
	snapshot Snapshot
	err      error
}

// New returns a Player for this platform.
func New() (Player, error) {
	p := &windowsPlayer{
		requests: make(chan pollRequest),
		quit:     make(chan struct{}),
		stopped:  make(chan struct{}),
		initErr:  make(chan error, 1),
	}
	go p.run()

	if err := <-p.initErr; err != nil {
		<-p.stopped
		return nil, err
	}
	return p, nil
}

func (p *windowsPlayer) Close() error {
	select {
	case <-p.stopped:
		return nil
	default:
	}
	close(p.quit)
	<-p.stopped
	return nil
}

func (p *windowsPlayer) Poll(ctx context.Context) (Snapshot, error) {
	reply := make(chan pollResult, 1)

	select {
	case p.requests <- pollRequest{reply: reply}:
	case <-p.stopped:
		return Snapshot{}, errors.New("player: worker has stopped")
	case <-ctx.Done():
		return Snapshot{}, ctx.Err()
	}

	select {
	case res := <-reply:
		return res.snapshot, res.err
	case <-ctx.Done():
		return Snapshot{}, ctx.Err()
	}
}

// run owns the COM apartment and answers poll requests one at a time.
func (p *windowsPlayer) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(p.stopped)

	if err := initApartment(); err != nil {
		p.initErr <- err
		return
	}
	defer roUninitialize()
	p.initErr <- nil

	w := &comWorker{}
	defer w.releaseManager()

	for {
		select {
		case req := <-p.requests:
			snap, err := w.poll()
			req.reply <- pollResult{snapshot: snap, err: err}
		case <-p.quit:
			return
		}
	}
}

// initApartment joins the multi-threaded apartment, tolerating the codes that
// mean it is already usable.
func initApartment() error {
	err := ole.RoInitialize(1) // RO_INIT_MULTITHREADED
	if err == nil {
		return nil
	}
	var oleErr *ole.OleError
	if errors.As(err, &oleErr) {
		switch uint32(oleErr.Code()) {
		case sFalse, rpcEChangedMode:
			return nil
		}
	}
	return fmt.Errorf("initialising the COM apartment: %w", err)
}

// comWorker holds the WinRT state that lives across polls.
type comWorker struct {
	manager *control.GlobalSystemMediaTransportControlsSessionManager
}

func (w *comWorker) releaseManager() {
	if w.manager != nil {
		w.manager.Release()
		w.manager = nil
	}
}

// ensureManager fetches the session manager once and caches it. Requesting it
// on every poll would mean an async round trip and a COM object to release each
// time, for an object that does not change.
func (w *comWorker) ensureManager() (*control.GlobalSystemMediaTransportControlsSessionManager, error) {
	if w.manager != nil {
		return w.manager, nil
	}

	op, err := control.GlobalSystemMediaTransportControlsSessionManagerRequestAsync()
	if err != nil {
		return nil, fmt.Errorf("requesting the media session manager: %w", err)
	}
	defer op.Release()

	ptr, err := awaitOperation(op, control.SignatureGlobalSystemMediaTransportControlsSessionManager)
	if err != nil {
		return nil, fmt.Errorf("awaiting the media session manager: %w", err)
	}
	if ptr == nil {
		return nil, errors.New("the media session manager was nil")
	}

	w.manager = (*control.GlobalSystemMediaTransportControlsSessionManager)(ptr)
	return w.manager, nil
}

// poll reads the Apple Music session, if there is one.
func (w *comWorker) poll() (Snapshot, error) {
	mgr, err := w.ensureManager()
	if err != nil {
		// Drop the cached manager so the next poll rebuilds it; Explorer
		// restarting can invalidate it.
		w.releaseManager()
		return Snapshot{}, err
	}

	session, err := appleMusicSession(mgr)
	if err != nil {
		w.releaseManager()
		return Snapshot{}, err
	}
	if session == nil {
		// Apple Music is not publishing a media session, so as far as this
		// daemon is concerned it is not running.
		return Snapshot{}, nil
	}
	defer session.Release()

	track, err := readTrack(session)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Running: true, Track: track}, nil
}

// appleMusicSession returns the session belonging to Apple Music, or nil.
// The caller owns the returned session and must Release it.
func appleMusicSession(mgr *control.GlobalSystemMediaTransportControlsSessionManager) (*control.GlobalSystemMediaTransportControlsSession, error) {
	sessions, err := mgr.GetSessions()
	if err != nil {
		return nil, fmt.Errorf("listing media sessions: %w", err)
	}
	if sessions == nil {
		return nil, nil
	}
	defer sessions.Release()

	size, err := sessions.GetSize()
	if err != nil {
		return nil, fmt.Errorf("counting media sessions: %w", err)
	}

	for i := uint32(0); i < size; i++ {
		ptr, err := sessions.GetAt(i)
		if err != nil || ptr == nil {
			continue
		}
		session := (*control.GlobalSystemMediaTransportControlsSession)(ptr)

		id, err := session.GetSourceAppUserModelId()
		if err != nil {
			session.Release()
			continue
		}
		if isAppleMusic(id) {
			return session, nil
		}
		// Someone else's session — Spotify, a browser — so let it go.
		session.Release()
	}
	return nil, nil
}

// isAppleMusic reports whether an Application User Model ID belongs to Apple
// Music or iTunes.
func isAppleMusic(id string) bool {
	id = strings.ToLower(id)
	for _, hint := range appleMusicHints {
		if strings.Contains(id, hint) {
			return true
		}
	}
	return false
}

// readTrack pulls metadata, timeline and playback state off a session.
func readTrack(session *control.GlobalSystemMediaTransportControlsSession) (Track, error) {
	state, err := readState(session)
	if err != nil {
		return Track{}, err
	}
	if state == StateStopped {
		return Track{State: StateStopped}, nil
	}

	op, err := session.TryGetMediaPropertiesAsync()
	if err != nil {
		return Track{}, fmt.Errorf("requesting media properties: %w", err)
	}
	defer op.Release()

	ptr, err := awaitOperation(op, control.SignatureGlobalSystemMediaTransportControlsSessionMediaProperties)
	if err != nil {
		return Track{}, fmt.Errorf("awaiting media properties: %w", err)
	}
	if ptr == nil {
		return Track{State: StateStopped}, nil
	}
	props := (*control.GlobalSystemMediaTransportControlsSessionMediaProperties)(ptr)
	defer props.Release()

	title, err := props.GetTitle()
	if err != nil {
		return Track{}, fmt.Errorf("reading the title: %w", err)
	}
	artist, err := props.GetArtist()
	if err != nil {
		return Track{}, fmt.Errorf("reading the artist: %w", err)
	}
	album, err := props.GetAlbumTitle()
	if err != nil {
		// Not every source fills the album in; it is not worth failing over.
		album = ""
	}

	position, duration := readTimeline(session)

	return Track{
		Title:    title,
		Artist:   artist,
		Album:    album,
		Position: position,
		Duration: duration,
		State:    state,
	}, nil
}

func readState(session *control.GlobalSystemMediaTransportControlsSession) (State, error) {
	info, err := session.GetPlaybackInfo()
	if err != nil {
		return StateStopped, fmt.Errorf("reading playback info: %w", err)
	}
	if info == nil {
		return StateStopped, nil
	}
	defer info.Release()

	status, err := info.GetPlaybackStatus()
	if err != nil {
		return StateStopped, fmt.Errorf("reading the playback status: %w", err)
	}

	switch status {
	case control.GlobalSystemMediaTransportControlsSessionPlaybackStatusPlaying:
		return StatePlaying, nil
	case control.GlobalSystemMediaTransportControlsSessionPlaybackStatusPaused:
		return StatePaused, nil
	default:
		return StateStopped, nil
	}
}

// readTimeline returns the position and duration, or zeroes when the session
// does not report them. A missing timeline is not fatal: the presence simply
// loses its progress bar.
func readTimeline(session *control.GlobalSystemMediaTransportControlsSession) (position, duration time.Duration) {
	timeline, err := session.GetTimelineProperties()
	if err != nil || timeline == nil {
		return 0, 0
	}
	defer timeline.Release()

	if pos, err := timeline.GetPosition(); err == nil {
		position = timeSpan(pos)
	}
	if end, err := timeline.GetEndTime(); err == nil {
		duration = timeSpan(end)
	}
	// Some sources report the timeline relative to a non-zero start.
	if start, err := timeline.GetStartTime(); err == nil {
		if offset := timeSpan(start); offset > 0 {
			position -= offset
			duration -= offset
		}
	}
	if position < 0 {
		position = 0
	}
	if duration < 0 {
		duration = 0
	}
	return position, duration
}

// timeSpan converts a WinRT TimeSpan, counted in 100-nanosecond ticks.
func timeSpan(ts foundation.TimeSpan) time.Duration {
	return time.Duration(ts.Duration) * ticksPerNanosecUnit
}

// awaitOperation blocks until a WinRT async operation settles, then returns its
// result. resultSignature is the WinRT signature of the operation's result
// type, needed to derive the completion handler's parameterised interface ID.
//
// The caller owns the returned pointer and must Release it.
func awaitOperation(op *foundation.IAsyncOperation, resultSignature string) (unsafe.Pointer, error) {
	iid := winrt.ParameterizedInstanceGUID(foundation.GUIDAsyncOperationCompletedHandler, resultSignature)

	// Buffered, because the handler runs on a thread-pool thread and may even
	// fire synchronously if the operation has already completed.
	done := make(chan foundation.AsyncStatus, 1)
	handler := foundation.NewAsyncOperationCompletedHandler(
		ole.NewGUID(iid),
		func(_ *foundation.AsyncOperationCompletedHandler, _ *foundation.IAsyncOperation, status foundation.AsyncStatus) {
			done <- status
		},
	)
	defer handler.Release()

	if err := op.SetCompleted(handler); err != nil {
		return nil, fmt.Errorf("registering the completion handler: %w", err)
	}

	timer := time.NewTimer(asyncTimeout)
	defer timer.Stop()

	select {
	case status := <-done:
		if status != foundation.AsyncStatusCompleted {
			return nil, fmt.Errorf("async operation ended with status %d", status)
		}
	case <-timer.C:
		return nil, errors.New("async operation timed out")
	}

	return op.GetResults()
}

// go-ole exposes RoInitialize but not its counterpart, so RoUninitialize is
// resolved from combase.dll directly. It runs once, as the worker thread is
// torn down.
var (
	modcombase         = windows.NewLazySystemDLL("combase.dll")
	procRoUninitialize = modcombase.NewProc("RoUninitialize")
)

func roUninitialize() {
	if err := procRoUninitialize.Find(); err != nil {
		return
	}
	procRoUninitialize.Call()
}
