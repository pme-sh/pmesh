package glob

import (
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

const hashSize = sha1.Size

var tbl = crc32.MakeTable(crc32.Castagnoli)

func hasher() hash.Hash {
	if hashSize == crc32.Size {
		return crc32.New(tbl)
	} else {
		return sha1.New()
	}
}
func checksum(data []byte) (r Checksum) {
	if hashSize == crc32.Size {
		u32 := crc32.Checksum(data, tbl)
		binary.LittleEndian.PutUint32(r[:], u32)
	} else {
		h := sha1.Sum(data)
		copy(r[:], h[:])
	}
	return
}

type Checksum [hashSize]byte

func (d Checksum) Slice() []byte {
	return d[:]
}
func (d Checksum) String() string {
	return hex.EncodeToString(d[:])
}

type HashKind uint8

const (
	HashContent HashKind = iota
	HashStat    HashKind = iota
)

func hashAppend(h hash.Hash, file *File, kind HashKind) {
	stat, err := os.Stat(file.Location)
	h.Write([]byte(file.Location))
	if err == nil {
		u1 := uint64(stat.Size())
		u2 := uint64(stat.Mode())
		u2 <<= 32
		u2 ^= uint64(stat.ModTime().UnixNano())

		buf := [16]byte{}
		binary.LittleEndian.PutUint64(buf[:8], u1)
		binary.LittleEndian.PutUint64(buf[8:], u2)
		h.Write(buf[:])

		if kind == HashContent {
			f, err := os.Open(file.Location)
			if err == nil {
				defer f.Close()
				io.Copy(h, f)
			}
		}
	}
}
func normalLoc(location string) string {
	location, _ = filepath.Abs(location)
	location = strings.ToLower(location)
	return strings.ReplaceAll(location, "\\", "/")
}

type HashList struct {
	mu         sync.Mutex
	HashMap    map[string]Checksum
	StableList []string
}

func (l *HashList) File(location string) (Checksum, bool) {
	hash, ok := l.HashMap[location]
	return hash, ok
}
func (l *HashList) Dir(prefix string) Checksum {
	prefix = normalLoc(prefix)
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	i, _ := slices.BinarySearch(l.StableList, prefix)
	subist := l.StableList[i:]

	buf := make([]byte, hashSize*len(subist))
	written := 0
	for _, loc := range l.StableList[i:] {
		if !strings.HasPrefix(loc, prefix) {
			break
		}
		h := [hashSize]byte(l.HashMap[loc])
		copy(buf[written:], h[:])
		written += hashSize
	}
	return checksum(buf[:written])
}

func ReduceToHash(ch <-chan *File, kind HashKind) (l *HashList) {
	l = &HashList{
		HashMap: make(map[string]Checksum),
	}

	wg := sync.WaitGroup{}
	for file := range ch {
		wg.Add(1)
		go func(file *File) {
			defer wg.Done()
			var dig Checksum
			h := hasher()
			hashAppend(h, file, kind)
			copy(dig[:], h.Sum(nil))
			loc := normalLoc(file.Location)

			l.mu.Lock()
			l.HashMap[loc] = dig
			l.StableList = append(l.StableList, loc)
			l.mu.Unlock()
		}(file)
	}
	wg.Wait()

	slices.Sort(l.StableList)
	return
}

func HashContext(ctx context.Context, dir string, kind HashKind, opts ...Option) *HashList {
	return ReduceToHash(WalkContext(ctx, dir, opts...), kind)
}
func Hash(dir string, kind HashKind, opts ...Option) *HashList {
	return HashContext(bgCtx, dir, kind, opts...)
}
