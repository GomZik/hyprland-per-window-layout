package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/textproto"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"time"
)

type Event struct {
	name string
	args []string
}

type Keyboard struct {
	Layout       string `json:"layout"`
	ActiveKeymap string `json:"active_keymap"`
	Main         bool   `json:"main"`
}

type DevicesResponse struct {
	Keyboards []Keyboard `json:"keyboards"`
}

func activateLayout(layoutIdx int) error {
	cmd := exec.Command("hyprctl", "switchxkblayout", "all", strconv.Itoa(layoutIdx))
	return cmd.Run()
}

func readLayouts() ([]string, error) {
	slog.Debug("Gathering layouts with Names")
	cmd := exec.Command("hyprctl", "devices", "-j")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to execute hyprctl: %w", err)
	}
	var response DevicesResponse
	if err := json.Unmarshal(out, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal hyprctl response: %w", err)
	}
	mainKb := response.Keyboards[0]
	for _, kb := range response.Keyboards {
		if kb.Main {
			mainKb = kb
			break
		}
	}
	layoutsShorts := strings.Split(mainKb.Layout, ",")
	result := make([]string, len(layoutsShorts))
	activeLayoutIdx := -1
	for i, l := range layoutsShorts {
		cmd := exec.Command("hyprctl", "switchxkblayout", "all", strconv.Itoa(i))
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("failed to switch to layout %s: %w", l, err)
		}
		cmd = exec.Command("hyprctl", "devices", "-j")
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("failed to read layout %s full name: %w", l, err)
		}
		var response DevicesResponse
		if err := json.Unmarshal(out, &response); err != nil {
			return nil, fmt.Errorf("failed to unmarshal devices info while fetching layout %s name: %w", l, err)
		}
		for _, kb := range response.Keyboards {
			if kb.Main {
				if kb.ActiveKeymap == mainKb.ActiveKeymap {
					activeLayoutIdx = i
				}
				result[i] = kb.ActiveKeymap
				break
			}
		}
	}
	if activeLayoutIdx == -1 {
		// Just ignore that case?
		slog.Warn("Before gathering information there was strange layout activated. Can't restore it")
		return result, nil
	}
	if err := activateLayout(activeLayoutIdx); err != nil {
		return nil, fmt.Errorf("failed to activate back layout that used before gathering: %w", err)
	}
	return result, nil
}

type HyprlandSocket struct {
	reader *textproto.Reader
}

func (hs *HyprlandSocket) Connect() error {
	sign, exists := os.LookupEnv("HYPRLAND_INSTANCE_SIGNATURE")
	if !exists {
		return fmt.Errorf("do you have Hyprland instance launched?")
	}
	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("don't know who are you: %w", err)
	}

	socketPath := fmt.Sprintf("/run/user/%s/hypr/%s/.socket2.sock", currentUser.Uid, sign)
	sock, err := net.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("can't connect to Hyprland event socket: %w.", err)
	}

	hs.reader = textproto.NewReader(bufio.NewReader(sock))
	return nil
}

func (hs *HyprlandSocket) ReadEvent() (Event, error) {
	data, err := hs.reader.ReadLine()
	if err != nil {
		return Event{}, fmt.Errorf("failed to read from socket2.sock: %w", err)
	}
	evtParts := strings.Split(data, ">>")
	if len(evtParts) == 0 {
		return Event{}, fmt.Errorf("got event, but the format is unexpected")
	}
	evt := Event{
		name: evtParts[0],
		args: strings.Split(evtParts[1], ","),
	}
	return evt, nil
}

func main() {
	logfile, err := os.OpenFile(os.ExpandEnv("$HOME/.per-window-layout.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0655)
	if err != nil {
		panic(fmt.Errorf("Could not open logfile: %w", err))
	}
	h := slog.NewTextHandler(logfile, &slog.HandlerOptions{Level: slog.LevelDebug})
	slog.SetDefault(slog.New(h))

	hs := HyprlandSocket{}
	if err := hs.Connect(); err != nil {
		err = fmt.Errorf("Could not connect to the hyprland socket: %w", err)
		slog.Error(err.Error())
		panic(err)
	}

	layouts, err := readLayouts()
	if err != nil {
		err = fmt.Errorf("Could not detect layouts: %w", err)
		slog.Error(err.Error())
		panic(err)
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

	retry := 0
	for {
		if retry > 3 {
			err = fmt.Errorf("Failed to reconnect after %d attempts", retry)
			slog.Error(err.Error())
			panic(err)

		}
		if retry > 0 {
			<-time.After(time.Duration(retry) * time.Second)
		}
		evt, err := hs.ReadEvent()
		if err != nil {
			retry += 1
			if err := hs.Connect(); err != nil {
				err = fmt.Errorf("Failed to reconnect to the socket when error is acquired while reading: %w", err)
				slog.Error(err.Error(), "Retry", retry)
			}
			continue
		}
		retry = 0
		switch evt.name {
		case "activelayout":
			{
				currentLayout = layoutToIndex[evt.args[len(evt.args)-1]]
				if currentWindowId == "" {
					continue
				}
				layoutMap[currentWindowId] = currentLayout
			}
		case "activewindowv2":
			{
				currentWindowId = evt.args[len(evt.args)-1]
				windowLayout, known := layoutMap[currentWindowId]
				if !known {
					windowLayout = defaultLayout
				}
				err := activateLayout(windowLayout)
				if err != nil {
					err = fmt.Errorf("Failed to activate layout: %w", err)
					slog.Error(err.Error())
					panic(err)
				}
			}
		}
	}
}
