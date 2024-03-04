package ui

import (
	"encoding"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"get.pme.sh/pmesh/config"
	"get.pme.sh/pmesh/util"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Bimodel interface {
	tea.Model
	Run() error
}

func Run(m Bimodel) {
	var err error
	if *config.Dumb {
		err = m.Run()
	} else {
		p := tea.NewProgram(m, tea.WithAltScreen())
		_, err = p.Run()
	}
	if err != nil {
		fmt.Printf(RenderErrorLine(err) + "\n")
		os.Exit(1)
	}
}

// Displayer is an interface for displaying a string.
type Displayer interface {
	Display() string
}

func Display(v any) string {
	switch v := v.(type) {
	case struct{}:
		return ""
	case Displayer:
		return v.Display()
	case string:
		return v
	case error:
		return v.Error()
	case uint, uint8, uint16, uint32, uint64:
		return DisplayUint(reflect.ValueOf(v).Uint())
	case int, int8, int16, int32, int64:
		return DisplayInt(reflect.ValueOf(v).Int())
	case float32, float64, bool:
		return fmt.Sprint(v)
	case time.Duration:
		return util.Duration(v).Display()
	case fmt.Stringer:
		return v.String()
	case encoding.TextMarshaler:
		b, err := v.MarshalText()
		if err != nil {
			break
		}
		return string(b)
	case json.Marshaler:
		b, err := v.MarshalJSON()
		if err != nil {
			break
		}
		return string(b)
	default:
		json, err := json.Marshal(v)
		if err != nil {
			break
		}
		return string(json)
	}
	return fmt.Sprintf("[%T?]", v)
}

func DisplayArray(vs []any) []string {
	ss := make([]string, len(vs))
	for i, v := range vs {
		ss[i] = Display(v)
	}
	return ss
}

func Pad(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(" ", n)
}
func FixedBlock(pre string, post string, n int) string {
	if n <= 0 {
		return ""
	}

	m := lipgloss.Width(post)
	if m > n {
		post = lipgloss.NewStyle().MaxWidth(n).Render(post)
		post, _, _ = strings.Cut(post, "\n")
		post = post[:len(post)-1]
		post += "…"
	} else {
		post += Pad(n - m)
	}

	if pre == "" {
		return post
	}
	return pre + " " + post
}

func DisplayFloatWithGran(f float64, gran float64) string {
	f *= gran
	f = math.Round(f)
	f /= gran
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func DisplayFloat(f float64) string {
	mag := max(f, -f)
	switch {
	case mag > 1000:
		// <1000 -> %f
		return DisplayFloatWithGran(f, 1)
	case mag > 100:
		// <100 -> %.1f
		return DisplayFloatWithGran(f, 10)
	default:
		// <10 -> %.2f
		return DisplayFloatWithGran(f, 100)
	}
}
func DisplayUint(n uint64) string {
	const (
		K            = 1_000
		M            = 1_000 * K
		UpgradeCoeff = 2 // 500 -> 0.5k
	)

	switch {
	case n == 0:
		return "0"
	case n < K/UpgradeCoeff:
		return strconv.FormatUint(n, 10)
	case n < M/UpgradeCoeff:
		return DisplayFloatWithGran(float64(n)/K, 10) + "K"
	default:
		return DisplayFloatWithGran(float64(n)/M, 10) + "M"
	}
}
func DisplayInt(n int64) string {
	if n < 0 {
		return "-" + DisplayUint(uint64(-n))
	} else {
		return DisplayUint(uint64(n))
	}
}
