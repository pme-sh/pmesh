package ui

import (
	"cmp"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"get.pme.sh/pmesh/client"
	"get.pme.sh/pmesh/session"
	"get.pme.sh/pmesh/util"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/erikgeiser/promptkit/selection"
	"github.com/erikgeiser/promptkit/textinput"
	"github.com/samber/lo"
)

type ServiceControl struct {
	Use, Short string
	Aliases    []string
	WaitMsg    string
	Display    string
	Do         func(cli client.Client, name string) (msg string, err error)
}

var ServiceControls = []ServiceControl{
	{
		Use:     "restart [name]",
		Short:   "Restart service",
		Aliases: []string{"r"},
		WaitMsg: "Restarting...",
		Display: "💫 Restart",
		Do: func(cli client.Client, name string) (string, error) {
			n, e := cli.ServiceRestart(name, false)
			if e != nil {
				return "", e
			} else {
				return fmt.Sprintf("Restarted %d services", n.Count), nil
			}
		},
	},
	{
		Use:     "rebuild [name]",
		Short:   "Invalidates build cache and restarts service",
		Aliases: []string{"invalidate"},
		WaitMsg: "Rebuilding...",
		Display: "🔨 Rebuild",
		Do: func(cli client.Client, name string) (string, error) {
			n, e := cli.ServiceRestart(name, true)
			if e != nil {
				return "", e
			} else {
				return fmt.Sprintf("Rebuilt %d services", n.Count), nil
			}
		},
	},
	{
		Use:     "stop [name]",
		Short:   "Stops service",
		Aliases: []string{"kill"},
		WaitMsg: "Stopping the service...",
		Display: "🛑 Stop",
		Do: func(cli client.Client, name string) (string, error) {
			n, e := cli.ServiceStop(name)
			if e != nil {
				return "", e
			} else {
				return fmt.Sprintf("Stopped %d services", n.Count), nil
			}
		},
	},
}

type unblockMsg struct {
	m tea.Msg
}
type updateMsg struct {
	session.ServiceMetrics
	err error
}

type ServiceDetailModel struct {
	entry        *ServiceItem
	cl           client.Client
	controlIndex int
	busy         bool
}

func (m ServiceDetailModel) Init() tea.Cmd {
	return func() tea.Msg {
		time.Sleep(100 * time.Millisecond)
		info, err := m.cl.ServiceMetrics(m.entry.Name)
		return updateMsg{info, err}
	}
}

func (m ServiceDetailModel) Update(msg tea.Msg) (PageModel, tea.Cmd) {
	switch msg := msg.(type) {
	case updateMsg:
		if msg.err != nil {
			return m, ErrMsg(msg.err)
		}
		m.entry.ServiceMetrics = msg.ServiceMetrics
		return m, m.Init()
	case unblockMsg:
		m.busy = false
		return m, tea.Batch(
			func() tea.Msg {
				return msg.m
			},
			SetSpinnerState(false),
		)
	case KeyMatchMsg:
		switch msg.Key {
		case "esc":
			return m, Navigate(MakeServiceListModel(m.cl))
		case "right", "l":
			m.controlIndex++
			if m.controlIndex >= len(ServiceControls) {
				m.controlIndex = 0
			}
		case "left", "h":
			m.controlIndex--
			if m.controlIndex < 0 {
				m.controlIndex = len(ServiceControls) - 1
			}
		case "enter":
			if m.busy {
				return m, StatusMsg("Busy")
			}
			ctrl := ServiceControls[m.controlIndex]
			if ctrl.Do != nil {
				m.busy = true
				return m, tea.Batch(
					func() tea.Msg {
						msg, err := ctrl.Do(m.cl, m.entry.Name)
						if err != nil {
							return unblockMsg{ErrMsg(err)}
						} else {
							return unblockMsg{StatusMsg(msg)}
						}
					},
					SetSpinnerState(true),
				)
			} else {
				return m, ErrMsg("Not implemented")
			}
		}
	}
	return m, nil
}

func (m ServiceDetailModel) upstreamViewBasic() [][]Pair {
	tbl := [][]Pair{}
	for _, u := range m.entry.Server.Upstreams {
		state := "Down"
		if u.Healthy {
			state = "Up"
		}
		tbl = append(tbl, Pairs(
			"State", state,
			"Address", u.Address,
			"Load", fmt.Sprint(u.LoadFactor),
			"Requests", fmt.Sprint(u.RequestCount),
			"5xx", fmt.Sprint(u.ServerErrorCount),
			"4xx", fmt.Sprint(u.ClientErrorCount),
		))
	}
	return tbl
}
func (m ServiceDetailModel) upstreamView(w int) string {
	tbl := table.New().Width(w).
		BorderStyle(lipgloss.NewStyle().Foreground(FaintColor))
	tbl.StyleFunc(func(row, col int) lipgloss.Style {
		if row == 0 {
			return lipgloss.NewStyle().Bold(true)
		}
		return lipgloss.NewStyle().Padding(0, 1)
	})

	tbl.Headers("🏹 Address", "🔥 Load", "📥 Requests", "🚨 5xx", "😝 4xx")
	for _, u := range m.entry.Server.Upstreams {
		state := "🔴 "
		if u.Healthy {
			state = "🟢 "
		}
		tbl.Row(
			state+u.Address,
			fmt.Sprint(u.LoadFactor),
			fmt.Sprint(u.RequestCount),
			fmt.Sprint(u.ServerErrorCount),
			fmt.Sprint(u.ClientErrorCount),
		)
	}
	return tbl.Render()
}
func (m ServiceDetailModel) processListViewBasic() [][]Pair {
	tbl := [][]Pair{}
	for _, p := range m.entry.Processes {
		tbl = append(tbl, Pairs(
			"PID", fmt.Sprint(p.PID),
			"Command", p.BriefCmd(),
			"CPU", DisplayFloatWithGran(p.CPU, 100),
			"Memory", util.Size(int(p.RSS)).Display(),
			"IO", util.Size(int(p.IoRead+p.IoWrite)).Display(),
			"Uptime", util.Duration(time.Since(p.CreateTime)).Display(),
		))
	}
	return tbl
}
func (m ServiceDetailModel) processListView(w int) string {
	tbl := table.New().Width(w).
		BorderStyle(lipgloss.NewStyle().Foreground(FaintColor))
	tbl.StyleFunc(func(row, col int) lipgloss.Style {
		if row == 0 {
			return lipgloss.NewStyle().Bold(true)
		}
		return lipgloss.NewStyle().Padding(0, 1)
	})

	sort.Slice(m.entry.Processes, func(i, j int) bool {
		return m.entry.Processes[i].CreateTime.Before(m.entry.Processes[j].CreateTime)
	})

	tbl.Headers("💳 PID", "🎯 Command", "💻 CPU", "🧠 Memory", "💾 IO", "⌚ Uptime")
	for _, p := range m.entry.Processes {
		tbl.Row(
			fmt.Sprint(p.PID),
			lipgloss.NewStyle().MaxWidth(20).Render(p.BriefCmd()),
			DisplayFloatWithGran(p.CPU, 100),
			util.Size(int(p.RSS)).Display(),
			util.Size(int(p.IoRead+p.IoWrite)).Display(),
			util.Duration(time.Since(p.CreateTime)).Display(),
		)
	}
	return tbl.Render()
}
func (m ServiceDetailModel) buttonsView() string {
	var buttons []string
	for i, c := range ServiceControls {
		style := lipgloss.NewStyle().Width(12).Padding(0, 1).Border(lipgloss.NormalBorder(), true, true, true, true)
		if i != m.controlIndex {
			style = style.BorderForeground(FaintColor)
		}
		buttons = append(buttons, style.Render(c.Display))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, buttons...)
}

func (m ServiceDetailModel) View(w, h int) string {
	status, _ := m.entry.viewStatus()
	buttons := m.buttonsView()

	padmax := w - lipgloss.Width(status)
	padmax -= lipgloss.Width(buttons) + 2
	if padmax > 0 {
		status = lipgloss.NewStyle().PaddingRight(padmax).Render(status)
	}

	tblstyle := lipgloss.NewStyle().Margin(1, 0, 1, 0)
	return lipgloss.JoinVertical(
		lipgloss.Left,
		lipgloss.JoinHorizontal(
			lipgloss.Top,
			status,
			buttons,
		),
		lipgloss.JoinHorizontal(
			lipgloss.Top,
			tblstyle.Render(m.upstreamView(w/2)),
			tblstyle.Render(m.processListView(w/2)),
		),
	)
}
func (m ServiceDetailModel) Run() error {

	u, err := m.cl.ServiceMetrics(m.entry.Name)
	if err != nil {
		return err
	}
	m.entry.ServiceMetrics = u

	fmt.Println(BasicTable(m.processListViewBasic()))
	fmt.Println(BasicTable(m.upstreamViewBasic()))
	return nil
}

type ServiceItem struct {
	Name string
	session.ServiceMetrics
}

func (i ServiceItem) Title() string {
	return i.Name + " " + FaintStyle.Render("("+i.Type+")")
}

func (i *ServiceItem) getCPU() string {
	var cpu float64
	for _, p := range i.Processes {
		cpu += p.CPU
	}
	return DisplayFloatWithGran(cpu, 100)
}
func (i *ServiceItem) getMemory() string {
	var mem int
	for _, p := range i.Processes {
		mem += int(p.RSS)
	}
	return util.Size(mem).Display()
}
func (i ServiceItem) getErrs() (load, serr, cerr uint64) {
	for _, p := range i.Server.Upstreams {
		serr += uint64(p.ServerErrorCount + p.ErrorCount)
		cerr += uint64(p.ClientErrorCount)
		load += uint64(p.LoadFactor)
	}
	return
}

func (i ServiceItem) viewStatus() (view string, norun bool) {
	statusMsg := i.Status
	instanceState := ""

	// Status
	if i.Total != 0 || i.Status != "OK" {
		if i.Status == "Down" {
			instanceState = fmt.Sprintf("❌ %d/%d", i.Healthy, i.Total)
			if i.Err != "" {
				return "❌ " + ErrStyle.Render(i.Err), true
			} else {
				statusMsg = ErrStyle.Render("Down")
			}
		} else if i.Status == "OK" {
			instanceState = fmt.Sprintf("🟢 %d/%d", i.Healthy, i.Total)
			statusMsg = OkStyle.Render("OK")
		} else {
			instanceState = fmt.Sprintf("❓ %d/%d", i.Healthy, i.Total)
		}
	} else {
		if np := len(i.Processes); np == 0 {
			return "🧊 Passive", true
		} else {
			instanceState = fmt.Sprintf("🔨 %d", np)
			statusMsg = BrownStyle.Render("OK")
		}
	}
	return lipgloss.JoinVertical(lipgloss.Top, instanceState, statusMsg), false
}
func (i ServiceItem) viewRuntime() (view string) {
	rsrcUse := ""
	webStatus := ""

	if len(i.Processes) > 0 {
		rsrcUse += fmt.Sprintf("💻 cpu: %s ", i.getCPU())
		rsrcUse += fmt.Sprintf("🧠 mem: %s ", i.getMemory())
	}

	load, serr, cerr := i.getErrs()
	if len(i.Server.Upstreams) != 0 {
		webStatus += fmt.Sprintf("⌛ wait: %s ", DisplayUint(load))
		webStatus += fmt.Sprintf("😝 4xx: %s ", DisplayUint(cerr))
		webStatus += fmt.Sprintf("🚨 5xx: %s", DisplayUint(serr))
	}
	return lipgloss.JoinVertical(lipgloss.Top, rsrcUse, webStatus)
}

func (i ServiceItem) Description() string {
	status, norun := i.viewStatus()
	if norun {
		return status
	}
	runtime := i.viewRuntime()

	colStyle := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, true, false, false).
		Foreground(FaintColor).
		BorderForeground(FaintColor).
		MarginRight(2).
		Height(2)

	leftColStyle := colStyle.Copy().Width(15)
	rightColStyle := colStyle.Copy().Width(40).BorderRight(false)

	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		leftColStyle.Render(status),
		rightColStyle.Render(runtime),
	)
}
func (i ServiceItem) FilterValue() string { return i.Name }

func (i ServiceItem) Entries() (m []Pair) {
	load, serr, cerr := i.getErrs()
	return Pairs(
		"Name", i.Name,
		"Type", i.Type,
		"Status", i.Status,
		"Health", fmt.Sprintf("%d/%d", i.Healthy, i.Total),
		"Load", fmt.Sprintf("%d", load),
		"4xx", fmt.Sprintf("%d", cerr),
		"5xx", fmt.Sprintf("%d", serr),
		"CPU", i.getCPU(),
		"Mem", i.getMemory(),
	)
}

type spinnyModel struct {
	spinner.Model
	msg  string
	done <-chan struct{}
}
type spinnyDone struct{}

func (m spinnyModel) Init() tea.Cmd {
	return tea.Batch(
		m.Model.Tick,
		func() tea.Msg {
			<-m.done
			return spinnyDone{}
		},
	)
}
func (m spinnyModel) View() string {
	return m.Model.View() + " " + m.msg
}
func (m spinnyModel) Update(msg tea.Msg) (mm tea.Model, cmd tea.Cmd) {
	switch msg := msg.(type) {
	case spinnyDone:
		return m, tea.Quit
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	default:
		m.Model, cmd = m.Model.Update(msg)
	}
	return m, cmd
}

func SpinnyAsync(msg string, done <-chan struct{}) {
	p := tea.NewProgram(spinnyModel{
		Model: spinner.New(
			spinner.WithSpinner(spinner.Points),
			spinner.WithStyle(SpinnerStyle),
		),
		msg:  msg,
		done: done,
	})
	if _, err := p.Run(); err != nil {
		ExitWithError(err)
	}
	select {
	case <-done:
		return
	default:
		ExitWithError("aborted")
	}
}
func SpinnyWait[T any](msg string, f func() (T, error)) (res T) {
	done := make(chan struct{})
	go func() {
		var err error
		if res, err = f(); err != nil {
			ExitWithError(err)
		}
		close(done)
	}()
	SpinnyAsync(msg, done)
	return
}

func PromptString(msg string, initial string, placeholder string, validate func(s string) error) string {
	input := textinput.New(msg)
	input.InitialValue = initial
	input.Placeholder = placeholder
	if validate != nil {
		input.Validate = validate
	}
	res, err := input.RunPrompt()
	if err != nil {
		ExitWithError(err)
	}
	return res
}
func PromptSelect(title string, list []string) string {
	sp := selection.New("", list)
	sp.PageSize = 4
	sp.FilterPrompt = title
	sp.FilterPlaceholder = ""
	choice, err := sp.RunPrompt()
	if err != nil {
		ExitWithError(err)
	}
	return choice
}

func PromptSelectValueDesc[V cmp.Ordered](title string, vd map[V]string) V {
	keys := lo.Keys(vd)
	slices.Sort(keys)

	desc := make([]string, len(keys))
	maxlen := 0
	for i, k := range keys {
		desc[i] = fmt.Sprint(k)
		maxlen = max(maxlen, len(desc[i]))
	}
	for i, s := range desc {
		desc[i] = "\033[1m" + s + "\033[0m:" + strings.Repeat(" ", 1+maxlen-len(s))
	}
	for i, k := range keys {
		desc[i] = desc[i] + vd[k]
	}

	sel := PromptSelect(title, desc)

	for i := range desc {
		if desc[i] == sel {
			return keys[i]
		}
	}
	ExitWithError("invalid selection")
	panic("unreachable")
}

func PromptSelectService(cl client.Client) string {
	mp, err := cl.ServiceMetricsMap()
	if err != nil {
		ExitWithError(err)
	}
	return PromptSelect("Pick a service: ", lo.Keys(mp))
}

func MakeServiceListModel(cl client.Client) Bimodel {
	del := list.NewDefaultDelegate()
	del.SetHeight(3)
	return NewList[*ServiceItem](del).
		WithTitle("Services").
		WithPull(func() ([]*ServiceItem, error) {
			services, err := cl.ServiceMetricsMap()
			if err != nil {
				return nil, err
			}
			return MapToStableList(services, func(k string, sv session.ServiceMetrics) *ServiceItem {
				return &ServiceItem{k, sv}
			}), nil
		}).
		WithThen(func(i *ServiceItem) tea.Model {
			if i == nil {
				return nil
			}
			return MakeServiceDetailModel(cl, i)
		})
}

func MakeServiceDetailModel(cl client.Client, item *ServiceItem) Bimodel {
	return NewPage(ServiceDetailModel{entry: item, cl: cl}, PageProps{
		Title: "/" + item.Name,
		Keys: []key.Binding{
			key.NewBinding(
				key.WithKeys("esc"),
				key.WithHelp("esc", "back to list"),
			),
			key.NewBinding(
				key.WithKeys("enter"),
				key.WithHelp("enter", "apply"),
			),
			key.NewBinding(
				key.WithKeys("right", "l"),
				key.WithHelp("→/l", "next control"),
			),
			key.NewBinding(
				key.WithKeys("left", "h"),
				key.WithHelp("←/h", "prev control"),
			),
		},
	})
}
