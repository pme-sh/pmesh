package netx

import (
	"bytes"
	"context"
	"encoding"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"get.pme.sh/pmesh/xlog"

	atomicfile "github.com/natefinch/atomic"
)

const remoteRecheckInterval = 4 * time.Hour

type RemoteFile struct {
	uri      string
	filepath string
	lastRead time.Time
}

func NewRemoteFile(uri string, filePath string) *RemoteFile {
	rf := &RemoteFile{uri, filePath, time.Time{}}
	if stat, err := os.Stat(filePath); err == nil && stat.Size() > 0 {
		rf.lastRead = stat.ModTime()
	}
	return rf
}

func (r *RemoteFile) loadIfChanged() (data []byte, changed bool, err error) {
	// If we've checked the file recently, don't check it again.
	if time.Since(r.lastRead) < remoteRecheckInterval {
		return nil, false, nil
	}

	req, err := http.NewRequest("GET", r.uri, nil)
	if err != nil {
		return nil, false, err
	}
	if !r.lastRead.IsZero() {
		req.Header["If-Modified-Since"] = []string{r.lastRead.Format(http.TimeFormat)}
	}

	// Fetch the file.
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer res.Body.Close()

	reader := res.Body
	if res.StatusCode == http.StatusNotModified || (res.StatusCode == http.StatusTooManyRequests && !r.lastRead.IsZero()) {
		// If the file hasn't changed, use local file
		r.lastRead = time.Now()
		os.Chtimes(r.filepath, r.lastRead, r.lastRead)
		return nil, false, nil
	} else if res.StatusCode != http.StatusOK {
		// If the request failed, return the error.
		return nil, false, fmt.Errorf("fetch failed: %s", res.Status)
	}

	// If the fetch was successful, write the file.
	data, err = io.ReadAll(reader)
	if err == nil {
		atomicfile.WriteFile(r.filepath, bytes.NewReader(data))
		r.lastRead = time.Now()
	}
	return data, true, err
}
func (r *RemoteFile) Load(ifchanged bool) ([]byte, error) {
	data, changed, err := r.loadIfChanged()
	if ifchanged && !changed {
		return nil, err
	}
	if err != nil || !changed {
		if filedata, fileerr := os.ReadFile(r.filepath); fileerr == nil {
			// we're consuming the error here, but we want to log it
			if err != nil {
				xlog.Warn().Err(err).Str("uri", r.uri).Msg("failed to fetch remote file")
			}
			return filedata, nil
		}
		return nil, err
	}
	return data, err
}
func (r *RemoteFile) Invalidate() {
	os.Remove(r.filepath)
	r.lastRead = time.Time{}
}

type remoteParsedFile[T any] struct {
	data atomic.Pointer[T]
	next atomic.Int64
	sema chan struct{}
	rf   *RemoteFile
}

func newRemoteParsedFile[T any](uri string, filePath string) *remoteParsedFile[T] {
	return &remoteParsedFile[T]{
		sema: make(chan struct{}, 1),
		rf:   NewRemoteFile(uri, filePath),
	}
}

func (p *remoteParsedFile[T]) LoadContext(ctx context.Context) (ptr *T, err error) {
	// Lock-free fast path
	ptr = p.data.Load()
	if ptr != nil && time.Now().Unix() < p.next.Load() {
		return
	}

	// If there is cached data, do not make the requester wait
	if ptr != nil {
		select {
		case p.sema <- struct{}{}:
		default:
			return ptr, nil
		}
	} else {
		// Lock the semaphore
		select {
		case p.sema <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Recheck the fast path
	ptr = p.data.Load()
	if ptr != nil && time.Now().Unix() < p.next.Load() {
		<-p.sema
		return
	}

	// Load the data in another goroutine, even if this one cancels, we want to keep the data
	var done = make(chan error, 1)
	go func(isLazy bool) {
		var data []byte
		for attempt := 0; attempt < 2; attempt++ {
			data, err = p.rf.Load(isLazy)
			if err != nil || (len(data) == 0 && isLazy) {
				break
			}
			newResult := new(T)
			if bin, ok := any(newResult).(encoding.BinaryUnmarshaler); ok {
				if err = bin.UnmarshalBinary(data); err != nil {
					p.rf.Invalidate()
					continue
				}
			} else if bin, ok := any(newResult).(encoding.TextUnmarshaler); ok {
				if err = bin.UnmarshalText(data); err != nil {
					p.rf.Invalidate()
					continue
				}
			} else if err = json.Unmarshal(data, newResult); err != nil {
				p.rf.Invalidate()
				continue
			}

			p.data.Store(newResult)
			break
		}

		// Set the error, update timestamp, release the semaphore
		if err == nil {
			p.next.Store(time.Now().Add(remoteRecheckInterval).Unix())
		}
		done <- err
		<-p.sema
		close(done)
	}(ptr != nil)

	// If there is cached data, do not make the requester wait
	if ptr == nil {
		// Wait for the result
		select {
		case <-ctx.Done():
			return ptr, nil
		case err = <-done:
			ptr = p.data.Load()
		}
	}
	return
}
func (p *remoteParsedFile[T]) Load() (ptr *T, err error) {
	return p.LoadContext(context.Background())
}
