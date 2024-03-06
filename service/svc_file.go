package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"get.pme.sh/pmesh/vhttp"

	"github.com/andybalholm/brotli"
)

type FileService struct {
	Options
	Path             string         `yaml:"path,omitempty"`               // The path to serve
	NotFound         string         `yaml:"404,omitempty"`                // Static file for 404 errors
	Dynamic          bool           `yaml:"dynamic,omitempty"`            // If true, the fileserver will serve files directly from the filesystem instead of in-memory
	Immutable        bool           `yaml:"immutable,omitempty"`          // If true, the fileserver will assume the files are immutable and set cache headers accordingly
	NoImmutableMatch bool           `yaml:"no_immutable_match,omitempty"` // If true, the fileserver will not assume the files are immutable if the path contains /immutable/
	IndexFile        string         `yaml:"index,omitempty"`              // The index file to serve if the path is a directory
	Match            *regexp.Regexp `yaml:"match,omitempty"`              // The pattern to match
}

func (fsrv *FileService) UnmarshalInline(text string) error {
	fsrv.Path = text
	return nil
}

func init() {
	Registry.Define("FS", func() any { return &FileService{IndexFile: "/index.html"} })
}

// Implement service.Service
func (fsrv *FileService) String() string {
	return fmt.Sprintf("Fileserver{Path: %s}", fsrv.Path)
}
func (fsrv *FileService) Prepare(opt Options) error {
	fsrv.Options = opt
	if fsrv.Path == "" {
		fsrv.Path = filepath.Join(opt.ServiceRoot, opt.Name)
	} else if !filepath.IsAbs(fsrv.Path) {
		fsrv.Path = filepath.Join(opt.ServiceRoot, fsrv.Path)
	}
	return nil
}

type memoryFile struct {
	fs.FileInfo
	data     []byte
	brdata   []byte
	children []*memoryFile
}

type memoryFileHandle struct {
	*memoryFile
	bytes.Reader
}

func (f *memoryFileHandle) Close() error               { return nil }
func (f *memoryFileHandle) Stat() (os.FileInfo, error) { return f.memoryFile, nil }
func (f *memoryFileHandle) Readdir(count int) ([]os.FileInfo, error) {
	if count < 0 {
		count = len(f.children)
	} else if count > len(f.children) {
		count = len(f.children)
	}
	files := make([]os.FileInfo, count)
	for i, f := range f.children[:count] {
		files[i] = f
	}
	return files, nil
}

type memoryFileSystem struct {
	files   map[string]*memoryFile
	pattern *regexp.Regexp
}

func (mfs *memoryFileSystem) Open(name string) (http.File, error) {
	name = strings.TrimSuffix(name, "/")
	name = strings.TrimPrefix(name, "/")
	if name == "" {
		name = "."
	}
	name, br := strings.CutSuffix(name, "@br")
	if f, ok := mfs.files[name]; ok {
		if !f.Mode().IsRegular() {
			return nil, os.ErrNotExist
		}
		hnd := &memoryFileHandle{memoryFile: f}
		if br {
			if f.brdata == nil {
				return nil, os.ErrNotExist
			}
			hnd.Reset(f.brdata)
		} else {
			hnd.Reset(f.data)
		}
		return hnd, nil
	}
	return nil, os.ErrNotExist
}

// Do not compress already compressed files
var badcompressionExt = map[string]struct{}{
	".png":   {},
	".jpg":   {},
	".jpeg":  {},
	".gif":   {},
	".webp":  {},
	".ico":   {},
	".mp4":   {},
	".webm":  {},
	".ogg":   {},
	".mp3":   {},
	".wav":   {},
	".flac":  {},
	".aac":   {},
	".opus":  {},
	".pdf":   {},
	".zip":   {},
	".tar":   {},
	".gz":    {},
	".bz2":   {},
	".xz":    {},
	".woff":  {},
	".woff2": {},
	".ttf":   {},
	".otf":   {},
	".eot":   {},
}

func (mfs *memoryFileSystem) recursiveAdd(hfs fs.FS, path string, root *memoryFile, wg *sync.WaitGroup) error {
	file, err := hfs.Open(path)
	if err != nil {
		return err
	}
	stat, err := file.Stat()
	if err != nil {
		return err
	}
	if !stat.IsDir() && !stat.Mode().IsRegular() {
		return nil
	}

	npath := strings.ReplaceAll(path, "\\", "/")
	if !stat.IsDir() {
		if mfs.pattern != nil && !mfs.pattern.MatchString(npath) {
			return nil
		}
	}

	mf := &memoryFile{FileInfo: stat}
	mfs.files[npath] = mf
	if root != nil {
		root.children = append(root.children, mf)
	}
	if !stat.IsDir() {
		data := make([]byte, stat.Size())
		mf.data = data
		wg.Add(1)
		go func() {
			defer file.Close()
			defer wg.Done()
			io.ReadFull(file, data)

			if _, bad := badcompressionExt[filepath.Ext(npath)]; !bad {
				brwr := bytes.NewBuffer(nil)
				wr := brotli.NewWriterV2(brwr, 5)
				wr.Write(data)
				if wr.Close() != nil {
					return
				}
				compressionRatio := float64(brwr.Len()) / float64(len(data))
				if compressionRatio < 0.9 {
					mf.brdata = brwr.Bytes()
				}
			}
		}()
	} else {
		defer file.Close()
		fdir := file.(fs.ReadDirFile)
		children, err := fdir.ReadDir(-1)
		if err != nil {
			return err
		}
		if path == "." {
			path = ""
		} else {
			path += "/"
		}
		for _, child := range children {
			rel := path + child.Name()
			_ = mfs.recursiveAdd(hfs, rel, mf, wg)
		}
	}
	return nil
}
func NewMemoryFileSystem(path string, pattern *regexp.Regexp) (*memoryFileSystem, error) {
	mfs := &memoryFileSystem{
		files:   make(map[string]*memoryFile),
		pattern: pattern,
	}
	hfs := os.DirFS(path)
	wg := &sync.WaitGroup{}
	err := mfs.recursiveAdd(hfs, ".", &memoryFile{}, wg)
	wg.Wait()
	return mfs, err
}

type FileServer struct {
	*FileService
	filesystem http.FileSystem
}

func (fsrv *FileService) Start(c context.Context, invaliate bool) (Instance, error) {
	inst := &FileServer{FileService: fsrv}
	if stat, err := os.Stat(fsrv.Path); err != nil {
		return nil, err
	} else if !stat.IsDir() {
		return nil, fmt.Errorf("path %q is not a directory", fsrv.Path)
	}

	if fsrv.Dynamic {
		inst.filesystem = http.Dir(fsrv.Path)
	} else {
		fs, err := NewMemoryFileSystem(fsrv.Path, fsrv.Match)
		if err != nil {
			return nil, err
		}
		inst.filesystem = fs
	}
	return inst, nil
}

type failedFileWriter struct {
	http.ResponseWriter
	Status int
}

func (bw *failedFileWriter) WriteHeader(status int) {
	if status != http.StatusOK {
		bw.Status = status
	}
	h := bw.ResponseWriter.Header()
	delete(h, "Etag")
	delete(h, "Last-Modified")
	delete(h, "Cache-Control")
	bw.ResponseWriter.WriteHeader(bw.Status)
}
func (bw *failedFileWriter) Write(b []byte) (int, error) {
	return bw.ResponseWriter.Write(b)
}
func (bw *failedFileWriter) Unwrap() http.ResponseWriter {
	return bw.ResponseWriter
}

func (fsrv *FileServer) setContentType(w http.ResponseWriter, name string) {
	ctype := mime.TypeByExtension(filepath.Ext(name))
	if ctype != "" {
		w.Header()["Content-Type"] = []string{ctype}
	}
}
func (fsrv *FileServer) serveContent(w http.ResponseWriter, r *http.Request, name string, mod time.Time, content io.ReadSeeker, status int) {
	// If the request is not a GET or HEAD, force it to be treated as a GET
	if method := r.Method; method != http.MethodGet && method != http.MethodHead {
		r.Method = http.MethodGet
	}

	if status == http.StatusOK {
		// If fileserver is immutable, take advantage of the SendFile path immediately and set cache headers
		immutable := fsrv.Immutable
		if immutable && !fsrv.NoImmutableMatch {
			immutable = strings.Contains(name, "/immutable/")
		}
		if immutable {
			w.Header()["Cache-Control"] = []string{"public, max-age=31536000, immutable"}
			if len(r.Header["Range"]) == 0 {
				fsrv.setContentType(w, name)
				w.WriteHeader(status)
				io.Copy(w, content)
				return
			}
		}

		// Yield to ServeContent
		http.ServeContent(w, r, name, mod, content)
		return
	}

	delete(r.Header, "If-Match")
	delete(r.Header, "If-None-Match")
	delete(r.Header, "If-Modified-Since")

	// If status is not 200, wrap it around a fake writer to enforce status
	bw := &failedFileWriter{ResponseWriter: w, Status: status}
	http.ServeContent(bw, r, name, mod, content)
}

// Implement service.Instance
func (fsrv *FileServer) internalError(w http.ResponseWriter, r *http.Request, path string, err error) vhttp.Result {
	// If there was an internal error, fail
	fsrv.Logger.Warn().Err(err).Str("path", path).Msg("error opening file")
	vhttp.Error(w, r, http.StatusInternalServerError)
	return vhttp.Done
}
func (fsrv *FileServer) notFound(w http.ResponseWriter, r *http.Request) vhttp.Result {
	if fsrv.NotFound == "" {
		return vhttp.Continue
	}
	if fsrv.NotFound == "default" {
		vhttp.Error(w, r, http.StatusNotFound)
		return vhttp.Done
	}

	file404, err404 := fsrv.filesystem.Open(fsrv.NotFound)
	if err404 != nil {
		return fsrv.internalError(w, r, fsrv.NotFound, err404)
	}
	defer file404.Close()

	modify := time.Time{}
	if stat, e := file404.Stat(); e == nil {
		modify = stat.ModTime()
	}
	fsrv.serveContent(w, r, fsrv.NotFound, modify, file404, http.StatusNotFound)
	return vhttp.Done
}
func (fsrv *FileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) vhttp.Result {
	// Clean up the path
	filePath := r.URL.Path
	if filePath == "/" {
		if fsrv.IndexFile == "" {
			return fsrv.notFound(w, r)
		}
		filePath = fsrv.IndexFile
	}

	var stat fs.FileInfo
	var file http.File
	var err error
	for i := 0; i < 2; i++ {
		// If it doesn't match the pattern, continue or 404
		if fsrv.Match != nil && !fsrv.Match.MatchString(filePath) {
			err = fs.ErrNotExist
			break
		}

		// Open the file
		file, err = fsrv.filesystem.Open(filePath)
		if err != nil {
			break
		}
		defer file.Close()

		// Get the file info, break if it's not a directory
		stat, err = file.Stat()
		if err != nil {
			break
		}
		if !stat.IsDir() {
			break
		}
		err = fs.ErrNotExist

		// Try to open the index file
		if i == 1 || fsrv.IndexFile == "" {
			break
		}
		filePath += fsrv.IndexFile
	}
	if err != nil {
		if os.IsNotExist(err) {
			return fsrv.notFound(w, r)
		}
		return fsrv.internalError(w, r, filePath, err)
	}

	if len(r.Header["Range"]) == 0 {
		accpt := r.Header.Get("Accept-Encoding")
		if strings.Contains(accpt, "br") {
			if cfile, e := fsrv.filesystem.Open(filePath + "@br"); e == nil {
				defer cfile.Close()
				file.Close()
				file = cfile
				w.Header()["Content-Encoding"] = []string{"br"}
			}
		}
	}

	// Serve the file
	fsrv.serveContent(w, r, filePath, stat.ModTime(), file, http.StatusOK)
	return vhttp.Done
}
func (fsrv *FileServer) Stop(context.Context) {}
