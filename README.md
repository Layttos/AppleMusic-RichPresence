# Apple Music Rich Presence

Shows the track you are playing in Apple Music on your Discord profile.
Runs on **macOS** and **Windows**, from the menu bar or the notification area.

<img width="461" height="196" alt="Discord rich presence showing an Apple Music track" src="https://github.com/user-attachments/assets/0283c2e6-9911-47ca-a57f-260406401961" />

---

## Install

### macOS

Build the application bundle and drop it in `/Applications`:

```bash
make mac-app && cp -R dist/AppleMusicRP.app /Applications/
```

Open it once from Finder. macOS will ask for permission to control Apple Music —
that prompt is what lets the app read the current track, so allow it. The icon
appears in the menu bar; there is no Dock icon and no window.

To start it at login: **System Settings → General → Login Items → Open at Login → +**,
then pick `AppleMusicRP`.

### Windows

Run `AppleMusicRP-Setup-<version>.exe`. It installs into your own profile, so
Windows does not ask for administrator rights, and it offers to start the app
when you sign in. Nothing else is needed: Apple Music and iTunes both report to
the system media controls, which is where the app reads from.

The icon appears in the notification area, next to the clock.

To build the installer yourself:

```powershell
.\build-windows.ps1
```

That needs [Inno Setup](https://jrsoftware.org/isinfo.php), which is one command
away — `winget install JRSoftware.InnoSetup` — and leaves both the executable
and the installer in `dist\`.

**Removing it.** *Settings → Apps → Installed apps → Apple Music Rich
Presence → Uninstall*, or the *Uninstall Apple Music Rich Presence* shortcut in
the Start menu, or `unins000.exe` in the install folder. Any of the three takes
away the files, the shortcuts and the autostart entry.

If you would rather not install anything, `make windows` still produces a
standalone `dist\applemusic-rp.exe` that runs from wherever you put it. To start
that one at login, press <kbd>Win</kbd>+<kbd>R</kbd>, run `shell:startup`, and
put a shortcut to it in the folder that opens.

### Headless (no icon)

If you would rather run it purely in the background:

```bash
make install-mac     # installs a LaunchAgent and starts it
make uninstall-mac   # removes it
```

The headless build is also the lighter one: about 7 MB of memory against roughly
20 MB with the menu bar, because the menu bar pulls in the system UI frameworks.

---

## Using it

The menu shows what is playing and whether Discord is connected. **Suspend
presence** stops publishing without quitting — useful when you would rather not
broadcast what you are listening to. **Quit** clears the presence and exits.

Command line flags:

| Flag | Meaning |
| --- | --- |
| `-headless` | Run without the menu bar or tray icon |
| `-v` | Verbose logging, one line per poll |
| `-client-id` | Use a different Discord application ID |
| `-version` | Print the version and exit |

---

## Build from source

Requires Go 1.25 or later. On macOS the menu bar build needs the Xcode command
line tools (`xcode-select --install`); the `notray` build does not.

```bash
make check    # gofmt, go vet for macOS and Windows, and the tests
make build    # a plain binary for the current platform
make icons    # rasterise internal/ui/icon.svg after changing the artwork
```

The artwork is a single SVG. `make icons` turns it into the template PNG the
menu bar wants and the multi-size ICO Windows wants, and `make syso` writes the
icon, the manifest and the version information into the object file the Go
linker folds into the executable. Both are plain Go: building for Windows needs
no resource compiler, and swapping in a different SVG needs no code change.

---

## How it works

```
player  ──►  daemon  ──►  discord
                │
                └──►  artwork
```

- **`internal/player`** reads the now-playing state. On macOS it first checks the
  process table with a `sysctl` — a few microseconds — and only spawns
  `osascript` once Apple Music is confirmed to be running. On Windows it calls
  the `GlobalSystemMediaTransportControls` WinRT API directly, so there is no
  PowerShell interpreter sitting in memory.
- **`internal/discord`** speaks Discord's local IPC protocol over a Unix socket
  or a named pipe. The connection is treated as disposable: Discord may not be
  running at startup, and may restart at any time.
- **`internal/artwork`** resolves cover art through the iTunes Search API, with a
  bounded LRU cache that also remembers misses.
- **`internal/ui`** is the menu bar and notification area item. Building with
  `-tags notray` removes it and its dependencies entirely.

### Why it stays small

A background app that runs for weeks has to be careful about three things:

- **Do nothing when nothing is playing.** While Apple Music is closed the poll is
  a single syscall, with no subprocess and no network.
- **Publish only on change.** Discord animates its own progress bar from the
  start and end timestamps, so a track that simply plays on is sent exactly once
  — not every few seconds.
- **Bound everything that grows.** The artwork cache has a fixed size, incoming
  IPC frames are size-checked before being allocated, and repeated errors are
  logged once rather than on every poll.

Measured over eight minutes of continuous playback, resident memory oscillates
between 5 and 12 MB with no upward trend.

---

## Troubleshooting

**The presence does not appear.**
Check the menu: it will say whether Apple Music is closed, whether Discord is
unreachable, or whether the presence is suspended. Discord must be the desktop
application — the browser version has no local IPC endpoint.

**macOS never asked for permission, or the presence stopped after a system update.**
Open **System Settings → Privacy & Security → Automation** and make sure
`AppleMusicRP` is allowed to control `Music`.

**Logs.**
Run from a terminal with `-v` to see every poll. The LaunchAgent writes to
`~/Library/Logs/AppleMusicRP/`.

---

## License

MIT — see [LICENSE](LICENSE).

The icon is *music-notes* from [Phosphor Icons](https://phosphoricons.com), also
MIT; the licence text is in [NOTICE](NOTICE).
