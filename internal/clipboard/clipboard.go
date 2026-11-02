package clipboard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	native "github.com/atotto/clipboard"
)

func Write(body string) error {
	if runtime.GOOS == "windows" {
		return native.WriteAll(body)
	}
	var commands [][]string
	if runtime.GOOS == "darwin" {
		commands = [][]string{{"pbcopy"}}
	} else {
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			commands = append(commands, []string{"wl-copy", "--type", "text/plain;charset=utf-8"})
		}
		if os.Getenv("DISPLAY") != "" {
			commands = append(commands, []string{"xclip", "-selection", "clipboard"}, []string{"xsel", "--clipboard", "--input"})
		}
		if os.Getenv("WAYLAND_DISPLAY") == "" && os.Getenv("DISPLAY") == "" {
			return fmt.Errorf("no graphical clipboard session available")
		}
	}
	var last error
	for _, args := range commands {
		if _, err := exec.LookPath(args[0]); err != nil {
			last = err
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Stdin = strings.NewReader(body)
		// Clipboard owners can fork; do not wait for inherited stdout/stderr pipes.
		cmd.Stdout, cmd.Stderr = nil, nil
		last = cmd.Run()
		cancel()
		if last == nil {
			return nil
		}
	}
	return fmt.Errorf("clipboard unavailable (install wl-clipboard, xclip or xsel on Linux): %v", last)
}
