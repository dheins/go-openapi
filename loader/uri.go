package loader

import (
	"net/url"
	"path/filepath"
	"strings"
)

// fileURL returns the absolute file: URL of a local path.
func fileURL(path string) (*url.URL, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		// Windows drive paths become file:///C:/dir/file.
		p = "/" + p
	}
	return &url.URL{Scheme: "file", Path: p}, nil
}

// filePath converts a file: URL back to a local path.
func filePath(u *url.URL) string {
	p := u.Path
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

// displayURI renders a URI for messages: local files as paths, relative to
// the working directory when that is shorter.
func displayURI(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return uri
	}
	p := filePath(u)
	if wd, err := filepath.Abs("."); err == nil {
		if rel, err := filepath.Rel(wd, p); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return p
}

// withoutFragment returns a copy of u with the fragment removed.
func withoutFragment(u *url.URL) *url.URL {
	c := *u
	c.Fragment = ""
	c.RawFragment = ""
	return &c
}

// dirURI returns the URI of the "directory" containing u: everything up to
// and including the last slash.
func dirURI(u string) string {
	if i := strings.LastIndexByte(u, '/'); i >= 0 {
		return u[:i+1]
	}
	return u
}
