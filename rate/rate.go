package rate

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Rate struct {
	Count  uint
	Period time.Duration
}

func Parse(s string) (Rate, error) {
	var r Rate
	err := r.UnmarshalText([]byte(s))
	return r, err
}
func MustParse(s string) Rate {
	r, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return r
}

func (d Rate) IsZero() bool {
	return d.Period == 0
}
func (d Rate) String() string {
	if d.IsZero() {
		return "0"
	}
	return fmt.Sprintf("%d/%s", d.Count, d.Period)
}
func (d Rate) IsPositive() bool {
	return d.Count > 0 && d.Period > 0
}
func (d Rate) Compare(o Rate) int {
	o = o.Rescale(d.Period)
	return int(d.Count) - int(o.Count)
}
func (d Rate) Faster(o Rate) bool {
	return d.Compare(o) < 0
}
func (d Rate) Slower(o Rate) bool {
	return d.Compare(o) > 0
}
func (d Rate) Rescale(period time.Duration) Rate {
	count := float64(d.Count) * float64(d.Period.Milliseconds()) / float64(period.Milliseconds())
	return Rate{
		Count:  uint(count),
		Period: period,
	}
}
func (d Rate) Interval() time.Duration {
	return d.Period / time.Duration(d.Count)
}
func (d Rate) Clamp(rateMin Rate, rateMax Rate) Rate {
	cmin := rateMin.Rescale(d.Period).Count
	cmax := rateMax.Rescale(d.Period).Count
	return Rate{
		Count:  max(min(d.Count, cmax), cmin),
		Period: d.Period,
	}
}
func (d Rate) ClampPeriod(pmin time.Duration, pmax time.Duration) Rate {
	return Rate{
		Count:  d.Count,
		Period: max(min(d.Period, pmax), pmin),
	}
}
func (d Rate) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}
func (d *Rate) UnmarshalText(text []byte) (err error) {
	if len(text) == 0 || string(text) == "0" {
		*d = Rate{}
		return nil
	}
	if before, after, ok := strings.Cut(string(text), "/"); ok {
		var n uint64
		n, err = strconv.ParseUint(before, 10, 32)
		if err != nil {
			return
		}
		d.Count = uint(n)
		d.Period, err = time.ParseDuration(after)
		if err != nil {
			after = "1" + after
			d.Period, err = time.ParseDuration(after)
		}
		return
	} else {
		d.Count = 1
		d.Period, err = time.ParseDuration(string(text))
		return
	}
}
func (d Rate) MarshalYAML() (any, error) {
	return d.String(), nil
}
func (d *Rate) UnmarshalYAML(node *yaml.Node) error {
	var res string
	if err := node.Decode(&res); err != nil {
		return err
	}
	return d.UnmarshalText([]byte(res))
}
func (d Rate) MarshalJSON() ([]byte, error) {
	return d.MarshalText()
}
func (d *Rate) UnmarshalJSON(data []byte) error {
	var res string
	if err := json.Unmarshal(data, &res); err != nil {
		return err
	}
	return d.UnmarshalText([]byte(res))
}
