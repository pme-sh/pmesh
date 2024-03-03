package glob

import (
	"context"
	"regexp"

	"github.com/pme-sh/pmesh/glob/gocodewalker"
)

type File = gocodewalker.File
type Walker = gocodewalker.FileWalker

var bgCtx = context.Background()

func WalkContext(ctx context.Context, dir string, opts ...Option) <-chan *File {
	ch := make(chan *File, 128)
	walker := gocodewalker.NewFileWalker(dir, ch)
	walker.SetErrorHandler(func(err error) bool { return true })
	if ctx != bgCtx {
		context.AfterFunc(ctx, walker.Terminate)
	}
	for _, opt := range opts {
		opt(walker)
	}
	go walker.Start()
	return ch
}

func Walk(dir string, opts ...Option) <-chan *File {
	return WalkContext(bgCtx, dir, opts...)
}
func ForEachContext(ctx context.Context, dir string, cb func(*File) bool, opts ...Option) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for file := range WalkContext(ctx, dir, opts...) {
		if !cb(file) {
			return
		}
	}
}
func ForEach(dir string, cb func(*File) bool, opts ...Option) {
	ForEachContext(bgCtx, dir, cb, opts...)
}

func CollectContext(ctx context.Context, dir string, opts ...Option) []*File {
	files := make([]*File, 0, 128)
	ForEachContext(ctx, dir, func(file *File) bool {
		files = append(files, file)
		return true
	}, opts...)
	return files
}
func Collect(dir string, opts ...Option) []*File { return CollectContext(bgCtx, dir, opts...) }

type Option = func(*Walker)

func LocationExcludePattern(patterns ...string) Option {
	return func(walker *Walker) {
		walker.LocationExcludePattern = append(walker.LocationExcludePattern, patterns...)
	}
}
func IncludeDirectory(patterns ...string) Option {
	return func(walker *Walker) {
		walker.IncludeDirectory = append(walker.IncludeDirectory, patterns...)
	}
}
func ExcludeDirectory(patterns ...string) Option {
	return func(walker *Walker) {
		walker.ExcludeDirectory = append(walker.ExcludeDirectory, patterns...)
	}
}
func IncludeFilename(patterns ...string) Option {
	return func(walker *Walker) {
		walker.IncludeFilename = append(walker.IncludeFilename, patterns...)
	}
}
func ExcludeFilename(patterns ...string) Option {
	return func(walker *Walker) {
		walker.ExcludeFilename = append(walker.ExcludeFilename, patterns...)
	}
}
func IncludeDirectoryRegex(patterns ...string) Option {
	return func(walker *Walker) {
		for _, pattern := range patterns {
			rgx := regexp.MustCompile(pattern)
			walker.IncludeDirectoryRegex = append(walker.IncludeDirectoryRegex, rgx)
		}
	}
}
func ExcludeDirectoryRegex(patterns ...string) Option {
	return func(walker *Walker) {
		for _, pattern := range patterns {
			rgx := regexp.MustCompile(pattern)
			walker.ExcludeDirectoryRegex = append(walker.ExcludeDirectoryRegex, rgx)
		}
	}
}
func IncludeFilenameRegex(patterns ...string) Option {
	return func(walker *Walker) {
		for _, pattern := range patterns {
			rgx := regexp.MustCompile(pattern)
			walker.IncludeFilenameRegex = append(walker.IncludeFilenameRegex, rgx)
		}
	}
}
func ExcludeFilenameRegex(patterns ...string) Option {
	return func(walker *Walker) {
		for _, pattern := range patterns {
			rgx := regexp.MustCompile(pattern)
			walker.ExcludeFilenameRegex = append(walker.ExcludeFilenameRegex, rgx)
		}
	}
}
func AllowListExtensions(extensions ...string) Option {
	return func(walker *Walker) {
		walker.AllowListExtensions = append(walker.AllowListExtensions, extensions...)
	}
}
func ExcludeListExtensions(extensions ...string) Option {
	return func(walker *Walker) {
		walker.ExcludeListExtensions = append(walker.ExcludeListExtensions, extensions...)
	}
}
func IgnoreIgnoreFile() Option {
	return func(walker *Walker) {
		walker.IgnoreIgnoreFile = true
	}
}
func IgnoreGitIgnore() Option {
	return func(walker *Walker) {
		walker.IgnoreGitIgnore = true
	}
}
func IncludeHidden() Option {
	return func(walker *Walker) {
		walker.IncludeHidden = true
	}
}
func IgnoreArtifacts() Option {
	return func(walker *Walker) {
		walker.ExcludeDirectoryRegex = append(walker.ExcludeDirectoryRegex, regexp.MustCompile(`^\.build`))
		walker.ExcludeDirectoryRegex = append(walker.ExcludeDirectoryRegex, regexp.MustCompile(`^\.run`))
	}
}
