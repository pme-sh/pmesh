package ui

import (
	"fmt"
	"os"

	"github.com/charmbracelet/lipgloss"
)

var (
	FaintColor = lipgloss.AdaptiveColor{Light: "#D9DCCF", Dark: "#8b8b8b"}
	FaintStyle = lipgloss.NewStyle().Foreground(FaintColor)
	OkColor    = lipgloss.AdaptiveColor{Light: "#43BF6D", Dark: "#73F59F"}
	OkStyle    = lipgloss.NewStyle().Foreground(OkColor)
	ErrColor   = lipgloss.AdaptiveColor{Light: "#770000", Dark: "#AA0000"}
	ErrStyle   = lipgloss.NewStyle().Foreground(ErrColor)
	BrownColor = lipgloss.AdaptiveColor{Light: "#A67C53", Dark: "#A67C53"}
	BrownStyle = lipgloss.NewStyle().Foreground(BrownColor)
	DocStyle   = lipgloss.NewStyle().Margin(1, 2)
	TitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("32")).
			Padding(0, 1)
	TitleBarStyle = lipgloss.NewStyle().Padding(0, 0, 1, 2)
	HelpStyle     = lipgloss.NewStyle().Padding(1, 0, 0, 2)
	SpinnerStyle  = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#8E8E8E", Dark: "#747373"})
)

var errLinePfx = lipgloss.NewStyle().Background(ErrColor).Bold(true).Render(" ERR ") + " "
var okLinePfx = lipgloss.NewStyle().Background(OkColor).Bold(true).Render(" OK ") + " "

func RenderErrorLine(err any) string {
	return errLinePfx + Display(err)
}
func ExitWithError(err any) {
	fmt.Println(RenderErrorLine(err) + "\n")
	os.Exit(1)
}

func RenderOkLine(res any) string {
	return okLinePfx + Display(res)
}
