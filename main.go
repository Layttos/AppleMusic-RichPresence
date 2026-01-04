package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

type AppleResponse struct {
	ResultCount int `json:"resultCount"`
	Results     []struct {
		ArtworkUrl100 string `json:"artworkUrl100"`
	} `json:"results"`
}

func main() {
	fmt.Println("Starting AMRichPresence for macOS...")
	os.Setenv("PATH", "/usr/local/bin:"+os.Getenv("PATH"))

	clientrp, err := NewClient("1457120161911013437")
	if err != nil {
		fmt.Println("Error creating Discord client:", err)
		return
	}

	for range time.Tick(time.Second * 5) {
		updateRichPresence(clientrp)
	}
}

func updateRichPresence(clientrp *DiscordClient) {
	info := exec.Command("osascript", "-e", `
on is_running(appName)
	tell application "System Events" to (name of processes) contains appName
end is_running

set appRunning to is_running("Music")
if appRunning is true then
	tell application "Music"
		set trackName to name of current track
		set trackArtist to artist of current track
		set trackAlbum to album of current track
		set playerPosition to player position
		set trackDuration to duration of current track
		
		set rPosition to (round (playerPosition * 100)) / 100
		set rDuration to (round (trackDuration * 100)) / 100
		
		return trackName & "|" & trackArtist & "|" & rPosition & "|" & rDuration & "|" & trackAlbum
	end tell
else
	return "App not running... Waiting for it to load"
end if
	`)
	output, err := info.Output()
	if err != nil {
		fmt.Println("Error retrieving track name:", err)
		return
	}
	if strings.Contains(string(output), "App not running") {
		fmt.Println("Music app is not running. Clearing Discord Rich Presence.")
		err := clientrp.ClearActivity()
		if err != nil {
			panic(err)
		}
		return
	}

	artworkURL := "music"
	informations := strings.Split(string(output), "|")
	fmt.Println("Track Name:", informations[0])
	fmt.Println("Artist:", informations[1])
	fmt.Println("Position (s):", informations[2])
	fmt.Println("Duration (s):", informations[3])

	searchURL := "https://itunes.apple.com/search?term=" + url.QueryEscape(informations[0]+" "+informations[1]) + "&entity=song&limit=1"
	resp, err := http.Get(searchURL)
	if err != nil {
		panic(err)
	}

	defer resp.Body.Close()
	var data AppleResponse

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		fmt.Println("Erreur de lecture : ", err)
		return
	}

	if data.ResultCount > 0 {
		artworkURL = data.Results[0].ArtworkUrl100
		artworkURL = strings.Replace(artworkURL, "100x100bb.jpg", "512x512.jpg", 1)
	} else {
		fmt.Println("No results found in iTunes API for artwork.")
		artworkURL = "music"
	}

	var position, duration float64
	fmt.Sscanf(strings.Replace(informations[2], ",", ".", -1), "%f", &position)
	fmt.Sscanf(strings.Replace(informations[3], ",", ".", -1), "%f", &duration)

	clientrp.SetActivity(Activity{
		Type:    2,
		Details: informations[4],
		State:   "by " + informations[1],
		Assets: Assets{
			LargeImage: artworkURL,
			LargeText:  informations[1],
		},
		Timestamps: Timestamps{
			Start: time.Now().Unix() - int64(position),
			End:   time.Now().Unix() + int64(duration-position),
		},
	})

	fmt.Println("Discord Rich Presence updated successfully!")
}
