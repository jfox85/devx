package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jfox85/devx/caddy"
	"github.com/jfox85/devx/config"
	"github.com/jfox85/devx/session"
)

type sessionItem struct {
	name            string
	projectAlias    string
	projectName     string
	branch          string
	path            string
	ports           map[string]int
	attentionFlag   bool
	attentionReason string
	attentionTime   time.Time
}

type projectItem struct {
	alias       string
	name        string
	path        string
	description string
}

type state int

const (
	stateList state = iota
	stateCreating
	stateProjectSelect
	stateConfirm
	stateHostnames
	stateProjectManagement
	stateProjectAdd
)

type model struct {
	sessions        []sessionItem
	cursor          int
	state           state
	help            help.Model
	keys            keyMap
	textInput       textinput.Model
	confirmMsg      string
	confirmFunc     func()
	deleteTarget    string
	width           int
	height          int
	err             error
	statusMsg       string
	showPreview     bool
	hostnames       []string
	hostnameCursor  int
	projects        []projectItem
	projectCursor   int
	selectedProject string
	caddyWarning    string
}

type keyMap struct {
	Up        key.Binding
	Down      key.Binding
	Enter     key.Binding
	Create    key.Binding
	Delete    key.Binding
	Open      key.Binding
	Edit      key.Binding
	Hostnames key.Binding
	Projects  key.Binding
	Preview   key.Binding
	Quit      key.Binding
	Help      key.Binding
	Back      key.Binding
}

var keys = keyMap{
	Up: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("↑/k", "up"),
	),
	Down: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("↓/j", "down"),
	),
	Enter: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "attach session"),
	),
	Create: key.NewBinding(
		key.WithKeys("c", "n"),
		key.WithHelp("c/n", "create new"),
	),
	Delete: key.NewBinding(
		key.WithKeys("d", "x"),
		key.WithHelp("d/x", "delete"),
	),
	Open: key.NewBinding(
		key.WithKeys("o"),
		key.WithHelp("o", "open routes"),
	),
	Edit: key.NewBinding(
		key.WithKeys("e"),
		key.WithHelp("e", "edit in editor"),
	),
	Hostnames: key.NewBinding(
		key.WithKeys("h"),
		key.WithHelp("h", "view hostnames"),
	),
	Projects: key.NewBinding(
		key.WithKeys("P"),
		key.WithHelp("P", "manage projects"),
	),
	Preview: key.NewBinding(
		key.WithKeys("p"),
		key.WithHelp("p", "toggle preview"),
	),
	Quit: key.NewBinding(
		key.WithKeys("q", "ctrl+c"),
		key.WithHelp("q", "quit"),
	),
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "toggle help"),
	),
	Back: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "back"),
	),
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Help, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Enter},
		{k.Create, k.Delete, k.Open},
		{k.Preview, k.Help, k.Quit},
	}
}

func InitialModel() model {
	ti := textinput.New()
	ti.Placeholder = "session-name"
	ti.CharLimit = 50

	return model{
		sessions:    []sessionItem{},
		state:       stateList,
		help:        help.New(),
		keys:        keys,
		textInput:   ti,
		showPreview: true, // Enable preview by default
		width:       80,   // Default width
		height:      24,   // Default height
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.loadSessions, m.refreshPreview(), m.refreshSessions(), m.checkCaddyHealth())
}

func (m *model) loadSessions() tea.Msg {
	store, err := session.LoadSessions()
	if err != nil {
		return errMsg{err}
	}

	// Load project registry to get project names
	registry, err := config.LoadProjectRegistry()
	if err != nil {
		return errMsg{err}
	}

	sessions := make([]sessionItem, 0, len(store.Sessions))
	for name, sess := range store.Sessions {
		projectName := sess.ProjectAlias // Default to alias
		if sess.ProjectAlias != "" {
			if project, err := registry.GetProject(sess.ProjectAlias); err == nil {
				projectName = project.Name
			}
		}

		sessions = append(sessions, sessionItem{
			name:            name,
			projectAlias:    sess.ProjectAlias,
			projectName:     projectName,
			branch:          sess.Branch,
			path:            sess.Path,
			ports:           sess.Ports,
			attentionFlag:   sess.AttentionFlag,
			attentionReason: sess.AttentionReason,
			attentionTime:   sess.AttentionTime,
		})
	}

	// Sort sessions: by project first, then flagged ones first, then by name
	sort.Slice(sessions, func(i, j int) bool {
		// First, group by project (sessions without project go last)
		if sessions[i].projectAlias != sessions[j].projectAlias {
			if sessions[i].projectAlias == "" {
				return false // No project goes to end
			}
			if sessions[j].projectAlias == "" {
				return true // No project goes to end
			}
			return sessions[i].projectAlias < sessions[j].projectAlias
		}

		// Within same project, prioritize flagged sessions
		if sessions[i].attentionFlag && !sessions[j].attentionFlag {
			return true
		}
		if !sessions[i].attentionFlag && sessions[j].attentionFlag {
			return false
		}

		// Both have same flag status, sort by name
		return sessions[i].name < sessions[j].name
	})

	return sessionsLoadedMsg{sessions}
}

func (m *model) loadProjects() tea.Msg {
	registry, err := config.LoadProjectRegistry()
	if err != nil {
		return errMsg{err}
	}

	projects := make([]projectItem, 0, len(registry.Projects))
	for alias, proj := range registry.Projects {
		projects = append(projects, projectItem{
			alias:       alias,
			name:        proj.Name,
			path:        proj.Path,
			description: proj.Description,
		})
	}

	// Sort projects by alias
	sort.Slice(projects, func(i, j int) bool {
		return projects[i].alias < projects[j].alias
	})

	return projectsLoadedMsg{projects}
}

type sessionsLoadedMsg struct {
	sessions []sessionItem
}

type projectsLoadedMsg struct {
	projects []projectItem
}

type sessionCreationStartedMsg struct{}

type projectSelectionNeededMsg struct{}

type projectAddedMsg struct {
	name       string
	alias      string
	path       string
	configNote string
}

type hostnamesLoadedMsg struct {
	hostnames []string
}

type errMsg struct{ err error }

type sessionCreatedMsg struct {
	sessionName string
}
type sessionDeletedMsg struct{}
type attachToNewSessionMsg struct {
	sessionName string
}
type refreshPreviewMsg struct{}
type refreshSessionsMsg struct{}
type caddyHealthMsg struct {
	warning string
}

func (e errMsg) Error() string { return e.err.Error() }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.help.Width = msg.Width

	case tea.KeyMsg:
		// Handle error state - allow user to clear error and return to list
		if m.err != nil {
			switch {
			case key.Matches(msg, m.keys.Quit):
				return m, tea.Quit
			default:
				// Any other key clears the error and returns to list
				m.err = nil
				m.state = stateList
			}
			return m, nil
		}

		switch m.state {
		case stateList:
			switch {
			case key.Matches(msg, m.keys.Quit):
				return m, tea.Quit

			case key.Matches(msg, m.keys.Up):
				if m.cursor > 0 {
					m.cursor--
				}

			case key.Matches(msg, m.keys.Down):
				if m.cursor < len(m.sessions)-1 {
					m.cursor++
				}

			case key.Matches(msg, m.keys.Enter):
				if len(m.sessions) > 0 {
					selected := m.sessions[m.cursor]
					return m, m.attachSession(selected.name)
				}

			case key.Matches(msg, m.keys.Create):
				return m, m.startSessionCreation()

			case key.Matches(msg, m.keys.Delete):
				if len(m.sessions) > 0 {
					selected := m.sessions[m.cursor]
					m.confirmMsg = fmt.Sprintf("Delete session '%s'? (y/n)", selected.name)
					m.deleteTarget = selected.name
					m.state = stateConfirm
				}

			case key.Matches(msg, m.keys.Open):
				if len(m.sessions) > 0 {
					selected := m.sessions[m.cursor]
					return m, m.openRoutes(selected.name)
				}

			case key.Matches(msg, m.keys.Edit):
				if len(m.sessions) > 0 {
					selected := m.sessions[m.cursor]
					return m, m.editInEditor(selected.name)
				}

			case key.Matches(msg, m.keys.Hostnames):
				// Pass the selected session if one is selected
				var selectedSession string
				if len(m.sessions) > 0 && m.cursor < len(m.sessions) {
					selectedSession = m.sessions[m.cursor].name
				}
				return m, m.loadHostnames(selectedSession)

			case key.Matches(msg, m.keys.Projects):
				m.state = stateProjectManagement
				return m, m.loadProjectsForManagement()

			case key.Matches(msg, m.keys.Preview):
				m.showPreview = !m.showPreview

			case key.Matches(msg, m.keys.Help):
				m.help.ShowAll = !m.help.ShowAll
			}

		case stateCreating:
			switch {
			case key.Matches(msg, m.keys.Back):
				m.state = stateList
				m.textInput.Blur()

			case msg.Type == tea.KeyEnter:
				name := strings.TrimSpace(m.textInput.Value())
				if name != "" {
					m.state = stateList
					m.textInput.Blur()
					return m, m.createSession(name)
				}

			default:
				var cmd tea.Cmd
				m.textInput, cmd = m.textInput.Update(msg)
				return m, cmd
			}

		case stateConfirm:
			switch strings.ToLower(msg.String()) {
			case "y":
				// Handle session deletion
				if m.deleteTarget != "" {
					target := m.deleteTarget
					m.state = stateList
					m.confirmMsg = ""
					m.deleteTarget = ""
					return m, m.deleteSession(target)
				}
				// Handle other confirmations (project deletion)
				if m.confirmFunc != nil {
					m.confirmFunc()
					m.confirmFunc = nil
					m.confirmMsg = ""
					// If we're in project management, reload projects
					if m.state == stateProjectManagement {
						return m, m.loadProjectsForManagement()
					}
					return m, nil
				}
			case "n":
				// Return to previous state based on context
				if m.deleteTarget != "" {
					m.state = stateList
					m.deleteTarget = ""
				} else if m.state == stateProjectManagement {
					// Stay in project management
				} else {
					m.state = stateList
				}
				m.confirmMsg = ""
				m.confirmFunc = nil
			}

		case stateHostnames:
			switch {
			case key.Matches(msg, m.keys.Back), key.Matches(msg, m.keys.Quit):
				m.state = stateList
				m.hostnames = nil
				m.hostnameCursor = 0

			case key.Matches(msg, m.keys.Up):
				if m.hostnameCursor > 0 {
					m.hostnameCursor--
				}

			case key.Matches(msg, m.keys.Down):
				if m.hostnameCursor < len(m.hostnames)-1 {
					m.hostnameCursor++
				}

			case key.Matches(msg, m.keys.Enter):
				if len(m.hostnames) > 0 {
					hostname := m.hostnames[m.hostnameCursor]
					m.state = stateList
					return m, m.openHostname(hostname)
				}
			}

		case stateProjectSelect:
			switch {
			case key.Matches(msg, m.keys.Back), key.Matches(msg, m.keys.Quit):
				m.state = stateList
				m.projects = nil
				m.projectCursor = 0

			case key.Matches(msg, m.keys.Up):
				if m.projectCursor > 0 {
					m.projectCursor--
				}

			case key.Matches(msg, m.keys.Down):
				if m.projectCursor < len(m.projects)-1 {
					m.projectCursor++
				}

			case key.Matches(msg, m.keys.Enter):
				if len(m.projects) > 0 {
					m.selectedProject = m.projects[m.projectCursor].alias
					m.state = stateCreating
					m.textInput.Reset()
					m.textInput.Focus()
					return m, textinput.Blink
				}
			}

		case stateProjectManagement:
			switch {
			case key.Matches(msg, m.keys.Back), key.Matches(msg, m.keys.Quit):
				m.state = stateList
				m.projects = nil
				m.projectCursor = 0
				m.statusMsg = "" // Clear status message

			case key.Matches(msg, m.keys.Up):
				if m.projectCursor > 0 {
					m.projectCursor--
				}

			case key.Matches(msg, m.keys.Down):
				if m.projectCursor < len(m.projects)-1 {
					m.projectCursor++
				}

			case key.Matches(msg, m.keys.Create):
				m.state = stateProjectAdd
				m.textInput.Reset()
				m.textInput.Focus()
				return m, textinput.Blink

			case key.Matches(msg, m.keys.Delete):
				if len(m.projects) > 0 {
					project := m.projects[m.projectCursor]
					m.confirmMsg = fmt.Sprintf("Remove project '%s'?", project.name)
					m.confirmFunc = func() {
						// Remove project
						registry, _ := config.LoadProjectRegistry()
						registry.RemoveProject(project.alias)
						// Return to project management
						m.state = stateProjectManagement
					}
					m.state = stateConfirm
					return m, nil
				}
			}

		case stateProjectAdd:
			switch {
			case key.Matches(msg, m.keys.Back):
				m.state = stateProjectManagement
				return m, m.loadProjectsForManagement()

			case key.Matches(msg, m.keys.Enter):
				path := m.textInput.Value()
				if path != "" {
					return m, m.addProject(path)
				}

			default:
				var cmd tea.Cmd
				m.textInput, cmd = m.textInput.Update(msg)
				return m, cmd
			}
		}

	case sessionsLoadedMsg:
		m.sessions = msg.sessions
		if m.cursor >= len(m.sessions) {
			m.cursor = len(m.sessions) - 1
		}
		if m.cursor < 0 {
			m.cursor = 0
		}

	case hostnamesLoadedMsg:
		m.hostnames = msg.hostnames
		m.hostnameCursor = 0
		m.state = stateHostnames

	case projectsLoadedMsg:
		m.projects = msg.projects
		m.projectCursor = 0
		// Don't change state here - let the caller decide the state

	case sessionCreationStartedMsg:
		m.state = stateCreating
		m.textInput.Reset()
		m.textInput.Focus()
		return m, textinput.Blink

	case projectSelectionNeededMsg:
		m.state = stateProjectSelect
		return m, m.loadProjects

	case projectAddedMsg:
		// Show success message briefly, then return to project management
		m.err = nil // Clear any previous errors
		m.state = stateProjectManagement

		// Create a success message
		m.statusMsg = fmt.Sprintf("✓ Added project '%s' (%s) at %s%s",
			msg.name, msg.alias, msg.path, msg.configNote)

		// Reload the project list
		return m, m.loadProjectsForManagement()

	case sessionCreatedMsg:
		// Reload sessions first, then attach to the newly created session
		return m, tea.Batch(
			m.loadSessions,
			func() tea.Msg {
				// Small delay to ensure sessions are loaded
				time.Sleep(100 * time.Millisecond)
				return attachToNewSessionMsg{sessionName: msg.sessionName}
			},
		)

	case sessionDeletedMsg:
		return m, m.loadSessions

	case attachToNewSessionMsg:
		// Attach to the newly created session
		return m, m.attachSession(msg.sessionName)

	case refreshPreviewMsg:
		// Only refresh if we're in preview mode
		if m.showPreview && m.state == stateList {
			return m, m.refreshPreview()
		}

	case refreshSessionsMsg:
		// Reload sessions to reflect changes and continue periodic refresh
		return m, tea.Batch(m.loadSessions, m.refreshSessions())

	case caddyHealthMsg:
		m.caddyWarning = msg.warning

	case errMsg:
		m.err = msg.err
		m.state = stateList
	}

	return m, nil
}

func (m model) View() string {
	if m.err != nil {
		return fmt.Sprintf("\n  Error: %v\n\n  Press any key to continue or 'q' to quit.\n", m.err)
	}

	var content string

	switch m.state {
	case stateList:
		content = m.listView()
	case stateCreating:
		content = m.createView()
	case stateProjectSelect:
		content = m.projectSelectView()
	case stateConfirm:
		content = m.confirmView()
	case stateHostnames:
		content = m.hostnamesView()
	case stateProjectManagement:
		content = m.projectManagementView()
	case stateProjectAdd:
		content = m.projectAddView()
	}

	// Create footer with commands
	var footer string
	if m.help.ShowAll {
		footer = m.help.View(m.keys)
	} else {
		switch m.state {
		case stateList:
			footer = footerStyle.Width(m.width).Render("↑/↓: navigate • enter: attach • c: create • d: delete • o: open routes • e: edit • h: hostnames • P: projects • p: preview • ?: help • q: quit")
		case stateCreating:
			footer = footerStyle.Width(m.width).Render("enter: create session • esc: cancel")
		case stateProjectSelect:
			footer = footerStyle.Width(m.width).Render("↑/↓: navigate • enter: select project • esc: back • q: quit")
		case stateConfirm:
			footer = footerStyle.Width(m.width).Render("y: confirm • n: cancel")
		case stateHostnames:
			footer = footerStyle.Width(m.width).Render("↑/↓: navigate • enter: open in browser • esc: back • q: quit")
		case stateProjectManagement:
			footer = footerStyle.Width(m.width).Render("↑/↓: navigate • c: add project • d: remove project • esc: back • q: quit")
		case stateProjectAdd:
			footer = footerStyle.Width(m.width).Render("enter: add project • esc: cancel")
		}
	}

	// Calculate available height for content
	// Account for footer height and some padding
	footerHeight := lipgloss.Height(footer)
	availableHeight := m.height - footerHeight - 1

	// Make sure we have a reasonable minimum height
	if availableHeight < 10 {
		availableHeight = 10
	}

	// Apply height constraint to prevent overflow
	return lipgloss.NewStyle().
		Height(availableHeight).
		MaxHeight(availableHeight).
		Render(content) + "\n" + footer
}

func (m model) listView() string {
	logo := logoStyle.Width(m.width).Render(`
  ____            __  __
 |  _ \  _____   _\ \/ /
 | | | |/ _ \ \ / /\  / 
 | |_| |  __/\ V / /  \ 
 |____/ \___| \_/ /_/\_\
                        `)

	if len(m.sessions) == 0 {
		return logo + "\n" + headerStyle.Render("devx Sessions") + "\n\n" +
			"  No sessions found.\n\n" +
			"  Press 'c' to create a new session.\n"
	}

	if !m.showPreview {
		// Original full-width layout
		var b strings.Builder
		b.WriteString(logo + "\n" + headerStyle.Render("devx Sessions") + "\n\n")

		// Show Caddy warning if present
		if m.caddyWarning != "" {
			b.WriteString(warningStyle.Render(m.caddyWarning) + "\n\n")
		}

		// Group sessions by project for display
		var currentProject string
		for i, sess := range m.sessions {
			// Add project header if this is a new project
			if sess.projectAlias != currentProject {
				if currentProject != "" {
					b.WriteString("\n") // Add spacing between projects
				}
				currentProject = sess.projectAlias

				projectHeader := "No Project"
				if sess.projectAlias != "" {
					if sess.projectName != "" {
						projectHeader = fmt.Sprintf("%s (%s)", sess.projectName, sess.projectAlias)
					} else {
						projectHeader = sess.projectAlias
					}
				}

				b.WriteString(headerStyle.Render(projectHeader) + "\n")
			}

			cursor := "  "
			if m.cursor == i {
				cursor = "> "
			}

			// Add attention indicator
			indicator := " "
			if sess.attentionFlag {
				indicator = "🔔"
			}

			line := fmt.Sprintf("%s%s %s", cursor, indicator, sess.name)
			if m.cursor == i {
				line = selectedStyle.Render(line)
			}
			b.WriteString(line + "\n")

			// Show details for selected session (inline)
			if m.cursor == i {
				details := m.getSessionDetails(sess)
				b.WriteString(dimStyle.Render(details))
			}
		}

		return b.String()
	}

	// Preview layout: sessions on left, preview on right
	// Calculate optimal width for session list based on content
	listWidth := m.calculateOptimalListWidth()
	previewWidth := m.width - listWidth - 4 // Account for borders and margins

	// Build session list
	var sessionList strings.Builder
	sessionList.WriteString(headerStyle.Render("Sessions") + "\n\n")

	// Group sessions by project for display
	var currentProject string
	for i, sess := range m.sessions {
		// Add project header if this is a new project
		if sess.projectAlias != currentProject {
			if currentProject != "" {
				sessionList.WriteString("\n") // Add spacing between projects
			}
			currentProject = sess.projectAlias

			projectHeader := "No Project"
			if sess.projectAlias != "" {
				if sess.projectName != "" {
					projectHeader = fmt.Sprintf("%s (%s)", sess.projectName, sess.projectAlias)
				} else {
					projectHeader = sess.projectAlias
				}
			}

			sessionList.WriteString(headerStyle.Render(projectHeader) + "\n")
		}

		cursor := "  "
		if m.cursor == i {
			cursor = "> "
		}

		// Add attention indicator
		indicator := " "
		if sess.attentionFlag {
			indicator = "🔔"
		}

		line := fmt.Sprintf("%s%s %s", cursor, indicator, sess.name)
		if m.cursor == i {
			line = selectedStyle.Render(line)
		}
		sessionList.WriteString(line + "\n")
	}

	// Build preview pane
	var preview string
	if len(m.sessions) > 0 && m.cursor < len(m.sessions) {
		selected := m.sessions[m.cursor]
		preview = m.getSessionPreview(selected)
	} else {
		preview = dimStyle.Render("No session selected")
	}

	// Style the panes
	leftPane := sessionListStyle.Width(listWidth).Height(m.height - 6).Render(sessionList.String())
	rightPane := previewStyle.Width(previewWidth).Height(m.height - 6).Render(preview)

	// Join them horizontally with the logo on top
	return logo + "\n" + lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)
}

func (m model) getSessionDetails(sess sessionItem) string {
	details := fmt.Sprintf("    Branch: %s\n    Path: %s\n",
		sess.branch,
		sess.path)

	if len(sess.ports) > 0 {
		details += "    Ports:"
		// Sort ports alphabetically
		var services []string
		for service := range sess.ports {
			services = append(services, service)
		}
		sort.Strings(services)
		for _, service := range services {
			details += fmt.Sprintf(" %s:%d", service, sess.ports[service])
		}
		details += "\n"
	}

	// Show Caddy routes
	if sessionStore, err := session.LoadSessions(); err == nil {
		if sessionData, exists := sessionStore.Sessions[sess.name]; exists && len(sessionData.Routes) > 0 {
			details += "    Routes:\n"
			// Sort routes alphabetically
			var routeServices []string
			for service := range sessionData.Routes {
				routeServices = append(routeServices, service)
			}
			sort.Strings(routeServices)
			for _, service := range routeServices {
				url := fmt.Sprintf("http://%s.localhost", sessionData.Routes[service])
				details += fmt.Sprintf("      %s: %s\n", service, url)
			}
		}
	}

	return details
}

func (m model) getSessionPreview(sess sessionItem) string {
	var preview strings.Builder

	preview.WriteString(headerStyle.Render(sess.name) + "\n")

	// Show attention reason at the top if flagged
	if sess.attentionFlag {
		attentionStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")). // Orange
			Bold(true)

		reasonText := fmt.Sprintf("🔔 ATTENTION: %s", sess.attentionReason)
		if !sess.attentionTime.IsZero() {
			reasonText += fmt.Sprintf(" (%s ago)", time.Since(sess.attentionTime).Round(time.Minute))
		}

		preview.WriteString(attentionStyle.Render(reasonText) + "\n\n")
	}

	// Check if tmux session exists and capture its content
	if tmuxContent := m.getTmuxSessionContent(sess.name); tmuxContent != "" {
		preview.WriteString(dimStyle.Render("Live tmux session:") + "\n\n")
		preview.WriteString(tmuxContent)
	} else {
		// Fallback to session details if tmux session isn't running
		preview.WriteString(dimStyle.Render("Session not running") + "\n\n")

		preview.WriteString(fmt.Sprintf("Branch: %s\n", sess.branch))
		preview.WriteString(fmt.Sprintf("Path: %s\n\n", sess.path))

		if len(sess.ports) > 0 {
			preview.WriteString("Ports:\n")
			// Sort ports alphabetically
			var services []string
			for service := range sess.ports {
				services = append(services, service)
			}
			sort.Strings(services)
			for _, service := range services {
				preview.WriteString(fmt.Sprintf("  %s: %d\n", service, sess.ports[service]))
			}
			preview.WriteString("\n")
		}

		// Show Caddy routes
		if sessionStore, err := session.LoadSessions(); err == nil {
			if sessionData, exists := sessionStore.Sessions[sess.name]; exists && len(sessionData.Routes) > 0 {
				preview.WriteString("Routes:\n")
				// Sort routes alphabetically
				var routeServices []string
				for service := range sessionData.Routes {
					routeServices = append(routeServices, service)
				}
				sort.Strings(routeServices)
				for _, service := range routeServices {
					url := fmt.Sprintf("http://%s.localhost", sessionData.Routes[service])
					preview.WriteString(fmt.Sprintf("  %s: %s\n", service, url))
				}
			}
		}
	}

	return preview.String()
}

func (m model) getTmuxSessionContent(sessionName string) string {
	// Check if tmux session exists
	checkCmd := exec.Command("tmux", "has-session", "-t", sessionName)
	if err := checkCmd.Run(); err != nil {
		return "" // Session doesn't exist
	}

	// Try to get the currently active window first
	activeCmd := exec.Command("tmux", "display-message", "-t", sessionName, "-p", "#{window_index}")
	activeOutput, err := activeCmd.Output()
	if err != nil {
		return ""
	}

	windowIndex := strings.TrimSpace(string(activeOutput))
	target := fmt.Sprintf("%s:%s", sessionName, windowIndex)

	// Capture the pane content with more options
	captureCmd := exec.Command("tmux", "capture-pane", "-t", target, "-p", "-S", "-20")
	output, err := captureCmd.Output()
	if err != nil {
		// Fallback: try without the -S flag
		captureCmd = exec.Command("tmux", "capture-pane", "-t", target, "-p")
		output, err = captureCmd.Output()
		if err != nil {
			return ""
		}
	}

	content := string(output)

	// Split into lines
	lines := strings.Split(content, "\n")

	// Clean up and format the content
	ansiRegex := regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	var cleanLines []string
	for _, line := range lines {
		// Remove ANSI escape sequences for cleaner display
		cleaned := ansiRegex.ReplaceAllString(line, "")
		// Don't trim whitespace completely - preserve some formatting
		if len(strings.TrimSpace(cleaned)) > 0 {
			cleanLines = append(cleanLines, cleaned)
		}
	}

	// Limit the number of lines to fit in preview pane
	maxLines := 25
	if len(cleanLines) > maxLines {
		cleanLines = cleanLines[len(cleanLines)-maxLines:] // Show last N lines
	}

	if len(cleanLines) == 0 {
		// Try to get some basic session info instead
		var debugInfo strings.Builder
		debugInfo.WriteString(fmt.Sprintf("Session: %s\n", sessionName))
		debugInfo.WriteString(fmt.Sprintf("Target: %s\n\n", target))

		// Show windows
		infoCmd := exec.Command("tmux", "list-windows", "-t", sessionName, "-F", "#{window_index}: #{window_name} (#{window_active})")
		infoOutput, err := infoCmd.Output()
		if err == nil && len(infoOutput) > 0 {
			debugInfo.WriteString("Windows:\n")
			debugInfo.WriteString(string(infoOutput))
		}

		// Show panes in current window
		panesCmd := exec.Command("tmux", "list-panes", "-t", target, "-F", "#{pane_index}: #{pane_current_command} (#{pane_active})")
		panesOutput, err := panesCmd.Output()
		if err == nil && len(panesOutput) > 0 {
			debugInfo.WriteString("\nPanes:\n")
			debugInfo.WriteString(string(panesOutput))
		}

		debugInfo.WriteString(fmt.Sprintf("\nRaw content length: %d", len(content)))
		debugInfo.WriteString(fmt.Sprintf("\nRaw lines: %d", len(lines)))

		return debugInfo.String()
	}

	return strings.Join(cleanLines, "\n")
}

func (m model) calculateOptimalListWidth() int {
	if len(m.sessions) == 0 {
		return 30 // Minimum width for empty list
	}

	// Find the longest session name
	maxNameLength := 0
	for _, sess := range m.sessions {
		nameLength := len(sess.name)
		if nameLength > maxNameLength {
			maxNameLength = nameLength
		}
	}

	// Add padding for cursor (2) + borders (4) + some margin (4)
	optimalWidth := maxNameLength + 10

	// Set reasonable bounds
	minWidth := 25
	maxWidth := m.width / 3 // Don't take more than 1/3 of screen

	if optimalWidth < minWidth {
		return minWidth
	}
	if optimalWidth > maxWidth {
		return maxWidth
	}

	return optimalWidth
}

func (m model) refreshPreview() tea.Cmd {
	return tea.Tick(time.Millisecond*500, func(t time.Time) tea.Msg {
		return refreshPreviewMsg{}
	})
}

func (m model) refreshSessions() tea.Cmd {
	return tea.Tick(time.Second*2, func(t time.Time) tea.Msg {
		return refreshSessionsMsg{}
	})
}

func (m model) checkCaddyHealth() tea.Cmd {
	return func() tea.Msg {
		// Load sessions
		store, err := session.LoadSessions()
		if err != nil {
			return caddyHealthMsg{warning: "Failed to load sessions for Caddy check"}
		}

		// Load project registry
		registry, err := config.LoadProjectRegistry()
		if err != nil {
			return caddyHealthMsg{warning: "Failed to load projects for Caddy check"}
		}

		// Convert sessions to format needed by health check
		sessionInfos := make(map[string]*caddy.SessionInfo)
		for name, sess := range store.Sessions {
			info := &caddy.SessionInfo{
				Name:  name,
				Ports: sess.Ports,
			}

			// Find project alias if session is in a project
			for alias, project := range registry.Projects {
				if sess.ProjectPath == project.Path {
					info.ProjectAlias = alias
					break
				}
			}

			sessionInfos[name] = info
		}

		// Perform health check
		result, err := caddy.CheckCaddyHealth(sessionInfos)
		if err != nil {
			return caddyHealthMsg{warning: fmt.Sprintf("Caddy health check failed: %v", err)}
		}

		// Generate warning message if issues found
		var warning string
		if !result.CaddyRunning {
			warning = "⚠️  Caddy is not running. Session hostnames won't work."
		} else if result.CatchAllFirst {
			warning = "⚠️  Caddy routes are misconfigured. Run 'devx caddy check --fix' to repair."
		} else if result.RoutesNeeded > result.RoutesExisting {
			missing := result.RoutesNeeded - result.RoutesExisting
			warning = fmt.Sprintf("⚠️  %d Caddy routes are missing. Run 'devx caddy check --fix' to repair.", missing)
		}

		return caddyHealthMsg{warning: warning}
	}
}

func (m model) createView() string {
	return headerStyle.Render("Create New Session") + "\n\n" +
		"  Session name: " + m.textInput.View() + "\n\n" +
		dimStyle.Render("  Press Enter to create, Esc to cancel")
}

func (m model) confirmView() string {
	return headerStyle.Render("Confirm") + "\n\n" +
		"  " + m.confirmMsg + "\n"
}

func (m model) hostnamesView() string {
	if len(m.hostnames) == 0 {
		return headerStyle.Render("Caddy Hostnames") + "\n\n" +
			"  No hostnames found.\n"
	}

	var b strings.Builder
	b.WriteString(headerStyle.Render("Caddy Hostnames") + "\n\n")

	for i, hostname := range m.hostnames {
		cursor := "  "
		if i == m.hostnameCursor {
			cursor = "> "
		}

		url := fmt.Sprintf("http://%s.localhost", hostname)
		b.WriteString(fmt.Sprintf("%s%s\n", cursor, url))
	}

	return b.String()
}

func (m model) projectSelectView() string {
	if len(m.projects) == 0 {
		return headerStyle.Render("Select Project") + "\n\n" +
			"  No projects found.\n" +
			"  Use 'devx project add' to register projects.\n"
	}

	var b strings.Builder
	b.WriteString(headerStyle.Render("Select Project") + "\n\n")

	for i, project := range m.projects {
		cursor := "  "
		if i == m.projectCursor {
			cursor = "> "
		}

		description := project.description
		if description == "" {
			description = project.path
		}

		b.WriteString(fmt.Sprintf("%s%s (%s)\n", cursor, project.name, project.alias))
		b.WriteString(fmt.Sprintf("    %s\n", description))
		if i < len(m.projects)-1 {
			b.WriteString("\n")
		}
	}

	return b.String()
}

func (m model) startSessionCreation() tea.Cmd {
	// Load projects to see if we need to show project selection
	return func() tea.Msg {
		registry, err := config.LoadProjectRegistry()
		if err != nil {
			return errMsg{err}
		}

		// If no projects or only one project, go directly to session creation
		if len(registry.Projects) <= 1 {
			for alias := range registry.Projects {
				m.selectedProject = alias
				break
			}
			return sessionCreationStartedMsg{}
		}

		// Multiple projects, need to show selection first
		return projectSelectionNeededMsg{}
	}
}

func (m model) attachSession(name string) tea.Cmd {
	return tea.ExecProcess(attachCmd(name), func(err error) tea.Msg {
		if err != nil {
			return errMsg{err}
		}
		// Reload sessions to update the TUI with cleared flags
		return refreshSessionsMsg{}
	})
}

func (m model) createSession(name string) tea.Cmd {
	return func() tea.Msg {
		// Run the create command with project if selected
		cmd := createCmd(name, m.selectedProject)

		// Capture output for better error reporting
		output, err := cmd.CombinedOutput()
		if err != nil {
			// Include the command output in the error message
			errorMessage := fmt.Sprintf("failed to create session '%s': %v", name, err)
			if len(output) > 0 {
				// Clean up the output to make it more readable
				outputStr := strings.TrimSpace(string(output))
				errorMessage += fmt.Sprintf("\n\nCommand output:\n%s", outputStr)
			}
			return errMsg{fmt.Errorf(errorMessage)}
		}

		return sessionCreatedMsg{sessionName: name}
	}
}

func (m model) deleteSession(name string) tea.Cmd {
	return func() tea.Msg {
		// Run the delete command
		cmd := deleteCmd(name)
		if err := cmd.Run(); err != nil {
			return errMsg{err}
		}
		return sessionDeletedMsg{}
	}
}

func attachCmd(name string) *exec.Cmd {
	cmd := exec.Command("devx", "session", "attach", name)
	return cmd
}

func createCmd(name, project string) *exec.Cmd {
	args := []string{"session", "create", name}
	if project != "" {
		args = append(args, "--project", project)
	}
	cmd := exec.Command("devx", args...)

	// If a project is specified, we should run from the project's directory
	if project != "" {
		// Load project registry to get project path
		registry, err := config.LoadProjectRegistry()
		if err == nil {
			if proj, err := registry.GetProject(project); err == nil {
				cmd.Dir = proj.Path
			}
		}
	} else {
		// Otherwise, check if we're in a git repo
		gitRoot := findGitRoot()
		if gitRoot != "" {
			cmd.Dir = gitRoot
		}
	}

	return cmd
}

func findGitRoot() string {
	// Start from current directory
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}

	// Walk up the directory tree looking for .git
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root without finding .git
			break
		}
		dir = parent
	}

	return ""
}

func deleteCmd(name string) *exec.Cmd {
	return exec.Command("devx", "session", "rm", name, "--force")
}

func (m model) openRoutes(sessionName string) tea.Cmd {
	return func() tea.Msg {
		store, err := session.LoadSessions()
		if err != nil {
			return errMsg{err}
		}

		sess, exists := store.Sessions[sessionName]
		if !exists || len(sess.Routes) == 0 {
			return errMsg{fmt.Errorf("no routes found for session '%s'", sessionName)}
		}

		// Open all routes in the default browser
		for _, hostname := range sess.Routes {
			url := fmt.Sprintf("http://%s.localhost", hostname)
			if err := openURL(url); err != nil {
				return errMsg{fmt.Errorf("failed to open %s: %w", url, err)}
			}
		}

		return nil
	}
}

func openURL(url string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("unsupported platform")
	}

	return cmd.Start()
}

func (m model) editInEditor(sessionName string) tea.Cmd {
	return func() tea.Msg {
		store, err := session.LoadSessions()
		if err != nil {
			return errMsg{err}
		}

		sess, exists := store.Sessions[sessionName]
		if !exists {
			return errMsg{fmt.Errorf("session '%s' not found", sessionName)}
		}

		// Launch editor using the existing session functionality
		err = session.LaunchEditorForSession(sessionName, sess.Path)
		if err != nil {
			return errMsg{fmt.Errorf("failed to launch editor: %w", err)}
		}

		return nil
	}
}

func (m model) loadHostnames(sessionName string) tea.Cmd {
	return func() tea.Msg {
		store, err := session.LoadSessions()
		if err != nil {
			return errMsg{err}
		}

		// If a specific session is selected, only show its routes
		if sessionName != "" {
			sess, exists := store.Sessions[sessionName]
			if exists {
				var hostnames []string
				for serviceName := range sess.Routes {
					// Generate hostname based on project and session info
					dnsServiceName := caddy.NormalizeDNSName(serviceName)
					if sess.ProjectAlias != "" {
						hostnames = append(hostnames, fmt.Sprintf("%s-%s-%s", sess.ProjectAlias, sess.Name, dnsServiceName))
					} else {
						hostnames = append(hostnames, fmt.Sprintf("%s-%s", sess.Name, dnsServiceName))
					}
				}
				sort.Strings(hostnames)
				return hostnamesLoadedMsg{hostnames: hostnames}
			}
		}

		// Otherwise, show all hostnames (original behavior)
		// Use Caddy client to get actual routes
		client := caddy.NewCaddyClient()
		routes, err := client.GetAllRoutes()
		if err != nil {
			// Fall back to generating hostnames from session data
			hostnameSet := make(map[string]bool)
			for _, sess := range store.Sessions {
				for serviceName := range sess.Routes {
					// Generate hostname based on project and session info
					dnsServiceName := caddy.NormalizeDNSName(serviceName)
					if sess.ProjectAlias != "" {
						hostnameSet[fmt.Sprintf("%s-%s-%s", sess.ProjectAlias, sess.Name, dnsServiceName)] = true
					} else {
						hostnameSet[fmt.Sprintf("%s-%s", sess.Name, dnsServiceName)] = true
					}
				}
			}

			var hostnames []string
			for hostname := range hostnameSet {
				hostnames = append(hostnames, hostname)
			}
			sort.Strings(hostnames)
			return hostnamesLoadedMsg{hostnames: hostnames}
		}

		// Extract hostnames from actual Caddy routes
		hostnameSet := make(map[string]bool)
		for _, route := range routes {
			for _, match := range route.Match {
				for _, host := range match.Host {
					// Extract just the subdomain part (without .localhost)
					if strings.HasSuffix(host, ".localhost") {
						subdomain := strings.TrimSuffix(host, ".localhost")
						hostnameSet[subdomain] = true
					}
				}
			}
		}

		// Convert to sorted slice
		var hostnames []string
		for hostname := range hostnameSet {
			hostnames = append(hostnames, hostname)
		}
		sort.Strings(hostnames)
		return hostnamesLoadedMsg{hostnames: hostnames}
	}
}

func (m model) openHostname(hostname string) tea.Cmd {
	return func() tea.Msg {
		url := fmt.Sprintf("http://%s.localhost", hostname)
		if err := openURL(url); err != nil {
			return errMsg{fmt.Errorf("failed to open %s: %w", url, err)}
		}
		return nil
	}
}

func (m model) projectManagementView() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("Project Management") + "\n\n")

	// Show status message if present
	if m.statusMsg != "" {
		b.WriteString(lipgloss.NewStyle().
			Foreground(lipgloss.Color("70")).
			Render(m.statusMsg) + "\n\n")
	}

	if len(m.projects) == 0 {
		b.WriteString("  No projects registered.\n\n")
		b.WriteString("  Press 'c' to add a project.\n")
		return b.String()
	}

	// List projects with session counts
	for i, project := range m.projects {
		cursor := "  "
		if i == m.projectCursor {
			cursor = "> "
		}

		// Count sessions for this project
		sessionCount := 0
		for _, sess := range m.sessions {
			if sess.projectAlias == project.alias {
				sessionCount++
			}
		}

		b.WriteString(fmt.Sprintf("%s%s (%s)\n", cursor, project.name, project.alias))
		b.WriteString(fmt.Sprintf("    Path: %s\n", project.path))
		b.WriteString(fmt.Sprintf("    Sessions: %d\n", sessionCount))
		if project.description != "" {
			b.WriteString(fmt.Sprintf("    %s\n", project.description))
		}
		if i < len(m.projects)-1 {
			b.WriteString("\n")
		}
	}

	return b.String()
}

func (m model) projectAddView() string {
	return headerStyle.Render("Add Project") + "\n\n" +
		"  Project path: " + m.textInput.View() + "\n\n" +
		dimStyle.Render("  Enter the path to a git repository") + "\n" +
		dimStyle.Render("  Press Enter to add, Esc to cancel")
}

func (m model) loadProjectsForManagement() tea.Cmd {
	return func() tea.Msg {
		registry, err := config.LoadProjectRegistry()
		if err != nil {
			return errMsg{err}
		}

		// Also load sessions to get counts
		store, err := session.LoadSessions()
		if err != nil {
			return errMsg{err}
		}
		m.sessions = nil
		for _, sess := range store.Sessions {
			m.sessions = append(m.sessions, sessionItem{
				name:         sess.Name,
				projectAlias: sess.ProjectAlias,
			})
		}

		var projects []projectItem
		for alias, project := range registry.Projects {
			projects = append(projects, projectItem{
				alias:       alias,
				name:        project.Name,
				path:        project.Path,
				description: project.Description,
			})
		}

		// Sort projects by name
		sort.Slice(projects, func(i, j int) bool {
			return projects[i].name < projects[j].name
		})

		return projectsLoadedMsg{projects: projects}
	}
}

func (m model) addProject(path string) tea.Cmd {
	return func() tea.Msg {
		// Expand tilde if present
		if strings.HasPrefix(path, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return errMsg{fmt.Errorf("failed to get home directory: %w", err)}
			}
			path = filepath.Join(home, path[2:])
		}

		// Validate path exists
		absPath, err := filepath.Abs(path)
		if err != nil {
			return errMsg{fmt.Errorf("invalid path: %w", err)}
		}

		// Check if directory exists
		if info, err := os.Stat(absPath); err != nil {
			if os.IsNotExist(err) {
				return errMsg{fmt.Errorf("directory does not exist: %s", absPath)}
			}
			return errMsg{fmt.Errorf("failed to access directory: %w", err)}
		} else if !info.IsDir() {
			return errMsg{fmt.Errorf("path is not a directory: %s", absPath)}
		}

		// Check if it's a git repository
		gitPath := filepath.Join(absPath, ".git")
		if _, err := os.Stat(gitPath); err != nil {
			return errMsg{fmt.Errorf("not a git repository: %s\nPlease ensure the directory contains a .git folder", absPath)}
		}

		// Check for devx config
		configNote := ""
		devxConfigPath := filepath.Join(absPath, ".devx", "config.yaml")
		if _, err := os.Stat(devxConfigPath); os.IsNotExist(err) {
			configNote = " (using defaults - no .devx/config.yaml found)"
		}

		// Get project name from directory
		projectName := filepath.Base(absPath)

		// Generate alias (lowercase, no spaces)
		alias := strings.ToLower(strings.ReplaceAll(projectName, " ", "-"))

		// Add to registry
		registry, err := config.LoadProjectRegistry()
		if err != nil {
			return errMsg{err}
		}

		project := &config.Project{
			Name: projectName,
			Path: absPath,
		}

		if err := registry.AddProject(alias, project); err != nil {
			return errMsg{err}
		}

		// Return success message
		return projectAddedMsg{
			name:       projectName,
			alias:      alias,
			path:       absPath,
			configNote: configNote,
		}
	}
}

func (m model) removeProject(alias string) {
	registry, err := config.LoadProjectRegistry()
	if err != nil {
		m.err = err
		return
	}

	if err := registry.RemoveProject(alias); err != nil {
		m.err = err
		return
	}

	// Reload projects and sessions
	m.state = stateProjectManagement
}
