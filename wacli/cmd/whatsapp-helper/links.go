package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	ogTagRe     = regexp.MustCompile(`(?i)<meta\b[^>]*>`)
	ogPropRe    = regexp.MustCompile("(?i)(?:property|name)\\s*=\\s*['\"](?:og|twitter):(title|description|image(?::secure_url)?|url)['\"]")
	ogContentRe = regexp.MustCompile(`(?i)content\s*=\s*['"]([^'"]+)['"]`)
)

type linkPreview struct {
	url, host, site, label, title, description, imageURL, embedURL string
}

func canonicalHost(host string) string {
	h := strings.ToLower(host)
	h = strings.TrimPrefix(h, "www.")
	switch h {
	case "m.instagram.com":
		return "instagram.com"
	case "m.facebook.com", "web.facebook.com":
		return "facebook.com"
	case "m.tiktok.com":
		return "tiktok.com"
	case "vt.tiktok.com":
		return "vm.tiktok.com"
	case "mobile.twitter.com":
		return "twitter.com"
	}
	return h
}

func embedURLFor(host, raw string) string {
	raw = strings.TrimSpace(raw)
	h := canonicalHost(host)
	if h == "" && raw != "" {
		if u, err := url.Parse(raw); err == nil {
			h = canonicalHost(u.Hostname())
		}
	}
	path := ""
	if u, err := url.Parse(raw); err == nil {
		path = u.Path
	}
	switch h {
	case "instagram.com":
		m := regexp.MustCompile(`/(reel|p|tv)/([^/?#]+)`).FindStringSubmatch(path)
		if m == nil {
			return ""
		}
		return fmt.Sprintf("https://www.instagram.com/%s/%s/embed/", m[1], m[2])
	case "tiktok.com":
		m := regexp.MustCompile(`/(?:video|photo|v)/(\d+)`).FindStringSubmatch(path)
		if m == nil {
			m = regexp.MustCompile(`/embed/(?:v2|v3)/(\d+)`).FindStringSubmatch(path)
		}
		if m == nil {
			return ""
		}
		return "https://www.tiktok.com/embed/v2/" + m[1]
	case "youtube.com", "youtu.be", "m.youtube.com":
		vid := ""
		if h == "youtu.be" {
			vid = strings.TrimPrefix(path, "/")
			if i := strings.Index(vid, "/"); i >= 0 {
				vid = vid[:i]
			}
		}
		if vid == "" {
			if m := regexp.MustCompile(`/(?:embed|shorts|live)/([^/?#]+)`).FindStringSubmatch(path); m != nil {
				vid = m[1]
			}
		}
		if vid == "" {
			if m := regexp.MustCompile(`[?&]v=([^&?#]+)`).FindStringSubmatch(raw); m != nil {
				vid = m[1]
			}
		}
		if vid == "" {
			return ""
		}
		return "https://www.youtube.com/embed/" + vid
	case "x.com", "twitter.com":
		m := regexp.MustCompile(`/(?:i/web/)?status/(\d+)`).FindStringSubmatch(path)
		if m == nil {
			return ""
		}
		if strings.Contains(path, "/video/") {
			return "https://twitter.com/i/videos/tweet/" + m[1]
		}
		return "https://platform.twitter.com/embed/Tweet.html?id=" + m[1] + "&dnt=true&theme=dark"
	case "facebook.com", "fb.watch":
		parsed, err := url.Parse(raw)
		if err != nil {
			return ""
		}
		if h == "facebook.com" && regexp.MustCompile(`/share/[rv]/`).MatchString(parsed.Path) {
			return ""
		}
		canonical := *parsed
		canonical.RawQuery = ""
		canonical.Fragment = ""
		if h == "facebook.com" {
			p := parsed.Path
			if p == "" {
				p = "/"
			}
			canonical.Path = p
			canonical.Host = "www.facebook.com"
			canonical.Scheme = "https"
		}
		if canonical.String() == "" {
			return ""
		}
		return "https://www.facebook.com/plugins/video.php?href=" +
			url.QueryEscape(canonical.String()) + "&show_text=false"
	}
	return ""
}

func embedUsable(url string) bool {
	if url == "" {
		return false
	}
	if strings.Contains(url, "plugins/video.php") && strings.Contains(url, "share") {
		return false
	}
	return true
}

func describeLink(raw string) linkPreview {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	host := ""
	if err == nil {
		host = canonicalHost(parsed.Hostname())
	}
	site, label := host, host
	switch host {
	case "instagram.com":
		site = "Instagram"
		path, _ := parsed.Path, err
		switch {
		case strings.Contains(path, "/reel/"):
			label = "Reel"
		case strings.Contains(path, "/p/"):
			label = "Post"
		case strings.Contains(path, "/tv/"):
			label = "Video"
		default:
			label = "Instagram"
		}
	case "tiktok.com", "vm.tiktok.com":
		site, label = "TikTok", "TikTok"
	case "youtube.com", "youtu.be", "m.youtube.com":
		site, label = "YouTube", "YouTube"
	case "x.com", "twitter.com", "t.co":
		site, label = "X", "Post"
	case "open.spotify.com":
		site, label = "Spotify", "Spotify"
	case "facebook.com", "fb.watch":
		site, label = "Facebook", "Facebook"
	}
	return linkPreview{
		url: raw, host: host, site: site, label: label, title: label,
		embedURL: embedURLFor(host, raw),
	}
}

// --- link cache ---

func linkCachePath() string { return filepath.Join(stateDir(), "link-previews.json") }

func loadLinkCache() map[string]any {
	data := readJSONMap(linkCachePath())
	if data == nil {
		return map[string]any{}
	}
	return data
}

func saveLinkCache(cache map[string]any) { atomicWriteJSON(linkCachePath(), cache) }

// --- HTTP ---

const (
	crawlerUA = "facebookexternalhit/1.1 (+http://www.facebook.com/externalhit_uatext.php) WhatsApp/2.24.12.80"
	browserUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
)

var httpClient = &http.Client{Timeout: 8 * time.Second}

// httpURL drops non-HTTP targets and loopback/local hosts.
func httpURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasSuffix(host, ".local") {
		return ""
	}
	return u.String()
}

func httpGet(target, ua string, limit int64) ([]byte, string, string) {
	req, err := http.NewRequest("GET", target, nil)
	if err != nil {
		return nil, "", ""
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json,image/*,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, "", resp.Request.URL.String()
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, limit))
	ctype := resp.Header.Get("Content-Type")
	if i := strings.Index(ctype, ";"); i >= 0 {
		ctype = strings.TrimSpace(ctype[:i])
	}
	return data, strings.ToLower(ctype), resp.Request.URL.String()
}

func metaOG(page string) map[string]string {
	out := map[string]string{}
	for _, tag := range ogTagRe.FindAllString(page, -1) {
		prop := ogPropRe.FindStringSubmatch(tag)
		content := ogContentRe.FindStringSubmatch(tag)
		if prop == nil || content == nil {
			continue
		}
		key := strings.ToLower(prop[1])
		if key == "image:secure_url" {
			key = "image"
		}
		if _, seen := out[key]; !seen {
			out[key] = html.UnescapeString(content[1])
		}
	}
	return out
}

func fetchOG(raw string) map[string]string {
	target := httpURL(raw)
	if target == "" {
		return map[string]string{}
	}
	page := ""
	final := target
	for _, ua := range []string{crawlerUA, browserUA} {
		rawData, ctype, got := httpGet(target, ua, 512*1024)
		final = got
		if len(rawData) == 0 || (ctype != "" && !strings.Contains(ctype, "html") &&
			!strings.Contains(ctype, "xml") && !strings.Contains(ctype, "json")) {
			continue
		}
		page = string(rawData)
		if strings.Contains(page, "og:image") || strings.Contains(page, "og:title") || strings.Contains(page, "twitter:image") {
			break
		}
	}
	if page == "" {
		return map[string]string{}
	}
	og := metaOG(page)
	image := og["image"]
	if strings.HasPrefix(image, "//") {
		image = "https:" + image
	} else if image != "" && !strings.HasPrefix(image, "http") {
		if base, err := url.Parse(final); err == nil {
			if ref, err := url.Parse(image); err == nil {
				image = base.ResolveReference(ref).String()
			}
		}
	}
	image = httpURL(image)
	out := map[string]string{}
	if t := strings.TrimSpace(og["title"]); t != "" {
		out["title"] = truncate(t, 180)
	}
	if d := strings.TrimSpace(og["description"]); d != "" {
		out["description"] = truncate(d, 240)
	}
	if image != "" {
		out["imageUrl"] = image
	}
	canonical := strings.TrimSpace(og["url"])
	if strings.HasPrefix(canonical, "//") {
		canonical = "https:" + canonical
	}
	if strings.HasPrefix(canonical, "http") {
		canonical = httpURL(canonical)
		if canonical != "" {
			out["canonicalUrl"] = canonical
		}
	}
	if final != "" {
		out["finalUrl"] = final
	}
	return out
}

var oembedHosts = map[string]string{
	"tiktok.com":       "https://www.tiktok.com/oembed?url={url}",
	"vm.tiktok.com":    "https://www.tiktok.com/oembed?url={url}",
	"youtube.com":      "https://www.youtube.com/oembed?format=json&url={url}",
	"m.youtube.com":    "https://www.youtube.com/oembed?format=json&url={url}",
	"youtu.be":         "https://www.youtube.com/oembed?format=json&url={url}",
	"open.spotify.com": "https://open.spotify.com/oembed?url={url}",
	"x.com":            "https://publish.twitter.com/oembed?url={url}",
	"twitter.com":      "https://publish.twitter.com/oembed?url={url}",
}

func fetchOEmbed(raw, host string) map[string]string {
	template, ok := oembedHosts[host]
	if !ok {
		return map[string]string{}
	}
	endpoint := strings.Replace(template, "{url}", url.QueryEscape(raw), 1)
	rawData, _, _ := httpGet(endpoint, browserUA, 64*1024)
	if len(rawData) == 0 {
		return map[string]string{}
	}
	var data map[string]any
	if json.Unmarshal(rawData, &data) != nil || data == nil {
		return map[string]string{}
	}
	str := func(k string) string { s, _ := data[k].(string); return strings.TrimSpace(s) }
	out := map[string]string{}
	if t := str("title"); t != "" {
		out["title"] = truncate(t, 180)
	}
	if a := str("author_name"); a != "" {
		out["description"] = truncate(a, 240)
	}
	if t := str("thumbnail_url"); t != "" {
		out["imageUrl"] = t
	}
	vid := str("embed_product_id")
	if vid == "" {
		htmlEmbed := str("html")
		if m := regexp.MustCompile(`data-video-id="(\d+)"`).FindStringSubmatch(htmlEmbed); m != nil {
			vid = m[1]
		}
	}
	if vid != "" && isAllDigits(vid) {
		out["embedUrl"] = "https://www.tiktok.com/embed/v2/" + vid
	}
	if _, ok := out["embedUrl"]; !ok {
		embedTarget := str("url")
		if embedTarget == "" {
			embedTarget = raw
		}
		if found := embedURLFor("", embedTarget); found != "" {
			out["embedUrl"] = found
		}
	}
	return out
}

func linkThumbDir() string {
	p := filepath.Join(stateDir(), "link-thumbs")
	_ = os.MkdirAll(p, 0o700)
	_ = os.Chmod(p, 0o700)
	return p
}

func cacheRemoteImage(raw, key string) string {
	target := httpURL(raw)
	if target == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	dest := filepath.Join(linkThumbDir(), hex.EncodeToString(sum[:])+".jpg")
	if st, err := os.Stat(dest); err == nil && st.Size() > 0 {
		return fileURL(dest)
	}
	data, ctype, _ := httpGet(target, browserUA, 2*1024*1024)
	if len(data) < 32 {
		return ""
	}
	if ctype != "" && !strings.HasPrefix(ctype, "image/") &&
		!(len(data) >= 3 && (string(data[:3]) == "\xff\xd8\xff" || string(data[:3]) == "\x89PN" || string(data[:3]) == "GIF")) {
		return ""
	}
	if os.WriteFile(dest, data, 0o600) != nil {
		return ""
	}
	return fileURL(dest)
}

func resolveWithYtdlp(raw, host string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	ytdlp, err := execLookPath("yt-dlp")
	if err != nil {
		return ""
	}
	out, err := runCommand(ytdlp, []string{"--no-download", "--print", "%(id)s", "--no-warnings", raw}, 15*time.Second)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	vid := ""
	if len(lines) > 0 {
		vid = strings.TrimSpace(lines[len(lines)-1])
	}
	if vid == "" || strings.Contains(vid, " ") || len(vid) > 32 {
		return ""
	}
	if host == "facebook.com" || host == "fb.watch" || (isAllDigits(vid) && strings.Contains(raw, "facebook")) {
		if isAllDigits(vid) {
			return embedURLFor("facebook.com", "https://www.facebook.com/reel/"+vid+"/")
		}
	}
	if host == "tiktok.com" || host == "vm.tiktok.com" || strings.Contains(raw, "tiktok") {
		if isAllDigits(vid) {
			return "https://www.tiktok.com/embed/v2/" + vid
		}
	}
	if isAllDigits(vid) {
		return embedURLFor("", "https://www.tiktok.com/@x/video/"+vid)
	}
	return ""
}

// previewMap converts a linkPreview struct into the JSON shape the GUI reads.
func previewMap(p linkPreview, fetched bool) map[string]any {
	return map[string]any{
		"url": p.url, "host": p.host, "site": p.site, "label": p.label,
		"title": p.label, "description": "", "imageUrl": "",
		"embedUrl": p.embedURL, "fetched": fetched,
	}
}

func attachLinkPreview(text string, cache map[string]any) map[string]any {
	raw := firstURL(text)
	if raw == "" {
		return map[string]any{}
	}
	preview := describeLink(raw)
	if cached, ok := cache[raw].(map[string]any); ok {
		str := func(k string) string { s, _ := cached[k].(string); return s }
		for _, key := range []string{"title", "description", "imageUrl"} {
			if v := str(key); v != "" {
				switch key {
				case "title":
					preview.title = v
				case "description":
					preview.description = v
				case "imageUrl":
					preview.imageURL = v
				}
			}
		}
		if cachedEmbed := str("embedUrl"); !embedUsable(preview.embedURL) && embedUsable(cachedEmbed) {
			preview.embedURL = cachedEmbed
		}
		if str("title") != "" || str("imageUrl") != "" {
			return previewToMap(preview, true)
		}
	}
	return previewToMap(preview, false)
}

func previewToMap(p linkPreview, fetched bool) map[string]any {
	return map[string]any{
		"url": p.url, "host": p.host, "site": p.site, "label": p.label,
		"title": p.title, "description": p.description, "imageUrl": p.imageURL,
		"embedUrl": p.embedURL, "fetched": fetched,
	}
}

func fetchSocialPreview(raw, host string) map[string]string {
	out := fetchOEmbed(raw, host)
	og := fetchOG(raw)
	get := func(m map[string]string, k string) string { return m[k] }
	if ogTitle := get(og, "title"); ogTitle != "" {
		if t, ok := out["title"]; !ok || t == "" || t == "Reel" || t == "Post" || t == "TikTok" ||
			t == "YouTube" || t == "X" || t == "Spotify" {
			out["title"] = ogTitle
		}
	}
	if d := get(og, "description"); d != "" {
		if _, ok := out["description"]; !ok {
			out["description"] = d
		}
	}
	if img := get(og, "imageUrl"); img != "" {
		if _, ok := out["imageUrl"]; !ok {
			out["imageUrl"] = img
		}
	}
	if embed := get(out, "embedUrl"); !embedUsable(embed) {
		delete(out, "embedUrl")
		for _, candidate := range []string{get(og, "canonicalUrl"), get(og, "finalUrl"), raw} {
			found := embedURLFor("", candidate)
			if embedUsable(found) {
				out["embedUrl"] = found
				break
			}
		}
	}
	if embed := get(out, "embedUrl"); !embedUsable(embed) {
		if found := resolveWithYtdlp(raw, host); found != "" {
			out["embedUrl"] = found
		}
	}
	if image := get(out, "imageUrl"); image != "" {
		if local := cacheRemoteImage(image, raw); local != "" {
			out["imageUrl"] = local
		}
	}
	return out
}

func cmdLinkPreview(raw string) (map[string]any, *helperError) {
	target := firstURL(raw)
	if target == "" {
		target = raw
	}
	if target == "" {
		return nil, fail("url is missing")
	}
	preview := describeLink(target)
	cache := loadLinkCache()
	cached, _ := cache[preview.url].(map[string]any)
	str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	if cached != nil && (str(cached, "title") != "" || str(cached, "imageUrl") != "") {
		for _, key := range []string{"title", "description", "imageUrl"} {
			if v := str(cached, key); v != "" {
				switch key {
				case "title":
					preview.title = v
				case "description":
					preview.description = v
				case "imageUrl":
					preview.imageURL = v
				}
			}
		}
		cachedEmbed := str(cached, "embedUrl")
		if !embedUsable(preview.embedURL) && embedUsable(cachedEmbed) {
			preview.embedURL = cachedEmbed
		}
		if !embedUsable(preview.embedURL) {
			fetched := fetchSocialPreview(preview.url, preview.host)
			if e := fetched["embedUrl"]; embedUsable(e) {
				preview.embedURL = e
				cached["embedUrl"] = e
				cache[preview.url] = cached
				saveLinkCache(cache)
			}
		}
		return previewToMap(preview, true), nil
	}
	fetched := fetchSocialPreview(preview.url, preview.host)
	if len(fetched) > 0 {
		keep := map[string]any{}
		for _, key := range []string{"title", "description", "imageUrl", "embedUrl"} {
			if v := fetched[key]; v != "" {
				keep[key] = v
			}
		}
		cache[preview.url] = keep
		if len(cache) > 400 {
			keys := make([]string, 0, len(cache))
			for k := range cache {
				keys = append(keys, k)
			}
			for _, k := range keys[:len(keys)-400] {
				delete(cache, k)
			}
		}
		saveLinkCache(cache)
		if v := fetched["title"]; v != "" {
			preview.title = v
		}
		if v := fetched["description"]; v != "" {
			preview.description = v
		}
		if v := fetched["imageUrl"]; v != "" {
			preview.imageURL = v
		}
		if v := fetched["embedUrl"]; v != "" {
			preview.embedURL = v
		}
	}
	return previewToMap(preview, true), nil
}
