package proxy

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/fishingpvalues/airrbag/internal/arr"
)

// Target is what a DELETE request would remove from disk.
type Target struct {
	FileIDs   []int
	ParentIDs []int
	// Sub is a narrower file query: Lidarr albums (albumId) and Readarr
	// books (bookId).
	SubParam string
	SubIDs   []int
}

// Empty reports whether the request deletes no files.
func (t Target) Empty() bool {
	return len(t.FileIDs) == 0 && len(t.ParentIDs) == 0 && len(t.SubIDs) == 0
}

var apiPathRE = regexp.MustCompile(`/api/v[0-9]+(/.*)$`)

// subParents are child entities whose delete can take files with it.
var subParents = map[string]string{"album": "albumId", "book": "bookId"}

func truthy(v string) bool {
	b, err := strconv.ParseBool(v)
	return err == nil && b
}

// ParseDelete maps a DELETE request onto the files it would remove. ok is
// false for DELETEs that never touch files on disk (tags, queue items,
// download clients, a movie removed without deleteFiles, ...).
func ParseDelete(shape arr.Shape, urlPath string, q url.Values, body []byte) (Target, bool) {
	m := apiPathRE.FindStringSubmatch(urlPath)
	if m == nil {
		return Target{}, false
	}
	parts := strings.Split(strings.Trim(m[1], "/"), "/")
	if len(parts) == 0 {
		return Target{}, false
	}
	res := strings.ToLower(parts[0])
	deleteFiles := truthy(q.Get("deleteFiles")) || truthy(q.Get("deletefiles"))
	var t Target
	switch res {
	case shape.FileRes:
		t = fileTarget(shape, parts, body)
	case shape.Parent:
		t = parentTarget(shape, parts, body, deleteFiles)
	default:
		if param, ok := subParents[res]; ok && len(parts) == 2 && deleteFiles {
			if id, err := strconv.Atoi(parts[1]); err == nil {
				t.SubParam, t.SubIDs = param, []int{id}
			}
		}
	}
	return t, !t.Empty()
}

func fileTarget(shape arr.Shape, parts []string, body []byte) Target {
	if len(parts) != 2 {
		return Target{}
	}
	if parts[1] == "bulk" {
		return Target{FileIDs: idsFromBody(body, shape.BulkFileKey)}
	}
	if id, err := strconv.Atoi(parts[1]); err == nil {
		return Target{FileIDs: []int{id}}
	}
	return Target{}
}

func parentTarget(shape arr.Shape, parts []string, body []byte, deleteFiles bool) Target {
	if len(parts) != 2 {
		return Target{}
	}
	if parts[1] == "editor" {
		if bodyBool(body, "deleteFiles") {
			return Target{ParentIDs: idsFromBody(body, shape.EditorIDsKey)}
		}
		return Target{}
	}
	if !deleteFiles {
		return Target{}
	}
	if id, err := strconv.Atoi(parts[1]); err == nil {
		return Target{ParentIDs: []int{id}}
	}
	return Target{}
}

func idsFromBody(body []byte, key string) []int {
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil {
		return nil
	}
	for k, v := range m {
		if strings.EqualFold(k, key) {
			var ids []int
			if json.Unmarshal(v, &ids) == nil {
				return ids
			}
		}
	}
	return nil
}

func bodyBool(body []byte, key string) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil {
		return false
	}
	for k, v := range m {
		if strings.EqualFold(k, key) {
			var b bool
			return json.Unmarshal(v, &b) == nil && b
		}
	}
	return false
}
