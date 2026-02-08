package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	userAgent      = "EngBlogs/1.0 (+https://engineeringblogs.xyz)"
	maxConcurrency = 30
	connectTimeout = 10 * time.Second
	readTimeout    = 15 * time.Second
	maxDays        = 7
	opmlFile       = "engblogs.opml"
	cacheFile      = "cache.json"
	outputDir      = "public"
)

// OPML structures

type OPML struct {
	XMLName xml.Name `xml:"opml"`
	Body    OPMLBody `xml:"body"`
}

type OPMLBody struct {
	Outlines []OPMLOutline `xml:"outline"`
}

type OPMLOutline struct {
	Type     string        `xml:"type,attr"`
	Text     string        `xml:"text,attr"`
	Title    string        `xml:"title,attr"`
	XMLURL   string        `xml:"xmlUrl,attr"`
	HTMLURL  string        `xml:"htmlUrl,attr"`
	Children []OPMLOutline `xml:"outline"`
}

// Feed structures (support both RSS and Atom)

type RSSFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Channel RSSChannel `xml:"channel"`
}

type RSSChannel struct {
	Items []RSSItem `xml:"item"`
}

type RSSItem struct {
	Title   string `xml:"title"`
	Link    string `xml:"link"`
	PubDate string `xml:"pubDate"`
	GUID    string `xml:"guid"`
}

type AtomFeed struct {
	XMLName xml.Name    `xml:"feed"`
	Entries []AtomEntry `xml:"entry"`
}

type AtomEntry struct {
	Title   string     `xml:"title"`
	Links   []AtomLink `xml:"link"`
	Updated string     `xml:"updated"`
	Published string   `xml:"published"`
	ID      string     `xml:"id"`
}

type AtomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}

// Application types

type Feed struct {
	Title   string
	XMLURL  string
	HTMLURL string
}

type Entry struct {
	BlogName  string
	BlogURL   string
	Title     string
	URL       string
	Published time.Time
}

type CacheEntry struct {
	ETag         string  `json:"etag,omitempty"`
	LastModified string  `json:"last_modified,omitempty"`
	Entries      []Entry `json:"entries,omitempty"`
}

type Cache map[string]CacheEntry

type DateGroup struct {
	Date    string
	Entries []Entry
}

type TemplateData struct {
	Groups    []DateGroup
	FeedCount int
	EntryCount int
	BuiltAt   string
}

func main() {
	feeds, err := parseOPML(opmlFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing OPML: %v\n", err)
		os.Exit(1)
	}

	feeds = deduplicateFeeds(feeds)
	fmt.Fprintf(os.Stderr, "Parsed %d unique feeds from OPML\n", len(feeds))

	cache := loadCache(cacheFile)
	entries, stats := fetchAllFeeds(feeds, cache)
	saveCache(cacheFile, cache)

	cutoff := time.Now().UTC().AddDate(0, 0, -maxDays)
	var recent []Entry
	for _, e := range entries {
		if e.Published.After(cutoff) {
			recent = append(recent, e)
		}
	}

	recent = deduplicateEntries(recent)

	sort.Slice(recent, func(i, j int) bool {
		return recent[i].Published.After(recent[j].Published)
	})

	groups := groupByDate(recent)

	fmt.Fprintf(os.Stderr, "Feeds: %d total, %d ok, %d failed | Entries: %d (last 7 days)\n",
		stats.total, stats.success, stats.failed, len(recent))

	if err := renderHTML(groups, len(feeds), len(recent)); err != nil {
		fmt.Fprintf(os.Stderr, "Error rendering HTML: %v\n", err)
		os.Exit(1)
	}

	if err := copyOPML(); err != nil {
		fmt.Fprintf(os.Stderr, "Error copying OPML: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Built public/index.html successfully\n")
}

// --- OPML parsing ---

func parseOPML(path string) ([]Feed, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var opml OPML
	if err := xml.Unmarshal(data, &opml); err != nil {
		return nil, err
	}

	var feeds []Feed
	var extract func(outlines []OPMLOutline)
	extract = func(outlines []OPMLOutline) {
		for _, o := range outlines {
			if o.XMLURL != "" {
				title := o.Title
				if title == "" {
					title = o.Text
				}
				feeds = append(feeds, Feed{
					Title:   title,
					XMLURL:  o.XMLURL,
					HTMLURL: o.HTMLURL,
				})
			}
			if len(o.Children) > 0 {
				extract(o.Children)
			}
		}
	}
	extract(opml.Body.Outlines)
	return feeds, nil
}

func deduplicateFeeds(feeds []Feed) []Feed {
	seen := make(map[string]bool)
	var result []Feed
	for _, f := range feeds {
		if !seen[f.XMLURL] {
			seen[f.XMLURL] = true
			result = append(result, f)
		}
	}
	return result
}

// --- Cache ---

func loadCache(path string) Cache {
	data, err := os.ReadFile(path)
	if err != nil {
		return make(Cache)
	}
	var c Cache
	if err := json.Unmarshal(data, &c); err != nil {
		return make(Cache)
	}
	return c
}

func saveCache(path string, cache Cache) {
	data, err := json.Marshal(cache)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not marshal cache: %v\n", err)
		return
	}
	os.WriteFile(path, data, 0644)
}

// --- Feed fetching ---

type fetchStats struct {
	total   int
	success int
	failed  int
}

func fetchAllFeeds(feeds []Feed, cache Cache) ([]Entry, fetchStats) {
	var (
		mu      sync.Mutex
		entries []Entry
		stats   fetchStats
		wg      sync.WaitGroup
		sem     = make(chan struct{}, maxConcurrency)
	)

	stats.total = len(feeds)
	client := &http.Client{
		Timeout: connectTimeout + readTimeout,
	}

	for _, feed := range feeds {
		wg.Add(1)
		sem <- struct{}{}
		go func(f Feed) {
			defer wg.Done()
			defer func() { <-sem }()

			fetched, err := fetchFeed(client, f, cache, &mu)
			mu.Lock()
			if err != nil {
				stats.failed++
				fmt.Fprintf(os.Stderr, "  FAIL %s (%s): %v\n", f.Title, f.XMLURL, err)
			} else {
				stats.success++
				entries = append(entries, fetched...)
			}
			mu.Unlock()
		}(feed)
	}

	wg.Wait()
	return entries, stats
}

func fetchFeed(client *http.Client, feed Feed, cache Cache, mu *sync.Mutex) ([]Entry, error) {
	req, err := http.NewRequest("GET", feed.XMLURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	mu.Lock()
	if cached, ok := cache[feed.XMLURL]; ok {
		if cached.ETag != "" {
			req.Header.Set("If-None-Match", cached.ETag)
		}
		if cached.LastModified != "" {
			req.Header.Set("If-Modified-Since", cached.LastModified)
		}
	}
	mu.Unlock()

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		mu.Lock()
		cached := cache[feed.XMLURL]
		mu.Unlock()
		return cached.Entries, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	entries, err := parseFeed(body, feed)
	if err != nil {
		return nil, err
	}

	mu.Lock()
	cache[feed.XMLURL] = CacheEntry{
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		Entries:      entries,
	}
	mu.Unlock()

	return entries, nil
}

func parseFeed(data []byte, feed Feed) ([]Entry, error) {
	// Try RSS first
	var rss RSSFeed
	if err := xml.Unmarshal(data, &rss); err == nil && len(rss.Channel.Items) > 0 {
		return parseRSSItems(rss.Channel.Items, feed), nil
	}

	// Try Atom
	var atom AtomFeed
	if err := xml.Unmarshal(data, &atom); err == nil && len(atom.Entries) > 0 {
		return parseAtomEntries(atom.Entries, feed), nil
	}

	// Try RSS without wrapper (some feeds use <rdf:RDF> or bare <channel>)
	type BareChannel struct {
		XMLName xml.Name  `xml:"channel"`
		Items   []RSSItem `xml:"item"`
	}
	var bare BareChannel
	if err := xml.Unmarshal(data, &bare); err == nil && len(bare.Items) > 0 {
		return parseRSSItems(bare.Items, feed), nil
	}

	// Try RDF format
	type RDFFeed struct {
		XMLName xml.Name  `xml:"RDF"`
		Items   []RSSItem `xml:"item"`
	}
	var rdf RDFFeed
	if err := xml.Unmarshal(data, &rdf); err == nil && len(rdf.Items) > 0 {
		return parseRSSItems(rdf.Items, feed), nil
	}

	return nil, fmt.Errorf("unrecognized feed format")
}

func parseRSSItems(items []RSSItem, feed Feed) []Entry {
	var entries []Entry
	for _, item := range items {
		t := parseTime(item.PubDate)
		link := strings.TrimSpace(item.Link)
		if link == "" {
			link = strings.TrimSpace(item.GUID)
		}
		if link == "" {
			continue
		}
		entries = append(entries, Entry{
			BlogName:  feed.Title,
			BlogURL:   feed.HTMLURL,
			Title:     strings.TrimSpace(item.Title),
			URL:       link,
			Published: t,
		})
	}
	return entries
}

func parseAtomEntries(items []AtomEntry, feed Feed) []Entry {
	var entries []Entry
	for _, item := range items {
		dateStr := item.Published
		if dateStr == "" {
			dateStr = item.Updated
		}
		t := parseTime(dateStr)

		link := ""
		for _, l := range item.Links {
			if l.Rel == "alternate" || l.Rel == "" {
				link = l.Href
				break
			}
		}
		if link == "" && len(item.Links) > 0 {
			link = item.Links[0].Href
		}
		if link == "" {
			link = item.ID
		}
		if link == "" {
			continue
		}

		entries = append(entries, Entry{
			BlogName:  feed.Title,
			BlogURL:   feed.HTMLURL,
			Title:     strings.TrimSpace(item.Title),
			URL:       strings.TrimSpace(link),
			Published: t,
		})
	}
	return entries
}

var timeFormats = []string{
	time.RFC1123Z,
	time.RFC1123,
	time.RFC3339,
	time.RFC3339Nano,
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05-07:00",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05 MST",
	"Mon, 02 Jan 2006 15:04:05 -0700",
	"Mon, 02 Jan 2006 15:04:05 MST",
	"02 Jan 2006 15:04:05 -0700",
	"2 Jan 2006 15:04:05 -0700",
	"2006-01-02",
}

func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, format := range timeFormats {
		if t, err := time.Parse(format, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// --- Deduplication & grouping ---

func deduplicateEntries(entries []Entry) []Entry {
	seen := make(map[string]bool)
	var result []Entry
	for _, e := range entries {
		normalized := strings.TrimRight(strings.TrimSpace(e.URL), "/")
		if !seen[normalized] {
			seen[normalized] = true
			result = append(result, e)
		}
	}
	return result
}

func groupByDate(entries []Entry) []DateGroup {
	groups := make(map[string][]Entry)
	var order []string
	for _, e := range entries {
		key := e.Published.Format("2006-01-02")
		if _, exists := groups[key]; !exists {
			order = append(order, key)
		}
		groups[key] = append(groups[key], e)
	}
	var result []DateGroup
	for _, key := range order {
		t, _ := time.Parse("2006-01-02", key)
		label := t.Format("Monday, January 2, 2006")
		result = append(result, DateGroup{
			Date:    label,
			Entries: groups[key],
		})
	}
	return result
}

// --- Rendering ---

func renderHTML(groups []DateGroup, feedCount, entryCount int) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return err
	}

	tmpl, err := template.New("index").Parse(htmlTemplate)
	if err != nil {
		return err
	}

	data := TemplateData{
		Groups:     groups,
		FeedCount:  feedCount,
		EntryCount: entryCount,
		BuiltAt:    time.Now().UTC().Format("2006-01-02 15:04 UTC"),
	}

	f, err := os.Create(filepath.Join(outputDir, "index.html"))
	if err != nil {
		return err
	}
	defer f.Close()

	return tmpl.Execute(f, data)
}

func copyOPML() error {
	data, err := os.ReadFile(opmlFile)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outputDir, "engblogs.opml"), data, 0644)
}

// --- HTML template ---

var htmlTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Engineering Blogs — {{.EntryCount}} posts from {{.FeedCount}} feeds</title>
<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body {
  font-family: Verdana, Geneva, sans-serif;
  font-size: 14px;
  line-height: 1.5;
  color: #333;
  background: #fff;
  padding: 2em 12px;
  max-width: 90ch;
  margin: 0 auto;
  border-top: 3px solid #08f;
}
h1 {
  font-size: 42px;
  font-weight: 300;
  margin-bottom: 0.1em;
}
.subtitle {
  color: #999;
  font-size: 0.85em;
  margin-bottom: 2em;
}
.subtitle a { color: #06c; text-decoration: none; }
.subtitle a:hover { text-decoration: underline; }
h2 {
  font-size: 24px;
  font-weight: 300;
  margin-top: 2em;
  margin-bottom: 0.5em;
  color: #333;
}
.entry {
  display: flex;
  gap: 0.5em;
  padding: 4px 0;
  align-items: baseline;
}
.source {
  flex: 0 0 auto;
  max-width: 24%;
  font-size: 0.8em;
  text-transform: uppercase;
  color: #999;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  position: relative;
  padding-right: 0.75em;
}
.source::after {
  content: "";
  position: absolute;
  right: 0;
  top: 50%;
  width: 999px;
  height: 1px;
  background: #ddd;
  z-index: -1;
}
.source a { color: #999; text-decoration: none; }
.source a:hover { color: #06c; }
.title {
  flex: 1;
  min-width: 0;
  background: #fff;
  padding-left: 0.5em;
}
.title a {
  font-family: Verdana, Arial, sans-serif;
  color: #06c;
  text-decoration: none;
}
.title a:visited { color: #aaa; }
.title a:hover { text-decoration: underline; }
.time {
  font-size: 0.75em;
  color: #bbb;
  margin-left: 0.5em;
  white-space: nowrap;
}
footer {
  margin-top: 3em;
  padding-top: 1em;
  border-top: 1px solid #ddd;
  font-size: 0.8em;
  color: #999;
}
footer a { color: #06c; text-decoration: none; }
footer a:hover { text-decoration: underline; }
@media (max-width: 720px) {
  .entry {
    flex-direction: column;
    gap: 0;
    margin-bottom: 18px;
  }
  .source {
    max-width: none;
    padding-right: 0;
  }
  .source::after { display: none; }
  .title { padding-left: 0; }
}
</style>
</head>
<body>

<h1>Engineering Blogs</h1>
<p class="subtitle">
  {{.EntryCount}} posts from {{.FeedCount}} feeds in the last 7 days.
  Built {{.BuiltAt}}.
  <a href="engblogs.opml">OPML</a>
</p>

{{range .Groups}}
<h2>{{.Date}}</h2>
{{range .Entries}}
<div class="entry">
  <span class="source"><a href="{{.BlogURL}}">{{.BlogName}}</a></span>
  <span class="title"><a href="{{.URL}}">{{.Title}}</a><span class="time">{{.Published.Format "15:04"}}</span></span>
</div>
{{end}}
{{end}}

<footer>
  <a href="https://github.com/peterc/engblogs">GitHub</a> — Suggest a feed by opening an issue or PR.
</footer>

</body>
</html>
`
