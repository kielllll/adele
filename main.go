// Command adele is a minimal YouTube streaming TUI.
package main

import (
	"fmt"
	"os"

	"adele/internal/player"
	"adele/internal/source"
	"adele/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "adele:", err)
		os.Exit(1)
	}
}

func run() error {
	client, err := source.New()
	if err != nil {
		return err
	}
	p, err := player.New()
	var mpvErr string
	if err != nil {
		// Degraded mode: UI runs so search still works; playback keys
		// report the install hint instead of failing silently.
		mpvErr = err.Error()
	} else {
		defer p.Close()
	}
	program := tea.NewProgram(ui.New(client, p, mpvErr), tea.WithAltScreen())
	if p != nil {
		// Route mpv end-file events into the Bubble Tea loop.
		p.OnEnd = func() { go program.Send(ui.TrackEnded()) }
	}
	if _, err := program.Run(); err != nil {
		return err
	}
	return nil
}
