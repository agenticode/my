package collect

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// A collection is written the way the DMTF mockup tools lay out a service:
// the answer for a URI is the file index.json in the directory of that
// URI, so /redfish/v1/Systems/1 is redfish/v1/Systems/1/index.json. Such a
// tree is read by "my import" and can be served again by a mockup server.
//
// The names come from the BMC. They are checked before they are used, and
// every file is written through an os.Root, which the operating system
// keeps from leaving the directory of the collection.

const (
	indexName = "index.json"
	// maxSegment and maxDepth bound a path made from a URI.
	maxSegment = 200
	maxDepth   = 32
)

// tree writes the files of one collection.
type tree struct {
	root *os.Root
	// taken maps a file name in lower case to the URI it was written for:
	// on a file system that ignores case two URIs can ask for one file.
	taken map[string]string
}

func openTree(dir string) (*tree, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &tree{root: root, taken: map[string]string{}}, nil
}

func (t *tree) close() error { return t.root.Close() }

// filePath returns where the answer for a URI is written, relative to the
// collection.
func filePath(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", errors.New("not a URI")
	}
	escaped := strings.Trim(u.EscapedPath(), "/")
	if escaped == "" {
		return "", errors.New("the URI has no path")
	}
	segs := strings.Split(escaped, "/")
	if len(segs) > maxDepth {
		return "", fmt.Errorf("the path has more than %d parts", maxDepth)
	}
	for _, seg := range segs {
		un, err := url.PathUnescape(seg)
		switch {
		case err != nil, seg == "", un == ".", un == "..":
			return "", errors.New("the path has a part that is no name")
		case len(seg) > maxSegment:
			return "", fmt.Errorf("a part of the path is longer than %d bytes", maxSegment)
		case strings.ContainsAny(seg, "\\:*?\"<>|\x00") || strings.ContainsAny(un, "/\\\x00"):
			return "", errors.New("the path has a character that cannot be part of a file name")
		}
		for i := 0; i < len(seg); i++ {
			if seg[i] < 0x20 || seg[i] == 0x7f {
				return "", errors.New("the path has a control character")
			}
		}
	}
	return strings.Join(segs, "/") + "/" + indexName, nil
}

// write stores the answer for a URI and returns the name of the file.
func (t *tree) write(uri string, data []byte) (string, error) {
	name, err := filePath(uri)
	if err != nil {
		return "", err
	}
	key := strings.ToLower(name)
	if other, ok := t.taken[key]; ok && other != uri {
		return "", fmt.Errorf("the file name is taken by %s", other)
	}
	dir := strings.TrimSuffix(name, "/"+indexName)
	if err := t.root.MkdirAll(dir, 0o777); err != nil {
		return "", err
	}
	if err := t.root.WriteFile(name, data, 0o666); err != nil {
		return "", err
	}
	t.taken[key] = uri
	return name, nil
}

// writeTop stores a file at the top of the collection.
func (t *tree) writeTop(name string, data []byte) error {
	return t.root.WriteFile(name, data, 0o666)
}
