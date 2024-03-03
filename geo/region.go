package geo

type Region struct {
	Code string
	Name string
	Coord
}

func (c Region) String() string {
	return c.Name
}

var region0 = Region{"EU", "Europe", Coord{50, 15}} // Europe
var regionN = []Region{
	{"NA", "North America", Coord{40, -100}},
	{"AS", "Asia", Coord{35, 105}},
	{"SA", "South America", Coord{-15, -60}},
	{"AF", "Africa", Coord{15, 25}},
	{"AN", "Antarctica", Coord{-90, 0}},
	{"OC", "Oceania", Coord{-25, 135}},
}

func (c Coord) Region() Region {
	min := region0.DistanceTo(c)
	continent := region0
	for _, r := range regionN {
		dist := r.DistanceTo(c)
		if dist < min {
			min = dist
			continent = r
		}
	}
	return continent
}
