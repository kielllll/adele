package ui

import (
	"testing"

	"adele/internal/source"

	tea "github.com/charmbracelet/bubbletea"
)

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
