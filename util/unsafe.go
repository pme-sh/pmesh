package util

import "unsafe"

func UnsafeBuffer(s string) []byte {
	b := unsafe.StringData(s)
	return unsafe.Slice(b, len(s))
}
func UnsafeString(b []byte) string {
	return unsafe.String(unsafe.SliceData(b), len(b))
}
