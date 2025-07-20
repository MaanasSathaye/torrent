package metainfo

import (
	"strings"
)

type File struct {
	Path   string
	Offset uint64
	Length uint64
}

// FileInfo information specific to a single file inside the MetaInfo structure.
type FileInfo struct {
	Length   int64    `bencode:"length"`
	Path     []string `bencode:"path"`
	PathUTF8 []string `bencode:"path.utf-8,omitempty"`
}

// DisplayPath ...
func (fi *FileInfo) DisplayPath(info *Info) string {
	if info.IsDir() {
		return strings.Join(fi.Path, "/")
	}

	return info.Name
}

// Offset ...
func (fi FileInfo) Offset(info *Info) (ret int64) {
	match := fi.DisplayPath(info)
	for _, c := range info.UpvertedFiles() {
		if match == c.DisplayPath(info) {
			return
		}
		ret += c.Length
	}
	panic("not found")
}
