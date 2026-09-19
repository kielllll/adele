package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"adele/internal/player"
	"adele/internal/source"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// trackItem adapts source.Track to list.Item.
type trackItem struct{ t source.Track }

func (i trackItem) Title() string { return fmt.Sprintf("%s — %s", i.t.Title, i.t.Artist) }
func (i trackItem) Description() string {
	return fmt.Sprintf("%s · %s", i.t.Artist, source.FormatDuration(i.t.Duration))
}
func (i trackItem) FilterValue() string { return i.t.Title + " " + i.t.Artist }

type searchDoneMsg struct {
	tracks []source.Track
	err    error
}

type trackEndedMsg struct{}

// TrackEnded reports an mpv end-file event for auto-advance.
func TrackEnded() tea.Msg { return trackEndedMsg{} }

// Model is the 3-pane player: search / results / now-playing.
type Model struct {
	input  textinput.Model
	tracks list.Model
	client *source.Client
	player *player.Player

	status   string
	fatal    string
	now      *source.Track
	paused   bool
	volume   int
	width    int
	height   int
	searchCh chan searchDoneMsg
}

// New builds the UI. player may be nil when mpv is missing; the UI then
// shows the install hint and disables playback keys.
func New(client *source.Client, p *player.Player, mpvErr string) Model {
	ti := textinput.New()
	ti.Placeholder = "Search YouTube… (type to search, enter ↵, esc leaves box)"
	ti.Focus()
	ti.CharLimit = 120

	delegate := list.NewDefaultDelegate()
	l := list.New(nil, delegate, 0, 0)
	l.Title = "Results"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.DisableQuitKeybindings()

	m := Model{input: ti, tracks: l, client: client, player: p, volume: 80}
	if mpvErr != "" {
		m.fatal = mpvErr
	}
	return m
}

func (m Model) Init() tea.Cmd {
	return textinput.Blink
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.Width = msg.Width - 4
		m.tracks.SetSize(msg.Width-4, msg.Height-10)
		return m, nil
	case searchDoneMsg:
		if msg.err != nil {
			m.status = "search error: " + msg.err.Error()
			return m, nil
		}
		items := make([]list.Item, 0, len(msg.tracks))
		for _, t := range msg.tracks {
			t := t
			items = append(items, trackItem{t: t})
		}
		m.tracks.SetItems(items)
		if len(items) == 0 {
			m.status = "no results"
		} else {
			m.status = fmt.Sprintf("%d results — enter play · space pause", len(items))
		}
		return m, nil
	case trackEndedMsg:
		m.advance(1)
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		if m.player != nil {
			_ = m.player.Close()
		}
		return m, tea.Quit
	}
	// While the search box is focused every key is text, except
	// enter (run the search) and esc (leave the box).
	if m.input.Focused() {
		switch msg.String() {
		case "enter":
			query := strings.TrimSpace(m.input.Value())
			if query == "" {
				return m, nil
			}
			m.input.Blur()
			m.status = "searching…"
			return m, m.searchCmd(query)
		case "esc":
			m.input.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	// Search box blurred: hotkeys are live.
	switch msg.String() {
	case "q":
		if m.player != nil {
			_ = m.player.Close()
		}
		return m, tea.Quit
	case "/":
		m.input.Focus()
		return m, nil
	case "enter":
		m.playSelected()
		return m, nil
	case " ":
		m.togglePause()
		return m, nil
	case "n":
		m.advance(1)
		return m, nil
	case "p":
		m.advance(-1)
		return m, nil
	case "s":
		if m.player != nil {
			_ = m.player.Stop()
			m.now = nil
			m.status = "stopped"
		}
		return m, nil
	case "left":
		if m.player != nil {
			_ = m.player.Seek(-5)
		}
		return m, nil
	case "right":
		if m.player != nil {
			_ = m.player.Seek(5)
		}
		return m, nil
	case "+", "=":
		m.setVolume(m.volume + 5)
		return m, nil
	case "-", "_":
		m.setVolume(m.volume - 5)
		return m, nil
	default:
		// Typing a printable character refocuses search and types it,
		// so focus is never silently stuck in the results list.
		if isPrintable(msg) {
			m.input.Focus()
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
		var cmd tea.Cmd
		m.tracks, cmd = m.tracks.Update(msg)
		return m, cmd
	}
}

// isPrintable reports whether msg is a single printable rune key press
// (a letter, digit, punctuation, …) as opposed to a named special key.
func isPrintable(msg tea.KeyMsg) bool {
	if msg.Type != tea.KeyRunes || len(msg.Runes) != 1 {
		return false
	}
	r := msg.Runes[0]
	return r >= 0x20 && r != 0x7f
}

func (m *Model) searchCmd(query string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		tracks, err := m.client.Search(ctx, query, 10)
		return searchDoneMsg{tracks: tracks, err: err}
	}
}

func (m *Model) playSelected() {
	if m.player == nil {
		m.status = "cannot play: " + m.fatal
		return
	}
	sel, ok := m.tracks.SelectedItem().(trackItem)
	if !ok {
		m.status = "nothing selected"
		return
	}
	t := sel.t
	if err := m.player.Play(t.URL); err != nil {
		m.status = "play error: " + err.Error()
		return
	}
	_ = m.player.SetVolume(m.volume)
	m.now = &t
	m.paused = false
	m.status = "playing"
}

func (m *Model) togglePause() {
	if m.player == nil || m.now == nil {
		return
	}
	m.paused = !m.paused
	if err := m.player.SetPaused(m.paused); err != nil {
		m.status = "pause error: " + err.Error()
		return
	}
	if m.paused {
		m.status = "paused"
	} else {
		m.status = "playing"
	}
}

func (m *Model) advance(step int) {
	items := m.tracks.Items()
	if len(items) == 0 {
		return
	}
	idx := m.tracks.Index() + step
	if idx < 0 {
		idx = 0
	}
	if idx >= len(items) {
		m.status = "end of results"
		return
	}
	m.tracks.Select(idx)
	m.playSelected()
}

func (m *Model) setVolume(v int) {
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	m.volume = v
	if m.player != nil {
		_ = m.player.SetVolume(v)
	}
	m.status = fmt.Sprintf("volume %d", v)
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true)
	barStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

// View renders search / results / now-playing panes.
func (m Model) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("adele — YouTube TUI"))
	b.WriteString("\n")
	b.WriteString(m.input.View())
	b.WriteString("\n")
	b.WriteString(m.tracks.View())
	b.WriteString("\n")
	if m.now != nil {
		state := "▶ playing"
		if m.paused {
			state = "⏸ paused"
		}
		fmt.Fprintf(&b, "%s\n", barStyle.Render(fmt.Sprintf("%s: %s — %s [%s] vol %d",
			state, m.now.Title, m.now.Artist, source.FormatDuration(m.now.Duration), m.volume)))
		fmt.Fprintf(&b, "%s\n", statusStyle.Render(fmt.Sprintf("♫ %s · via YouTube", m.now.URL)))
	} else {
		b.WriteString(statusStyle.Render("nothing playing"))
		b.WriteString("\n")
	}
	if m.fatal != "" {
		fmt.Fprintf(&b, "%s\n", errStyle.Render(m.fatal))
	}
	if m.status != "" {
		fmt.Fprintf(&b, "%s\n", statusStyle.Render(m.status))
	}
	b.WriteString(statusStyle.Render("enter play · space pause · n/p next/prev · ←/→ seek · +/- vol · s stop · / search · q quit"))
	return b.String()
}
