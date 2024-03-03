package netx

import "sort"

type ip6info[Info any] struct {
	beg, end IP
	info     *Info
}

type ip4info[Info any] struct {
	beg, end uint32
	info     *Info
}

type IPMap[Info any] struct {
	v4 []ip4info[Info]
	v6 []ip6info[Info]
}

func (s *IPMap[Info]) Sort() {
	sort.Slice(s.v4, func(i, j int) bool {
		return s.v4[i].beg < s.v4[j].beg
	})
	sort.Slice(s.v6, func(i, j int) bool {
		return s.v6[i].beg.Compare(s.v6[j].beg) < 0
	})
}

func (s *IPMap[Info]) Add(info *Info, min IP, max IP) {
	if min.IsV4() {
		imin := uint32(min.Low)
		imax := uint32(max.Low)
		wrapped := ip4info[Info]{imin, imax, info}
		s.v4 = append(s.v4, wrapped)
	} else {
		wrapped := ip6info[Info]{min, max, info}
		s.v6 = append(s.v6, wrapped)
	}
}

func (i2 *IPMap[Info]) Find(ip IP) (v *Info) {
	if ip.IsV4() {
		ip := uint32(ip.Low)
		arr := i2.v4
		i, j := 0, len(arr)
		for i < j {
			h := int(uint(i+j) >> 1)
			if r := arr[h].end; r < ip {
				i = h + 1
			} else if r != 0 && arr[h].beg > ip {
				j = h
			} else {
				v = arr[h].info
				return
			}
		}
	} else {
		arr := i2.v6
		i, j := 0, len(arr)
		for i < j {
			h := int(uint(i+j) >> 1)
			if r := arr[h].end.Compare(ip); r < 0 {
				i = h + 1
			} else if r != 0 && arr[h].beg.Compare(ip) > 0 {
				j = h
			} else {
				v = arr[h].info
				return
			}
		}
	}
	return
}
