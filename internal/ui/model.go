package ui

import (
	"context"
	"errors"
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
	elapsed  float64 // seconds into the current track (from mpv)
	total    float64 // track length in seconds, 0 when unknown
	gen      int     // playback generation: stale ticks carry an old gen
	searchCh chan searchDoneMsg
}

// tickMsg refreshes playback position. gen invalidates ticks from an
// earlier playback so overlapping tick loops cannot multiply.
type tickMsg struct{ gen int }

// posMsg carries one background mpv position poll result.
type posMsg struct {
	pos float64
	dur float64
	err error
}

func tickCmd(gen int) tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{gen: gen} })
}

// New builds the UI. player may be nil when mpv is missing; the UI then
// shows the install hint and disables playback keys.
func New(client *source.Client, p *player.Player, mpvErr string) Model {
	ti := textinput.New()
	ti.Placeholder = "Search music.."
	ti.Focus()
	ti.CharLimit = 120

	delegate := list.NewDefaultDelegate()
	l := list.New(nil, delegate, 0, 0)
	l.Title = "Results"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.SetShowHelp(false)
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
		boxW := msg.Width - 4
		if boxW < 20 {
			boxW = 20
		}
		m.input.Width = boxW - 4
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
			m.status = fmt.Sprintf("%d results", len(items))
		}
		return m, nil
	case trackEndedMsg:
		return m, m.advance(1)
	case tickMsg:
		if msg.gen != m.gen || m.now == nil {
			return m, nil
		}
		return m, tea.Batch(m.posCmd(), tickCmd(m.gen))
	case posMsg:
		if msg.err == nil && m.now != nil {
			m.elapsed = msg.pos
			if msg.dur > 0 {
				m.total = msg.dur
			}
		}
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
		return m, m.playSelected()
	case " ":
		m.togglePause()
		return m, nil
	case "n":
		return m, m.advance(1)
	case "p":
		return m, m.advance(-1)
	case "s":
		if m.player != nil {
			_ = m.player.Stop()
			m.now = nil
			m.elapsed, m.total = 0, 0
			m.gen++ // invalidate any in-flight tick loop
			m.status = "stopped"
		}
		return m, nil
	case "left":
		if m.player != nil {
			_ = m.player.Seek(-5)
			m.elapsed -= 5
			if m.elapsed < 0 {
				m.elapsed = 0
			}
		}
		return m, nil
	case "right":
		if m.player != nil {
			_ = m.player.Seek(5)
			m.elapsed += 5
			if m.total > 0 && m.elapsed > m.total {
				m.elapsed = m.total
			}
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

// playSelected starts the highlighted track and kicks off the position
// tick loop. It returns the first tick command, or nil when nothing plays.
func (m *Model) playSelected() tea.Cmd {
	if m.player == nil {
		m.status = "cannot play: " + m.fatal
		return nil
	}
	sel, ok := m.tracks.SelectedItem().(trackItem)
	if !ok {
		m.status = "nothing selected"
		return nil
	}
	t := sel.t
	if err := m.player.Play(t.URL); err != nil {
		m.status = "play error: " + err.Error()
		return nil
	}
	_ = m.player.SetVolume(m.volume)
	m.now = &t
	m.paused = false
	m.elapsed = 0
	m.total = float64(t.Duration)
	m.gen++
	// No status text: the centered player panel already shows the state.
	return tickCmd(m.gen)
}

// posCmd polls mpv in the background so a slow query never blocks the UI.
// Errors keep the last displayed values.
func (m *Model) posCmd() tea.Cmd {
	p := m.player
	return func() tea.Msg {
		if p == nil {
			return posMsg{err: errors.New("no player")}
		}
		pos, dur, err := p.Position()
		return posMsg{pos: pos, dur: dur, err: err}
	}
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
	// No status text: the centered player panel already shows the state.
}

func (m *Model) advance(step int) tea.Cmd {
	items := m.tracks.Items()
	if len(items) == 0 {
		return nil
	}
	idx := m.tracks.Index() + step
	if idx < 0 {
		idx = 0
	}
	if idx >= len(items) {
		m.status = "end of results"
		return nil
	}
	m.tracks.Select(idx)
	return m.playSelected()
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

// Version is the TUI version shown in the footer. Bump on release.
const Version = "v0.1.0"

var (
	titleStyle  = lipgloss.NewStyle().Bold(true)
	homeTitle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13"))
	homeSub     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	searchBox   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("8")).Padding(0, 1)
	searchFocus = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("13")).Padding(0, 1)
	barStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

// isHome reports the idle homescreen: no results yet, nothing playing.
func (m Model) isHome() bool {
	return len(m.tracks.Items()) == 0 && m.now == nil
}

// View renders the homescreen when idle, else search / results / now-playing.
func (m Model) View() string {
	if m.isHome() {
		return m.homeView()
	}
	var b strings.Builder
	w := m.width
	if w <= 0 {
		w = 80
	}
	boxW := w - 4
	if boxW < 20 {
		boxW = 20
	}
	inp := m.input
	inp.Width = boxW - 4
	box := searchBox
	if m.input.Focused() {
		box = searchFocus
	}
	b.WriteString(box.Width(boxW).Render(inp.View()))
	b.WriteString("\n")
	b.WriteString(m.tracks.View())
	b.WriteString("\n")
	if m.fatal != "" {
		fmt.Fprintf(&b, "%s\n", errStyle.Render(m.fatal))
	}
	if m.status != "" {
		fmt.Fprintf(&b, "%s\n", statusStyle.Render(m.status))
	}
	if m.now != nil {
		b.WriteString(m.playerView())
		b.WriteString("\n")
	} else {
		b.WriteString(statusStyle.Render("nothing playing"))
		b.WriteString("\n")
	}
	// Pin the footer to the bottom row: pad the gap when content is
	// shorter than the terminal, never truncate when it overflows.
	h := m.height
	if h <= 0 {
		h = 24
	}
	// Body always ends with a newline, so newline count == row count
	// (lipgloss.Height overcounts here by one).
	if pad := h - strings.Count(b.String(), "\n") - 1; pad > 0 {
		b.WriteString(strings.Repeat("\n", pad))
	}
	b.WriteString(footerView(m.width))
	return b.String()
}

// footerHints lists the browser hotkeys shown in the footer.
const footerHints = "enter play · space pause · n/p next/prev · ←/→ seek · +/- vol · s stop · / search · q quit"

// footerView renders one footer row: hints on the left, version pinned to
// the lower-right corner. Hints truncate (rune-wise) on narrow screens so
// the row never wraps; the version is never dropped.
func footerView(w int) string {
	if w <= 0 {
		w = 80
	}
	hintsW := w - len(Version) - 1
	if hintsW < 10 {
		return lipgloss.Place(w, 1, lipgloss.Right, lipgloss.Center, statusStyle.Render(Version))
	}
	left := lipgloss.NewStyle().Width(hintsW).Render(statusStyle.Render(truncateRunes(footerHints, hintsW)))
	return lipgloss.JoinHorizontal(lipgloss.Bottom, left, " ", statusStyle.Render(Version))
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// playerView renders the bottom mini-player: state, title, progress bar,
// elapsed/total time and volume. Unknown durations show times without a bar.
func (m Model) playerView() string {
	glyph, word := "▶", "playing"
	if m.paused {
		glyph, word = "⏸", "paused"
	}
	el := m.elapsed
	if el < 0 {
		el = 0
	}
	if m.total > 0 && el > m.total {
		el = m.total
	}
	title := fmt.Sprintf("%s %s — %s", glyph, m.now.Title, m.now.Artist)
	times := fmt.Sprintf("%s / %s · %s · vol %d",
		source.FormatDuration(int(el)), source.FormatDuration(int(m.total)), word, m.volume)
	w := m.width
	if w <= 0 {
		w = 80
	}
	center := lipgloss.NewStyle().Width(w).Align(lipgloss.Center)
	if w < 50 || m.total <= 0 {
		return center.Render(titleStyle.Render(title)) + "\n" + center.Render(statusStyle.Render(times))
	}
	barW := w - len(times) - 6
	if barW > 40 {
		barW = 40
	}
	if barW < 10 {
		barW = 10
	}
	bar := progressBar(barW, el/m.total)
	return center.Render(titleStyle.Render(title)) + "\n" +
		center.Render(barStyle.Render(bar)+" "+statusStyle.Render(times))
}

// progressBar renders a frac-filled bar of the given cell width.
func progressBar(width int, frac float64) string {
	if width <= 1 {
		return "●"
	}
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	pos := int(frac * float64(width))
	if pos >= width {
		pos = width - 1
	}
	return strings.Repeat("━", pos) + "●" + strings.Repeat("─", width-pos-1)
}

// homeBanner is a large Adele wordmark for the homescreen. Terminals have
// no font sizes, so scale comes from block-letter art instead of styling.
const homeBanner = ` █████╗ ██████╗ ███████╗██╗     ███████╗
██╔══██╗██╔══██╗██╔════╝██║     ██╔════╝
███████║██║  ██║█████╗  ██║     █████╗
██╔══██║██║  ██║██╔══╝  ██║     ██╔══╝
██║  ██║██████╔╝███████╗███████╗███████╗
╚═╝  ╚═╝╚═════╝ ╚══════╝╚══════╝╚══════╝`

// homeView renders the idle screen: Adele banner just above the search box
// as one centered group, hints along the bottom.
func (m Model) homeView() string {
	w, h := m.width, m.height
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 24
	}
	banner := homeBanner
	if w < 50 {
		banner = "Adele"
	}

	boxW := 60
	if w-8 < boxW {
		boxW = w - 8
	}
	if boxW < 20 {
		boxW = 20
	}
	inp := m.input
	inp.Width = boxW - 4
	box := searchBox
	if m.input.Focused() {
		box = searchFocus
	}
	middle := box.Width(boxW).Render(inp.View())
	sub := homeSub.Render("YouTube cli player")
	body := lipgloss.JoinVertical(lipgloss.Center, homeTitle.Render(banner), sub, "", middle)
	if m.status != "" {
		body = lipgloss.JoinVertical(lipgloss.Center, body, statusStyle.Render(m.status))
	}
	if m.fatal != "" {
		body = lipgloss.JoinVertical(lipgloss.Center, body, errStyle.Render(m.fatal))
	}
	centerH := h - 2
	if centerH < 5 {
		centerH = 5
	}
	mid := lipgloss.Place(w, centerH, lipgloss.Center, lipgloss.Center, body)
	foot := lipgloss.Place(w, 2, lipgloss.Center, lipgloss.Bottom,
		statusStyle.Render("enter search · esc leave box · q quit"))
	return mid + "\n" + foot
}
