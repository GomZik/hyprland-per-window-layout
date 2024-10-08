package hypr

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
)

var (
	ErrClosed = fmt.Errorf("clinet: closed")
)

type Client struct {
	closed bool
	reader *textproto.Reader
}

type Event struct {
	Name string
	Args []string
}

type Keyboard struct {
	Layout       string `json:"layout"`
	ActiveKeymap string `json:"active_keymap"`
	Main         bool   `json:"main"`
	Name         string `json:"name"`
}

type DevicesResponse struct {
	Keyboards []Keyboard `json:"keyboards"`
}

func NewClient() (*Client, func(), error) {
	hs := new(Client)
	sign, exists := os.LookupEnv("HYPRLAND_INSTANCE_SIGNATURE")
	if !exists {
		return nil, nil, fmt.Errorf("do you have Hyprland instance launched?")
	}
	currentUser, err := user.Current()
	if err != nil {
		return nil, nil, fmt.Errorf("don't know who are you: %w", err)
	}

	socketPath := fmt.Sprintf("/run/user/%s/hypr/%s/.socket2.sock", currentUser.Uid, sign)
	sock, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, nil, fmt.Errorf("can't connect to Hyprland event socket: %w.", err)
	}

	hs.reader = textproto.NewReader(bufio.NewReader(sock))
	return hs, func() {
		hs.closed = true
		sock.Close()
	}, nil
}

func (c *Client) ReadEvent() (Event, error) {
	if c.closed {
		return Event{}, ErrClosed
	}
	data, err := c.reader.ReadLine()
	if err != nil {
		return Event{}, fmt.Errorf("failed to read from socket2.sock: %w", err)
	}
	evtParts := strings.Split(data, ">>")
	if len(evtParts) == 0 {
		return Event{}, fmt.Errorf("got event, but the format is unexpected")
	}
	evt := Event{
		Name: evtParts[0],
		Args: strings.Split(evtParts[1], ","),
	}
	return evt, nil
}

func (c *Client) SwitchXKBLayout(layoutIdx int) error {
	cmd := exec.Command("hyprctl", "switchxkblayout", "all", strconv.Itoa(layoutIdx))
	return cmd.Run()
}

func (c *Client) ReadLayouts() ([]string, error) {
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
		if err := c.SwitchXKBLayout(i); err != nil {
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
	if err := c.SwitchXKBLayout(activeLayoutIdx); err != nil {
		return nil, fmt.Errorf("failed to activate back layout that used before gathering: %w", err)
	}
	return result, nil
}
