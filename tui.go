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
	orphanStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("214")) // amber
	driftStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	ephemeralStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("13")) // magenta
)

type tuiModel struct {
	paths    Paths
	profiles []Profile
	drifted  map[string]bool
	cursor   int
	err      error
}

func runTUI(p Paths) error {
	st, err := loadState(p)
	if err != nil {
		return err
	}
	if len(st.Profiles) == 0 {
		fmt.Println("No profiles found in", p.MasterDir)
		fmt.Println("Run `credswitch init` first to bootstrap from ~/.aws/.")
		return nil
	}
	drift, err := loadDrift(p)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(tuiModel{
		paths:    p,
		profiles: st.Profiles,
		drifted:  driftedNames(drift),
	}).Run()
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
		return m.runOnCurrent(toggleAction)
	case "s":
		return m.runOnCurrent(syncAction)
	case "r":
		return m.runOnCurrent(revertAction)
	case "e":
		return m.runOnCurrent(ephemeralAction)
	}
	return m, nil
}

type tuiAction int

const (
	toggleAction tuiAction = iota
	syncAction
	revertAction
	ephemeralAction
)

func (m tuiModel) runOnCurrent(action tuiAction) (tea.Model, tea.Cmd) {
	if len(m.profiles) == 0 {
		return m, nil
	}
	prof := m.profiles[m.cursor]
	var err error
	switch action {
	case toggleAction:
		if prof.Name == defaultProfile {
			m.err = fmt.Errorf("default profile is always enabled")
			return m, nil
		}
		if prof.Enabled {
			err = disableProfile(m.paths, prof.Name)
		} else {
			err = enableProfile(m.paths, prof.Name)
		}
	case syncAction:
		err = syncToMaster(m.paths, prof.Name)
	case revertAction:
		err = revertProfile(m.paths, prof.Name)
	case ephemeralAction:
		_, err = toggleEphemeral(m.paths, prof.Name)
	}
	if err != nil {
		m.err = err
		return m, nil
	}
	m.err = nil
	return m.reload()
}

func (m tuiModel) reload() (tea.Model, tea.Cmd) {
	st, err := loadState(m.paths)
	if err != nil {
		m.err = err
		return m, nil
	}
	m.profiles = st.Profiles
	drift, err := loadDrift(m.paths)
	if err != nil {
		m.err = err
		return m, nil
	}
	m.drifted = driftedNames(drift)
	if m.cursor >= len(m.profiles) {
		m.cursor = len(m.profiles) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
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
		annot := ""
		switch {
		case p.Orphan:
			annot = "  " + orphanStyle.Render("ORPHAN")
		case m.drifted[p.Name]:
			annot = "  " + driftStyle.Render("DRIFTED")
		}
		if p.Ephemeral {
			annot += "  " + ephemeralStyle.Render("EPHEMERAL")
		}
		fmt.Fprintf(&b, "%s%s %s %s%s\n",
			cursor,
			mark,
			nameStyle.Render(fmt.Sprintf("%-30s", p.Name)),
			helpStyle.Render(locationLabel(p)),
			annot,
		)
	}

	b.WriteString("\n")
	if m.err != nil {
		b.WriteString(errorStyle.Render("error: " + m.err.Error()))
		b.WriteString("\n")
	}
	b.WriteString(helpStyle.Render("up/down move · space toggle · e ephemeral · s sync · r revert · q quit"))
	b.WriteString("\n")
	return b.String()
}
