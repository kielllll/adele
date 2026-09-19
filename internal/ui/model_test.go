package ui

import (
	"regexp"
	"strings"
	"testing"

	"adele/internal/source"

	tea "github.com/charmbracelet/bubbletea"
)

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

func runeKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func testModel() Model {
	return New(&source.Client{YtdlpPath: "yt-dlp"}, nil, "mpv missing (test)")
}

// step feeds one key press through Update and returns the concrete model.
func step(t *testing.T, m Model, msg tea.KeyMsg) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(msg)
	pm, ok := updated.(*Model)
	if !ok {
		t.Fatalf("Update returned %T, want *Model", updated)
	}
	return *pm, cmd
}

// Focused search must accept every printable key as text, including
// letters that double as blurred-state hotkeys (j, n, s, q) and space.
func TestTypingWhileFocused(t *testing.T) {
	m := testModel()
	if !m.input.Focused() {
		t.Fatal("search box should start focused")
	}
	var cmd tea.Cmd
	for _, r := range []rune{'e', 'j', 'n', 's', 'q', ' '} {
		m, cmd = step(t, m, runeKey(r))
		_ = cmd
	}
	if got := m.input.Value(); got != "ejnsq " {
		t.Fatalf("input = %q, want %q", got, "ejnsq ")
	}
	if !m.input.Focused() {
		t.Fatal("typing must not blur the search box")
	}
}

// Typing while blurred must refocus search and insert the character
// instead of dropping it into the results list.
func TestTypingWhileBlurredRefocuses(t *testing.T) {
	m := testModel()
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.input.Focused() {
		t.Fatal("esc should blur the search box")
	}
	m, _ = step(t, m, runeKey('e'))
	if !m.input.Focused() {
		t.Fatal("typing 'e' should refocus the search box")
	}
	if got := m.input.Value(); got != "e" {
		t.Fatalf("input = %q, want %q", got, "e")
	}
}

// Blurred-state hotkeys must keep working: space does not refocus,
// q quits.
func TestBlurredHotkeys(t *testing.T) {
	m := testModel()
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyEsc})

	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if m.input.Focused() {
		t.Fatal("space while blurred must not refocus search")
	}

	_, cmd := m.Update(runeKey('q'))
	if cmd == nil {
		t.Fatal("expected a quit command for 'q'")
	}
}

// Idle homescreen (no results, nothing playing) shows the large Adele
// block-letter banner and centered search instead of the results list.
func TestHomeViewIdle(t *testing.T) {
	m := testModel()
	v := m.View()
	if !strings.Contains(v, "██") {
		t.Fatalf("home view missing large Adele banner:\n%s", v)
	}
	if !strings.Contains(v, "Search") {
		t.Fatalf("home view missing search hint:\n%s", v)
	}
	if strings.Contains(v, "Results") {
		t.Fatalf("home view should not show results list:\n%s", v)
	}
}

// Once results arrive the browser view replaces the homescreen.
func TestBrowserViewAfterResults(t *testing.T) {
	m := testModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, ok := updated.(Model)
	if !ok {
		if pm, ok := updated.(*Model); ok {
			m = *pm
		} else {
			t.Fatalf("Update returned %T, want Model", updated)
		}
	}
	updated, _ = m.Update(searchDoneMsg{tracks: []source.Track{{Title: "Hello", Artist: "Adele"}}})
	var v string
	switch u := updated.(type) {
	case Model:
		v = u.View()
	case *Model:
		v = u.View()
	default:
		t.Fatalf("Update returned %T, want Model", updated)
	}
	if !strings.Contains(v, "Hello") {
		t.Fatalf("browser view missing result track:\n%s", v)
	}
}

// A successful position poll updates elapsed/total and the panel shows
// elapsed/total plus a progress marker.
func TestPosMsgUpdatesPanel(t *testing.T) {
	m := testModel()
	m.now = &source.Track{Title: "Hello", Artist: "Adele", Duration: 225}
	m.elapsed, m.total = 0, 225
	updated, _ := m.Update(posMsg{pos: 65, dur: 225})
	var v string
	switch u := updated.(type) {
	case Model:
		v = u.View()
	case *Model:
		v = u.View()
	default:
		t.Fatalf("Update returned %T, want Model", updated)
	}
	if !strings.Contains(v, "1:05 / 3:45") {
		t.Fatalf("panel missing elapsed/total:\n%s", v)
	}
	if !strings.Contains(v, "●") {
		t.Fatalf("panel missing progress bar:\n%s", v)
	}
}

// Unknown durations show --:-- with no fabricated progress bar.
func TestPanelUnknownDuration(t *testing.T) {
	m := testModel()
	m.now = &source.Track{Title: "Hello", Artist: "Adele"}
	m.elapsed, m.total = 0, 0
	v := m.View()
	if !strings.Contains(v, "--:--") {
		t.Fatalf("panel should show --:-- for unknown duration:\n%s", v)
	}
	if strings.Contains(v, "●") {
		t.Fatalf("panel must not show progress without a total:\n%s", v)
	}
}

// Stale ticks from an earlier playback must not schedule new work.
func TestStaleTickIgnored(t *testing.T) {
	m := testModel()
	m.now = &source.Track{Title: "Hello", Artist: "Adele"}
	m.gen = 2
	_, cmd := m.Update(tickMsg{gen: 1})
	if cmd != nil {
		t.Fatal("stale tick must not schedule follow-up commands")
	}
}

// The footer shows hints with the version pinned to the right edge.
func TestFooterHintsAndVersion(t *testing.T) {
	f := stripANSI(footerView(80))
	if !strings.Contains(f, "enter play") {
		t.Fatalf("footer missing hints:\n%s", f)
	}
	if !strings.HasSuffix(f, Version) {
		t.Fatalf("footer must end with version %q:\n%s", Version, f)
	}
	if got := len([]rune(f)); got != 80 {
		t.Fatalf("footer width = %d, want 80:\n%s", got, f)
	}
}

// The footer stays on one line with the version intact on narrow screens.
func TestFooterNarrow(t *testing.T) {
	f := stripANSI(footerView(30))
	if strings.Contains(f, "\n") {
		t.Fatalf("footer must not wrap:\n%s", f)
	}
	if !strings.HasSuffix(f, Version) {
		t.Fatalf("narrow footer must keep version %q:\n%s", Version, f)
	}
}

// The browser footer sits on the bottom row of the terminal.
func TestFooterPinnedToBottom(t *testing.T) {
	m := testModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, ok := updated.(Model)
	if !ok {
		if pm, ok := updated.(*Model); ok {
			m = *pm
		} else {
			t.Fatalf("Update returned %T, want Model", updated)
		}
	}
	updated, _ = m.Update(searchDoneMsg{tracks: []source.Track{{Title: "Hello", Artist: "Adele"}}})
	var v string
	switch u := updated.(type) {
	case Model:
		v = u.View()
	case *Model:
		v = u.View()
	default:
		t.Fatalf("Update returned %T, want Model", updated)
	}
	lines := strings.Split(stripANSI(v), "\n")
	if len(lines) != 24 {
		t.Fatalf("view height = %d lines, want 24", len(lines))
	}
	if last := lines[len(lines)-1]; !strings.HasSuffix(last, Version) {
		t.Fatalf("last row must be the footer ending in %q, got %q", Version, last)
	}
}

// The homescreen hints sit on the bottom row of the terminal.
func TestHomeFooterPinnedToBottom(t *testing.T) {
	m := testModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	var v string
	switch u := updated.(type) {
	case Model:
		v = u.View()
	case *Model:
		v = u.View()
	default:
		t.Fatalf("Update returned %T, want Model", updated)
	}
	lines := strings.Split(stripANSI(v), "\n")
	if len(lines) != 24 {
		t.Fatalf("home height = %d lines, want 24", len(lines))
	}
	if last := lines[len(lines)-1]; !strings.Contains(last, "q quit") {
		t.Fatalf("last home row must be the hints, got %q", last)
	}
}

// The play state appears exactly once (in the panel), never duplicated as
// a standalone status line.
func TestNoDuplicatePlayState(t *testing.T) {
	for _, paused := range []bool{false, true} {
		m := testModel()
		m.now = &source.Track{Title: "Hello", Artist: "Adele", Duration: 225}
		m.elapsed, m.total = 65, 225
		m.paused = paused
		m.status = "1 results — enter play · space pause"
		v := m.View()
		if got := strings.Count(v, "playing"); got != boolToInt(!paused) {
			t.Fatalf("paused=%v: %q occurs %d times, want %d:\n%s", paused, "playing", got, boolToInt(!paused), v)
		}
		if got := strings.Count(v, "paused"); got != boolToInt(paused) {
			t.Fatalf("paused=%v: %q occurs %d times, want %d:\n%s", paused, "paused", got, boolToInt(paused), v)
		}
	}
}

// The list's j/k/?-more help is hidden; the results count sits on that row.
func TestListHelpHidden(t *testing.T) {
	m := testModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, ok := updated.(Model)
	if !ok {
		if pm, ok := updated.(*Model); ok {
			m = *pm
		} else {
			t.Fatalf("Update returned %T, want Model", updated)
		}
	}
	updated, _ = m.Update(searchDoneMsg{tracks: []source.Track{{Title: "Hello", Artist: "Adele"}}})
	var v string
	switch u := updated.(type) {
	case Model:
		v = u.View()
	case *Model:
		v = u.View()
	default:
		t.Fatalf("Update returned %T, want Model", updated)
	}
	for _, hint := range []string{"? more", "↓/j", "↑/k"} {
		if strings.Contains(v, hint) {
			t.Fatalf("list help %q should be hidden:\n%s", hint, v)
		}
	}
	if !strings.Contains(v, "1 results") {
		t.Fatalf("results count missing:\n%s", v)
	}
}

// The results/status text renders above the realtime player panel.
func TestStatusAbovePlayer(t *testing.T) {
	m := testModel()
	m.now = &source.Track{Title: "Hello", Artist: "Adele", Duration: 225}
	m.elapsed, m.total = 65, 225
	m.status = "1 results — enter play · space pause"
	v := m.View()
	si, pi := strings.Index(v, "1 results"), strings.Index(v, "1:05 / 3:45")
	if si < 0 || pi < 0 {
		t.Fatalf("missing status or panel times:\n%s", v)
	}
	if si > pi {
		t.Fatalf("status must render above the player panel:\n%s", v)
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// The player panel is centered, not left-aligned.
func TestPlayerViewCentered(t *testing.T) {
	m := testModel()
	m.width = 80
	m.now = &source.Track{Title: "Hello", Artist: "Adele", Duration: 225}
	m.elapsed, m.total = 65, 225
	first := stripANSI(strings.SplitN(m.playerView(), "\n", 2)[0])
	if !strings.HasPrefix(first, " ") {
		t.Fatalf("panel title should be centered:\n%q", first)
	}
}
