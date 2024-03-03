package geo

import (
	"fmt"
	"math"
)

type Coord struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

func LatLon(lat, lon float64) Coord {
	return Coord{lat, lon}
}

func (c Coord) String() string {
	return fmt.Sprintf("(%f,%f)", c.Lat, c.Lon)
}

func (c Coord) DistanceTo(o Coord) float64 {
	// Latitude: 1 deg = 110.574 km · Longitude: 1 deg = 111.320*cos(latitude)
	var (
		r1     = math.Pi * c.Lat / 180
		r2     = math.Pi * o.Lat / 180
		theta  = c.Lon - o.Lon
		rtheta = math.Pi * theta / 180
	)
	dist := math.Sin(r1)*math.Sin(r2) + math.Cos(r1)*math.Cos(r2)*math.Cos(rtheta)
	dist = min(1, dist)
	dist = math.Acos(dist)
	dist = dist * 180 / math.Pi
	dist = dist * 111.320
	return dist * 1000 // meters
}
