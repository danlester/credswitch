package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	cursorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
	enabledStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	disabledStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
)

type tuiModel struct {
	paths    Paths
	profiles []Profile
	cursor   int
	err      error
}

func runTUI(p Paths) error {
	orphans, err := loadOrphans(p)
	if err != nil {
		return err
	}
	if len(orphans) > 0 {
		return fmt.Errorf("orphan profiles in ~/.aws/ (run `credswitch list` to see them) — resolve before using the TUI")
	}
	st, err := loadState(p)
	if err != nil {
		return err
	}
	if len(st.Profiles) == 0 {
		fmt.Println("No profiles found in", p.MasterDir)
		fmt.Println("Run `credswitch init` first to bootstrap from ~/.aws/.")
		return nil
	}
	_, err = tea.NewProgram(tuiModel{paths: p, profiles: st.Profiles}).Run()
	return err
}

func (m tuiModel) Init() tea.Cmd { return nil }

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch keyMsg.String() {
	case "q", "ctrl+c", "esc":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.profiles)-1 {
			m.cursor++
		}
	case "g", "home":
		m.cursor = 0
	case "G", "end":
		m.cursor = len(m.profiles) - 1
	case " ", "enter", "x":
		return m.toggle()
	}
	return m, nil
}

func (m tuiModel) toggle() (tea.Model, tea.Cmd) {
	if len(m.profiles) == 0 {
		return m, nil
	}
	prof := m.profiles[m.cursor]
	if prof.Name == defaultProfile {
		m.err = fmt.Errorf("default profile is always enabled")
		return m, nil
	}
	var err error
	if prof.Enabled {
		err = disableProfile(m.paths, prof.Name)
	} else {
		err = enableProfile(m.paths, prof.Name)
	}
	if err != nil {
		m.err = err
		return m, nil
	}
	m.err = nil
	st, reloadErr := loadState(m.paths)
	if reloadErr != nil {
		m.err = reloadErr
		return m, nil
	}
	m.profiles = st.Profiles
	if m.cursor >= len(m.profiles) {
		m.cursor = len(m.profiles) - 1
	}
	return m, nil
}

func (m tuiModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("credswitch — AWS profile manager"))
	b.WriteString("\n\n")

	for i, p := range m.profiles {
		cursor := "  "
		if i == m.cursor {
			cursor = cursorStyle.Render("> ")
		}
		mark := "[ ]"
		nameStyle := disabledStyle
		if p.Enabled {
			mark = "[x]"
			nameStyle = enabledStyle
		}
		fmt.Fprintf(&b, "%s%s %s %s\n",
			cursor,
			mark,
			nameStyle.Render(fmt.Sprintf("%-30s", p.Name)),
			helpStyle.Render(locationLabel(p)),
		)
	}

	b.WriteString("\n")
	if m.err != nil {
		b.WriteString(errorStyle.Render("error: " + m.err.Error()))
		b.WriteString("\n")
	}
	b.WriteString(helpStyle.Render("up/down move · space toggle · q quit"))
	b.WriteString("\n")
	return b.String()
}
