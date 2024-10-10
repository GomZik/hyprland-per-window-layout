package main

import (
	"fmt"
	"log/slog"
	"os"
	"perwindowlayout/hypr"
	"time"
)

func processHyprlandEvents(resetRetryCount func()) error {
	client, clientClose, err := hypr.NewClient()
	if err != nil {
		return fmt.Errorf("could not connect to the hyprland socket: %w", err)
	}
	defer clientClose()

	layouts, err := client.ReadLayouts()
	if err != nil {
		return fmt.Errorf("could not detect layouts: %w", err)
	}
	slog.Debug(fmt.Sprintf("Layouts: %v", layouts))
	layoutToIndex := make(map[string]int)
	for i, l := range layouts {
		layoutToIndex[l] = i
	}
	slog.Debug(fmt.Sprintf("Index Mapping: %+v", layoutToIndex))

	layoutMap := make(map[string]int, 0)
	defaultLayout := 0
	currentWindowId := ""
	currentLayout := -1

	for {
		evt, err := client.ReadEvent()
		if err != nil {
			return fmt.Errorf("failed to read hyprland event: %w", err)
		}
		resetRetryCount()
		switch evt.Name {
		case "activelayout":
			{
				if currentWindowId == "" {
					continue
				}
				currentLayout = layoutToIndex[evt.Args[len(evt.Args)-1]]
				layoutMap[currentWindowId] = currentLayout
			}
		case "activewindowv2":
			{
				newWindowId := evt.Args[len(evt.Args)-1]
				if currentWindowId == newWindowId {
					continue
				}
				currentWindowId = newWindowId
				windowLayout, known := layoutMap[currentWindowId]
				if !known {
					windowLayout = defaultLayout
				}
				if windowLayout == currentLayout {
					continue
				}
				err := client.SwitchXKBLayout(windowLayout)
				if err != nil {
					return fmt.Errorf("failed to activate layout: %w", err)
				}
			}
		}
	}

}

func main() {
	logfile, err := os.OpenFile(os.ExpandEnv("$HOME/.per-window-layout.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0655)
	if err != nil {
		panic(fmt.Errorf("Could not open logfile: %w", err))
	}
	h := slog.NewTextHandler(logfile, &slog.HandlerOptions{Level: slog.LevelDebug})
	slog.SetDefault(slog.New(h))

	retry := 0
	retryWait := []time.Duration{
		500 * time.Millisecond,
		time.Second,
		2 * time.Second,
		4 * time.Second,
	}
	resetRetry := func() {
		retry = 0
	}
	for {
		if err := processHyprlandEvents(resetRetry); err != nil {
			slog.Error(err.Error())
			if retry >= len(retryWait) {
				panic(err)
			}
			slog.Info(fmt.Sprintf("Waiting %s for recover", retryWait[retry]), "retry", retry)
			<-time.After(retryWait[retry])
			retry += 1
		}
	}
}
