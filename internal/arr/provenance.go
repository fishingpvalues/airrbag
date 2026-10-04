package arr

import (
	"regexp"
	"strings"
	"time"
)

// Provenance is where one library file came from.
type Provenance struct {
	Protocol   string    `json:"protocol"` // usenet, torrent, unknown
	Indexer    string    `json:"indexer,omitempty"`
	Client     string    `json:"client,omitempty"`
	DownloadID string    `json:"downloadId,omitempty"`
	InfoHash   string    `json:"infoHash,omitempty"`
	GrabbedAt  time.Time `json:"grabbedAt,omitempty"`
	ImportedAt time.Time `json:"importedAt,omitempty"`
	// SourceTitle is the release name of the import (or grab).
	SourceTitle string `json:"sourceTitle,omitempty"`
	// DroppedPath is where the *Arr imported the file from (its own view).
	DroppedPath string `json:"droppedPath,omitempty"`
	// Recorded is true when history has an import event for the file.
	Recorded bool `json:"recorded"`
}

var infoHashRE = regexp.MustCompile(`^[0-9a-fA-F]{40}$|^[0-9a-fA-F]{64}$`)

// NormalizeProtocol maps every dialect the apps use to usenet/torrent/unknown.
// Sonarr and Radarr write the enum value ("1" usenet, "2" torrent), Lidarr
// writes the type name ("UsenetDownloadProtocol"), newer builds the word.
func NormalizeProtocol(p, downloadID string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "1", "usenet", "usenetdownloadprotocol":
		return "usenet"
	case "2", "torrent", "torrentdownloadprotocol":
		return "torrent"
	}
	switch {
	case infoHashRE.MatchString(downloadID):
		return "torrent"
	case strings.HasPrefix(downloadID, "SABnzbd_nzo_"), strings.HasPrefix(downloadID, "NZBGet"):
		return "usenet"
	}
	return "unknown"
}

// Index maps file ids to provenance, built from history.
type Index struct {
	byFile map[int]Provenance
}

// NewIndex builds an Index from history records (any order). A file id that
// was imported more than once keeps its latest import.
func NewIndex(records []HistoryRecord) *Index {
	grabs := map[string]HistoryRecord{}
	type imp struct {
		rec HistoryRecord
	}
	imports := map[int]imp{}
	for _, r := range records {
		et := strings.ToLower(r.EventType)
		switch {
		case et == "grabbed":
			if r.DownloadID == "" {
				continue
			}
			key := strings.ToUpper(r.DownloadID)
			if prev, ok := grabs[key]; !ok || r.Date.After(prev.Date) {
				grabs[key] = r
			}
		case strings.HasSuffix(et, "imported") && et != "downloadimported":
			id := intOf(r.Data["fileId"])
			if id == 0 {
				continue
			}
			if prev, ok := imports[id]; !ok || r.Date.After(prev.rec.Date) {
				imports[id] = imp{rec: r}
			}
		}
	}
	idx := &Index{byFile: make(map[int]Provenance, len(imports))}
	for id, im := range imports {
		r := im.rec
		p := Provenance{DownloadID: r.DownloadID, ImportedAt: r.Date, SourceTitle: r.SourceTitle,
			DroppedPath: r.D("droppedPath"), Recorded: true}
		p.Client = r.D("downloadClientName")
		if p.Client == "" {
			p.Client = r.D("downloadClient")
		}
		proto := ""
		if g, ok := grabs[strings.ToUpper(r.DownloadID)]; ok && r.DownloadID != "" {
			proto = p.applyGrab(g)
		}
		p.Protocol = NormalizeProtocol(proto, r.DownloadID)
		if p.InfoHash == "" && p.Protocol == "torrent" && infoHashRE.MatchString(r.DownloadID) {
			p.InfoHash = r.DownloadID
		}
		p.InfoHash = strings.ToLower(p.InfoHash)
		idx.byFile[id] = p
	}
	return idx
}

// Get returns the provenance of a file id.
func (i *Index) Get(fileID int) (Provenance, bool) {
	if i == nil {
		return Provenance{}, false
	}
	p, ok := i.byFile[fileID]
	return p, ok
}

// Len is the number of indexed files.
func (i *Index) Len() int {
	if i == nil {
		return 0
	}
	return len(i.byFile)
}

// Merge adds entries from other that i does not have.
func (i *Index) Merge(other *Index) {
	if other == nil {
		return
	}
	for k, v := range other.byFile {
		if _, ok := i.byFile[k]; !ok {
			i.byFile[k] = v
		}
	}
}

// applyGrab copies what the matching grab event knows into p and returns
// the protocol the grab recorded.
func (p *Provenance) applyGrab(g HistoryRecord) string {
	p.Indexer = g.D("indexer")
	p.GrabbedAt = g.Date
	if p.SourceTitle == "" {
		p.SourceTitle = g.SourceTitle
	}
	p.InfoHash = g.D("torrentInfoHash")
	if c := g.D("downloadClientName"); c != "" {
		p.Client = c
	} else if p.Client == "" {
		p.Client = g.D("downloadClient")
	}
	return g.D("protocol")
}
