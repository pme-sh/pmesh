package ui

import (
	"cmp"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/truncate"
	"golang.org/x/term"
)

type PageProps struct {
	Title string
	Keys  []key.Binding
}

type PageModel interface {
	// Interactive page.
	Update(tea.Msg) (PageModel, tea.Cmd)
	View(w, h int) string
	// Non-interactive page.
	Run() error
}

type Page struct {
	showTitle  bool
	showFilter bool
	showHelp   bool

	Title string

	// Key mappings for navigating the list.
	KeyMap []key.Binding

	width  int
	height int
	Help   help.Model

	StatusMessageLifetime time.Duration
	statusMessage         string
	statusMessageTimer    *time.Timer

	spinner     spinner.Model
	showSpinner bool

	inner PageModel
	init  bool
}

// New returns a new model with sensible defaults.
func NewPage(inner PageModel, prop PageProps) Page {
	sp := spinner.New()
	sp.Spinner = spinner.Points
	sp.Style = SpinnerStyle

	m := Page{
		showTitle:             true,
		showFilter:            true,
		showHelp:              true,
		Title:                 cmp.Or(prop.Title, "pmesh"),
		StatusMessageLifetime: 2 * time.Second,
		KeyMap: append(prop.Keys, key.NewBinding(
			key.WithKeys("q"),
			key.WithHelp("q", "quit"),
		)),
		spinner: sp,
		Help:    help.New(),
		inner:   inner,
	}

	w, h, _ := term.GetSize(int(os.Stdout.Fd()))
	m.setSize(w, h)
	return m
}

// SetShowTitle shows or hides the title bar.
func (m *Page) SetShowTitle(v bool) {
	m.showTitle = v
}

// ShowTitle returns whether or not the title bar is set to be rendered.
func (m Page) ShowTitle() bool {
	return m.showTitle
}

// SetShowHelp shows or hides the help view.
func (m *Page) SetShowHelp(v bool) {
	m.showHelp = v
}

// ShowHelp returns whether or not the help is set to be rendered.
func (m Page) ShowHelp() bool {
	return m.showHelp
}

// Width returns the current width setting.
func (m Page) Width() int {
	return m.width
}

// Height returns the current height setting.
func (m Page) Height() int {
	return m.height
}

// SetSpinner allows to set the spinner style.
func (m *Page) SetSpinner(spinner spinner.Spinner) {
	m.spinner.Spinner = spinner
}

// StartSpinner starts the spinner. Note that this returns a command.
func (m *Page) StartSpinner() tea.Cmd {
	m.showSpinner = true
	return m.spinner.Tick
}

// StopSpinner stops the spinner.
func (m *Page) StopSpinner() {
	m.showSpinner = false
}

type statusMessageTimeoutMsg struct{}
type statusMessageMsg struct {
	Message string
}

type setSpinnerStateMsg bool

func SetSpinnerState(b bool) tea.Cmd {
	return func() tea.Msg {
		return setSpinnerStateMsg(b)
	}
}

func StatusMsg(msg string) tea.Cmd {
	return func() tea.Msg {
		return statusMessageMsg{Message: msg}
	}
}
func ErrMsg(err any) tea.Cmd {
	return StatusMsg(RenderErrorLine(err))
}

// SetSize sets the width and height of this component.
func (m *Page) SetSize(width, height int) {
	m.setSize(width, height)
}

// SetWidth sets the width of this component.
func (m *Page) SetWidth(v int) {
	m.setSize(v, m.height)
}

// SetHeight sets the height of this component.
func (m *Page) SetHeight(v int) {
	m.setSize(m.width, v)
}

func (m *Page) setSize(width, height int) {
	if width != 0 {
		w, h := DocStyle.GetFrameSize()
		width -= w
		height -= h
	}
	m.width = width
	m.height = height
	m.Help.Width = width
}

func (m *Page) hideStatusMessage() {
	m.statusMessage = ""
	if m.statusMessageTimer != nil {
		m.statusMessageTimer.Stop()
	}
}

func (m Page) Init() tea.Cmd {
	return nil
}

type NavigateMsg struct {
	Model tea.Model
}
type KeyMatchMsg struct {
	Key string
}

func Navigate(model tea.Model) tea.Cmd {
	return func() tea.Msg {
		return NavigateMsg{Model: model}
	}
}

// Update is the Bubble Tea update loop.
func (m Page) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case NavigateMsg:
		return msg.Model, tea.ClearScreen
	case tea.KeyMsg:
		if msg.String() == "q" {
			return m, tea.Quit
		}
		for _, binding := range m.KeyMap {
			if key.Matches(msg, binding) {
				newInner, icmd := m.inner.Update(KeyMatchMsg{Key: binding.Keys()[0]})
				m.inner = newInner
				return m, icmd
			}
		}
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
	case spinner.TickMsg:
		newSpinnerModel, cmd := m.spinner.Update(msg)
		m.spinner = newSpinnerModel
		if m.showSpinner {
			cmds = append(cmds, cmd)
		}
	case setSpinnerStateMsg:
		m.showSpinner = bool(msg)
		if m.showSpinner {
			cmds = append(cmds, m.spinner.Tick)
		}
	case statusMessageMsg:
		m.statusMessage = msg.Message
		if m.statusMessageTimer != nil {
			m.statusMessageTimer.Stop()
		}
		m.statusMessageTimer = time.NewTimer(m.StatusMessageLifetime)
		// Wait for timeout
		return m, func() tea.Msg {
			<-m.statusMessageTimer.C
			return statusMessageTimeoutMsg{}
		}
	case statusMessageTimeoutMsg:
		m.hideStatusMessage()
	}

	newInner, icmd := m.inner.Update(msg)
	m.inner = newInner
	if icmd != nil {
		cmds = append(cmds, icmd)
	}

	if !m.init {
		m.init = true
		if i, ok := m.inner.(interface{ Init() tea.Cmd }); ok {
			cmds = append(cmds, i.Init())
		}
	}
	return m, tea.Batch(cmds...)
}

// View renders the component.
func (m Page) View() string {
	var (
		sections    []string
		availHeight = m.height
	)

	if m.showTitle {
		v := m.titleView()
		sections = append(sections, v)
		availHeight -= lipgloss.Height(v)
	}

	var help string
	if m.showHelp {
		help = m.helpView()
		availHeight -= lipgloss.Height(help)
	}

	content := lipgloss.NewStyle().PaddingLeft(2).Height(availHeight).Render(m.inner.View(m.width-2, availHeight))
	sections = append(sections, content)

	if m.showHelp {
		sections = append(sections, help)
	}

	return DocStyle.Render(lipgloss.JoinVertical(lipgloss.Left, sections...))
}

func (m Page) titleView() string {
	var (
		view          string
		titleBarStyle = TitleBarStyle.Copy()

		// We need to account for the size of the spinner, even if we don't
		// render it, to reserve some space for it should we turn it on later.
		spinnerView    = m.spinnerView()
		spinnerWidth   = lipgloss.Width(spinnerView)
		spinnerLeftGap = " "
		spinnerOnLeft  = titleBarStyle.GetPaddingLeft() >= spinnerWidth+lipgloss.Width(spinnerLeftGap) && m.showSpinner
	)

	if m.showTitle {
		if m.showSpinner && spinnerOnLeft {
			view += spinnerView + spinnerLeftGap
			titleBarGap := titleBarStyle.GetPaddingLeft()
			titleBarStyle = titleBarStyle.PaddingLeft(titleBarGap - spinnerWidth - lipgloss.Width(spinnerLeftGap))
		}

		view += TitleStyle.Render(m.Title)

		// Status message
		view += "  " + m.statusMessage
		view = truncate.StringWithTail(view, uint(m.width-spinnerWidth), "…")
	}

	// Spinner
	if m.showSpinner && !spinnerOnLeft {
		// Place spinner on the right
		availSpace := m.width - lipgloss.Width(TitleBarStyle.Render(view))
		if availSpace > spinnerWidth {
			view += strings.Repeat(" ", availSpace-spinnerWidth)
			view += spinnerView
		}
	}

	if len(view) > 0 {
		return titleBarStyle.Render(view)
	}
	return view
}

func (m Page) ShortHelp() []key.Binding {
	return m.KeyMap
}
func (m Page) FullHelp() [][]key.Binding {
	return [][]key.Binding{m.KeyMap}
}

func (m Page) helpView() string {
	return HelpStyle.Render(m.Help.View(m))
}

func (m Page) spinnerView() string {
	return m.spinner.View()
}
func (m Page) Run() error {
	return m.inner.Run()
}
