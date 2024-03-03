package ui

import (
	"fmt"
	"os"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/samber/lo"
	"golang.org/x/term"
)

type Pair struct {
	Key   string
	Value string
}

func Pairs(in ...string) (r []Pair) {
	for i := 0; i < len(in); i += 2 {
		r = append(r, Pair{in[i], in[i+1]})
	}
	return
}

func BasicTable(data [][]Pair) string {
	keys := []string{}
	items := []map[string]string{}
	for _, kv := range data {
		e := map[string]string{}
		for _, v := range kv {
			if !lo.Contains(keys, v.Key) {
				keys = append(keys, v.Key)
			}
			e[v.Key] = v.Value
		}
		items = append(items, e)
	}
	tbl := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(FaintStyle).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == 0 {
				return lipgloss.NewStyle().Bold(true)
			}
			return lipgloss.NewStyle()
		})

	w, _, _ := term.GetSize(int(os.Stdout.Fd()))
	if w == 0 {
		w = 80
	}
	w = min(w, 120)
	tbl.Width(w)

	tbl.Headers(keys...)
	for _, item := range items {
		row := []string{}
		for _, k := range keys {
			row = append(row, item[k])
		}
		tbl = tbl.Row(row...)
	}
	return tbl.Render()
}

type Item interface {
	list.DefaultItem
	FilterValue() string
	Entries() []Pair
}

type List[T Item] struct {
	Model    list.Model
	pick     func(T) tea.Model
	observer Observer[[]T]
	lastBusy bool
}

func (m List[T]) Init() tea.Cmd {
	return nil
}
func (m List[T]) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			if m.pick != nil {
				var zero T
				if model := m.pick(zero); model != nil {
					return model, tea.ClearScreen
				}
			}
		case "enter":
			if m.pick != nil {
				var res T
				if el := m.Model.SelectedItem(); el != nil {
					res, _ = el.(T)
				}
				if model := m.pick(res); model != nil {
					return model, tea.ClearScreen
				}
			}
		}
	case tea.WindowSizeMsg:
		fw, fh := DocStyle.GetFrameSize()
		m.Model.SetSize(msg.Width-fw, msg.Height-fh)
	}

	var cmds [3]tea.Cmd

	m.Model, cmds[0] = m.Model.Update(msg)
	cmds[1] = m.observer.Observe(msg, func(i []T, err error) tea.Cmd {
		if err == nil {
			ir := make([]list.Item, len(i))
			for j, el := range i {
				ir[j] = el
			}
			return m.Model.SetItems(ir)
		} else {
			return m.Model.NewStatusMessage("\n" + RenderErrorLine(err))
		}
	})

	if busy := m.observer.Busy; busy != m.lastBusy {
		m.lastBusy = busy
		if busy {
			cmds[2] = m.Model.StartSpinner()
		} else {
			m.Model.StopSpinner()
		}
	}
	return m, tea.Batch(cmds[:]...)
}

func (m List[T]) View() string {
	return DocStyle.Render(m.Model.View())
}

// Options.
func (m List[T]) WithStore(s Store[[]T]) List[T] {
	m.observer = MakeObserver(s)
	return m
}
func (m List[T]) WithPull(f func() ([]T, error)) List[T] {
	m.observer = MakeObserver(PullStore[[]T](f))
	return m
}
func (m List[T]) WithTitle(title string) List[T] {
	m.Model.Title = title
	return m
}
func (m List[T]) WithThen(then func(T) tea.Model) List[T] {
	m.pick = then
	m.Model.AdditionalShortHelpKeys = func() []key.Binding {
		return []key.Binding{
			key.NewBinding(
				key.WithKeys("enter"),
				key.WithHelp("enter", "pick"),
			),
			key.NewBinding(
				key.WithKeys("esc"),
				key.WithHelp("esc", "cancel"),
			),
		}
	}
	return m
}
func (m List[T]) Run() error {
	data, _, err := m.observer.Source.Next()
	if err != nil {
		return err
	}
	fmt.Println(BasicTable(lo.Map(data, func(i T, _ int) []Pair { return i.Entries() })))
	return nil
}

func NewList[T Item](del list.ItemDelegate) List[T] {
	w, h, _ := term.GetSize(int(os.Stdout.Fd()))
	fw, fh := DocStyle.GetFrameSize()
	m := list.New(nil, del, w-fw, h-fh)
	m.SetShowStatusBar(false)
	return List[T]{
		Model: m,
	}
}
