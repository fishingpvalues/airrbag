// Package arr is a small read-only client for the Servarr family (Sonarr,
// Radarr, Lidarr, Readarr, Whisparr). The apps share one codebase lineage,
// so one client covers them through a per-app Shape table.
package arr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Shape describes where an app keeps its parents, files and history.
type Shape struct {
	App          string // sonarr, radarr, lidarr, readarr, whisparr
	API          string // "v3" or "v1"
	Parent       string // movie, series, artist, author
	ParentParam  string // movieId, seriesId, artistId, authorId
	FileRes      string // moviefile, episodefile, trackfile, bookfile
	BulkFileKey  string // movieFileIds, episodeFileIds, trackFileIds, bookFileIds
	EditorIDsKey string // movieIds, seriesIds, artistIds, authorIds
	SlugField    string // JSON field the UI route uses for a parent
}

var (
	radarrShape  = Shape{App: "radarr", API: "v3", Parent: "movie", ParentParam: "movieId", FileRes: "moviefile", BulkFileKey: "movieFileIds", EditorIDsKey: "movieIds", SlugField: "titleSlug"}
	sonarrShape  = Shape{App: "sonarr", API: "v3", Parent: "series", ParentParam: "seriesId", FileRes: "episodefile", BulkFileKey: "episodeFileIds", EditorIDsKey: "seriesIds", SlugField: "titleSlug"}
	lidarrShape  = Shape{App: "lidarr", API: "v1", Parent: "artist", ParentParam: "artistId", FileRes: "trackfile", BulkFileKey: "trackFileIds", EditorIDsKey: "artistIds", SlugField: "foreignArtistId"}
	readarrShape = Shape{App: "readarr", API: "v1", Parent: "author", ParentParam: "authorId", FileRes: "bookfile", BulkFileKey: "bookFileIds", EditorIDsKey: "authorIds", SlugField: "titleSlug"}
)

// ShapeFor returns the shape of an app at a given major version. Whisparr 2
// is Sonarr-based (series), Whisparr 3 is Radarr-based (movies).
func ShapeFor(app string, major int) (Shape, error) {
	switch strings.ToLower(app) {
	case "radarr":
		return radarrShape, nil
	case "sonarr":
		return sonarrShape, nil
	case "lidarr":
		return lidarrShape, nil
	case "readarr":
		return readarrShape, nil
	case "whisparr":
		s := sonarrShape
		if major >= 3 {
			s = radarrShape
		}
		s.App = "whisparr"
		return s, nil
	}
	return Shape{}, fmt.Errorf("unsupported app %q", app)
}

// Status is /system/status.
type Status struct {
	AppName string `json:"appName"`
	Version string `json:"version"`
	URLBase string `json:"urlBase"`
}

// Major returns the major version number, 0 if unparsable.
func (s Status) Major() int {
	n, _ := strconv.Atoi(strings.SplitN(s.Version, ".", 2)[0])
	return n
}

// Client talks to one *Arr instance with its API key.
type Client struct {
	base   string
	apiKey string
	http   *http.Client
	Shape  Shape
	Status Status
}

// New creates a client. Call Detect before use.
func New(base, apiKey string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{base: strings.TrimRight(base, "/"), apiKey: apiKey, http: hc}
}

// Base returns the upstream base URL.
func (c *Client) Base() string { return c.base }

// ErrHTTP is an unexpected status from the *Arr.
type ErrHTTP struct {
	Status int
	Path   string
}

func (e *ErrHTTP) Error() string { return fmt.Sprintf("%s: HTTP %d", e.Path, e.Status) }

func (c *Client) get(ctx context.Context, api, path string, q url.Values, out any) error {
	u := c.base + "/api/" + api + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return &ErrHTTP{Status: resp.StatusCode, Path: "/api/" + api + path}
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 256<<20)).Decode(out)
}

// Detect reads /system/status (v3 first, then v1) and picks the shape.
// hint is the configured app name or "auto".
func (c *Client) Detect(ctx context.Context, hint string) error {
	var st Status
	var api string
	for _, v := range []string{"v3", "v1"} {
		if err := c.get(ctx, v, "/system/status", nil, &st); err == nil && st.AppName != "" {
			api = v
			break
		}
	}
	if api == "" {
		return errors.New("could not read /system/status on api v3 or v1")
	}
	app := strings.ToLower(st.AppName)
	if hint != "" && hint != "auto" {
		app = strings.ToLower(hint)
	}
	shape, err := ShapeFor(app, st.Major())
	if err != nil {
		return err
	}
	if shape.API != api && strings.ToLower(st.AppName) == shape.App {
		shape.API = api
	}
	c.Shape = shape
	c.Status = st
	return nil
}

// File is one library file.
type File struct {
	ID           int    `json:"id"`
	ParentID     int    `json:"-"`
	Path         string `json:"path"`
	RelativePath string `json:"relativePath"`
	Size         int64  `json:"size"`
	// SceneName is the release name the file was imported as (Sonarr,
	// Radarr), OriginalFilePath its path inside the download folder.
	SceneName        string `json:"sceneName,omitempty"`
	OriginalFilePath string `json:"originalFilePath,omitempty"`
}

// rawFile decodes any of the per-app file shapes.
type rawFile struct {
	ID           int    `json:"id"`
	Path         string `json:"path"`
	RelativePath string `json:"relativePath"`
	Size         int64  `json:"size"`
	SceneName    string `json:"sceneName"`
	OriginalPath string `json:"originalFilePath"`
	MovieID      int    `json:"movieId"`
	SeriesID     int    `json:"seriesId"`
	ArtistID     int    `json:"artistId"`
	AuthorID     int    `json:"authorId"`
}

func (r rawFile) file() File {
	pid := r.MovieID
	for _, v := range []int{r.SeriesID, r.ArtistID, r.AuthorID} {
		if pid == 0 {
			pid = v
		}
	}
	return File{ID: r.ID, ParentID: pid, Path: r.Path, RelativePath: r.RelativePath, Size: r.Size,
		SceneName: r.SceneName, OriginalFilePath: r.OriginalPath}
}

// Files lists the files of one parent (movie, series, artist, author).
func (c *Client) Files(ctx context.Context, parentID int) ([]File, error) {
	var raw []rawFile
	q := url.Values{c.Shape.ParentParam: {strconv.Itoa(parentID)}}
	if err := c.get(ctx, c.Shape.API, "/"+c.Shape.FileRes, q, &raw); err != nil {
		return nil, err
	}
	out := make([]File, 0, len(raw))
	for _, r := range raw {
		f := r.file()
		if f.ParentID == 0 {
			f.ParentID = parentID
		}
		out = append(out, f)
	}
	return out, nil
}

// FilesBy lists files filtered by another key, e.g. albumId (Lidarr) or
// bookId (Readarr).
func (c *Client) FilesBy(ctx context.Context, param string, id int) ([]File, error) {
	var raw []rawFile
	if err := c.get(ctx, c.Shape.API, "/"+c.Shape.FileRes, url.Values{param: {strconv.Itoa(id)}}, &raw); err != nil {
		return nil, err
	}
	out := make([]File, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.file())
	}
	return out, nil
}

// File returns one file by id (its parent id included).
func (c *Client) File(ctx context.Context, id int) (File, error) {
	var r rawFile
	if err := c.get(ctx, c.Shape.API, "/"+c.Shape.FileRes+"/"+strconv.Itoa(id), nil, &r); err != nil {
		return File{}, err
	}
	return r.file(), nil
}

// AlbumByForeignID finds a Lidarr album by the foreign album id the Lidarr UI
// uses in its /album/<id> route. ok is false when the album is unknown or the
// app has no albums.
func (c *Client) AlbumByForeignID(ctx context.Context, foreignID string) (albumID, artistID int, ok bool, err error) {
	if c.Shape.App != "lidarr" || foreignID == "" {
		return 0, 0, false, nil
	}
	var raw []struct {
		ID       int    `json:"id"`
		ArtistID int    `json:"artistId"`
		Foreign  string `json:"foreignAlbumId"`
	}
	if err := c.get(ctx, c.Shape.API, "/album", url.Values{"foreignAlbumId": {foreignID}}, &raw); err != nil {
		return 0, 0, false, err
	}
	for _, a := range raw {
		if strings.EqualFold(a.Foreign, foreignID) {
			return a.ID, a.ArtistID, true, nil
		}
	}
	return 0, 0, false, nil
}

// Parent is a movie, series, artist or author.
type Parent struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	Slug  string `json:"slug"`
}

// Parents lists every parent in the library.
func (c *Client) Parents(ctx context.Context) ([]Parent, error) {
	var raw []map[string]any
	if err := c.get(ctx, c.Shape.API, "/"+c.Shape.Parent, nil, &raw); err != nil {
		return nil, err
	}
	out := make([]Parent, 0, len(raw))
	for _, m := range raw {
		p := Parent{ID: intOf(m["id"]), Slug: strOf(m[c.Shape.SlugField])}
		p.Title = strOf(m["title"])
		if p.Title == "" {
			p.Title = strOf(m["artistName"])
		}
		if p.Title == "" {
			p.Title = strOf(m["authorName"])
		}
		out = append(out, p)
	}
	return out, nil
}

// HistoryRecord is one history event. Data values are strings in every app,
// but decoded as any to survive nulls.
type HistoryRecord struct {
	EventType   string         `json:"eventType"`
	SourceTitle string         `json:"sourceTitle"`
	DownloadID  string         `json:"downloadId"`
	Date        time.Time      `json:"date"`
	Data        map[string]any `json:"data"`
}

// D returns a data field as a string.
func (h HistoryRecord) D(key string) string { return strOf(h.Data[key]) }

type historyPage struct {
	Page         int             `json:"page"`
	PageSize     int             `json:"pageSize"`
	TotalRecords int             `json:"totalRecords"`
	Records      []HistoryRecord `json:"records"`
}

// History pages through global history, newest first, up to limit records.
func (c *Client) History(ctx context.Context, limit int) ([]HistoryRecord, error) {
	const size = 1000
	var out []HistoryRecord
	for page := 1; len(out) < limit; page++ {
		var hp historyPage
		q := url.Values{
			"page": {strconv.Itoa(page)}, "pageSize": {strconv.Itoa(size)},
			"sortKey": {"date"}, "sortDirection": {"descending"},
		}
		if err := c.get(ctx, c.Shape.API, "/history", q, &hp); err != nil {
			return out, err
		}
		out = append(out, hp.Records...)
		if len(hp.Records) < size {
			break
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ParentHistory returns the full history of one parent.
func (c *Client) ParentHistory(ctx context.Context, parentID int) ([]HistoryRecord, error) {
	var out []HistoryRecord
	q := url.Values{c.Shape.ParentParam: {strconv.Itoa(parentID)}}
	err := c.get(ctx, c.Shape.API, "/history/"+c.Shape.Parent, q, &out)
	return out, err
}

// DownloadClient is a client configured in the *Arr. Secrets are masked by
// the *Arr API and are therefore not read.
type DownloadClient struct {
	Name           string
	Implementation string
	Protocol       string
	Host           string
	Port           int
	UseSSL         bool
	URLBase        string
}

// URL builds the client's base URL from host/port/ssl/urlBase.
func (d DownloadClient) URL() string {
	if d.Host == "" {
		return ""
	}
	scheme := "http"
	if d.UseSSL {
		scheme = "https"
	}
	host := d.Host
	if d.Port > 0 {
		host = fmt.Sprintf("%s:%d", d.Host, d.Port)
	}
	return strings.TrimRight(scheme+"://"+host+"/"+strings.Trim(d.URLBase, "/"), "/")
}

// DownloadClients lists the configured download clients.
func (c *Client) DownloadClients(ctx context.Context) ([]DownloadClient, error) {
	var raw []struct {
		Name           string `json:"name"`
		Implementation string `json:"implementation"`
		Protocol       string `json:"protocol"`
		Fields         []struct {
			Name  string `json:"name"`
			Value any    `json:"value"`
		} `json:"fields"`
	}
	if err := c.get(ctx, c.Shape.API, "/downloadclient", nil, &raw); err != nil {
		return nil, err
	}
	out := make([]DownloadClient, 0, len(raw))
	for _, r := range raw {
		d := DownloadClient{Name: r.Name, Implementation: r.Implementation, Protocol: r.Protocol}
		for _, f := range r.Fields {
			switch f.Name {
			case "host":
				d.Host = strOf(f.Value)
			case "port":
				d.Port = intOf(f.Value)
			case "useSsl":
				d.UseSSL, _ = f.Value.(bool)
			case "urlBase":
				d.URLBase = strOf(f.Value)
			}
		}
		out = append(out, d)
	}
	return out, nil
}

func strOf(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case nil:
		return ""
	default:
		return fmt.Sprint(t)
	}
}

func intOf(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case string:
		n, _ := strconv.Atoi(t)
		return n
	}
	return 0
}
