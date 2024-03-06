package glob

import (
	"context"
	"crypto/sha1"
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

type Checksum [hashSize]byte

func (d Checksum) Slice() []byte {
	return d[:]
}
func (d Checksum) String() string {
	return hex.EncodeToString(d[:])
}

func hashAppend(h hash.Hash, file *File) {
	h.Write([]byte(file.Location))
	f, err := os.Open(file.Location)
	if err == nil {
		defer f.Close()
		io.Copy(h, f)
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
	h := hasher()
	for _, loc := range l.StableList[i:] {
		if !strings.HasPrefix(loc, prefix) {
			break
		}
		h.Write(l.HashMap[loc].Slice())
	}
	return Checksum(h.Sum(nil))
}
func (l *HashList) All() Checksum {
	h := hasher()
	for _, hash := range l.HashMap {
		h.Write(hash.Slice())
	}
	return Checksum(h.Sum(nil))
}

func ReduceToHash(ch <-chan *File) (l *HashList) {
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
			hashAppend(h, file)
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

func HashContext(ctx context.Context, dir string, opts ...Option) *HashList {
	return ReduceToHash(WalkContext(ctx, dir, opts...))
}
func Hash(dir string, opts ...Option) *HashList {
	return HashContext(bgCtx, dir, opts...)
}
