package util

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"

	"github.com/shirou/gopsutil/v3/mem"
)

type Size int

var GetTotalMemory = sync.OnceValue(func() int64 {
	v, e := mem.VirtualMemory()
	if e != nil {
		// whatever
		return 16 * 1024 * 1024 * 1024
	}
	return int64(v.Available)
})

var units = map[string]float64{
	"b":        1,
	"byte":     1,
	"k":        1024,
	"kb":       1000,
	"kib":      1024,
	"kilobyte": 1000,
	"kibibyte": 1024,
	"m":        math.Pow(1024, 2),
	"mb":       math.Pow(1000, 2),
	"mib":      math.Pow(1024, 2),
	"megabyte": math.Pow(1000, 2),
	"mebibyte": math.Pow(1024, 2),
	"g":        math.Pow(1024, 3),
	"gb":       math.Pow(1000, 3),
	"gib":      math.Pow(1024, 3),
	"gigabyte": math.Pow(1000, 3),
	"gibibyte": math.Pow(1024, 3),
}

func NewSize(d any) Size {
	switch d := d.(type) {
	case Size:
		return d
	case int:
		return Size(d)
	case int64:
		return Size(int(d))
	case uint:
		return Size(int(d))
	case uint64:
		return Size(int(d))
	case string:
		res := Size(0)
		res.UnmarshalText([]byte(d))
		return res
	default:
		log.Fatalf("unsupported type: %T", d)
		return Size(0)
	}
}

func (d Size) IsZero() bool {
	return d == 0
}
func (d Size) IsPositive() bool {
	return d > 0
}
func (d Size) String() string {
	if d < 0 {
		return "-" + Size(-d).String()
	} else if d == 0 {
		return "0"
	} else {
		if d <= 1024 {
			return fmt.Sprintf("%db", d)
		}
		d >>= 10
		if d <= 1024 {
			return fmt.Sprintf("%dkb", d)
		}
		d >>= 10
		if d <= 1024 {
			return fmt.Sprintf("%dmb", d)
		}
		d >>= 10
		return fmt.Sprintf("%dgb", d)
	}
}
func (n Size) Display() string {

	const (
		K            = 1_000
		M            = 1_000 * K
		G            = 1_000 * M
		UpgradeCoeff = 2 // 500b -> 0.5kb
	)

	switch {
	case n < 0:
		return "-" + (-n).String()
	case n == 0:
		return "0"
	case n < K/UpgradeCoeff:
		return fmt.Sprintf("%dB", int(n))
	case n < M/UpgradeCoeff:
		return fmt.Sprintf("%.1fKb", float64(n)/K)
	case n < G/UpgradeCoeff:
		return fmt.Sprintf("%.1fMb", float64(n)/M)
	default:
		return fmt.Sprintf("%.1fGb", float64(n)/G)
	}
}
func (d Size) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}
func (d *Size) UnmarshalText(text []byte) (err error) {
	var val float64
	var unit string
	_, err = fmt.Sscanf(string(text), "%f%s", &val, &unit)
	if err != nil {
		return
	}
	unit = strings.TrimSpace(unit)
	unit, _ = strings.CutSuffix(unit, "s")
	unit = strings.ToLower(unit)
	unit = strings.TrimPrefix(unit, " ")
	if unit == "" {
		unit = "b"
	}
	if unit == "%" || unit == "percent" {
		*d = Size(val * float64(GetTotalMemory()) / 100)
		return
	}
	if mult, ok := units[unit]; ok {
		*d = Size(val * mult)
		return
	}
	unit = string([]byte(unit)[:1])
	if mult, ok := units[unit]; ok {
		*d = Size(val * mult)
		return
	}
	return fmt.Errorf("invalid unit: %s", unit)
}

func (d Size) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}
func (d *Size) UnmarshalJSON(text []byte) (err error) {
	*d = 0
	if len(text) == 0 {
		return nil
	}
	if text[0] == 'n' {
		return nil
	} else if text[0] == '"' {
		var str string
		err = json.Unmarshal(text, &str)
		if err != nil {
			return
		}
		return d.UnmarshalText(text)
	} else {
		var fp float64
		err = json.Unmarshal(text, &fp)
		if err != nil {
			return
		}
		*d = Size(fp)
		return
	}
}
func (d Size) Bytes() int {
	return int(d)
}
func (d Size) Kilobytes() int {
	return int(d >> 10)
}
func (d Size) Megabytes() int {
	return int(d >> 20)
}
func (d Size) Gigabytes() int {
	return int(d >> 30)
}
