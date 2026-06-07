// Service games serves a live SVG card of josiassejod1's Backloggd profile.
package games

import (
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

const (
	backloggdUser = "josiassejod1"
	cacheTTL      = 24 * time.Hour
)

// ---- data types ----

type Game struct {
	Title    string
	URL      string
	Rating   string // e.g. "9" out of 10
	CoverURL string // IGDB image URL
	CoverB64 string // base64 data URI, embedded in SVG
}

// ---- cache ----

var (
	cacheMu   sync.Mutex
	cacheData map[string][]Game
	cacheTime time.Time
)

func getCached() (map[string][]Game, bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if cacheData != nil && time.Since(cacheTime) < cacheTTL {
		return cacheData, true
	}
	return nil, false
}

func setCached(g map[string][]Game) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cacheData = g
	cacheTime = time.Now()
}

// ---- scraping ----

var httpClient = &http.Client{Timeout: 15 * time.Second}

func fetchStatus(status string) ([]Game, error) {
	url := fmt.Sprintf("https://backloggd.com/u/%s/games/%s/", backloggdUser, status)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("backloggd returned status %d", resp.StatusCode)
	}

	// Go does not auto-decompress when Accept-Encoding is set explicitly.
	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("gzip reader: %w", err)
		}
		defer gzr.Close()
		reader = gzr
	}

	doc, err := html.Parse(reader)
	if err != nil {
		return nil, err
	}

	return extractGames(doc), nil
}

func hasClass(n *html.Node, cls string) bool {
	for _, a := range n.Attr {
		if a.Key == "class" {
			for _, c := range strings.Fields(a.Val) {
				if c == cls {
					return true
				}
			}
		}
	}
	return false
}

func attrVal(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func textContent(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(b.String())
}

func extractGames(doc *html.Node) []Game {
	var games []Game
	seen := map[string]bool{}

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "div" && hasClass(n, "game-cover") {
			rating := attrVal(n, "data-rating")
			var title, gameURL, coverURL string

			var inner func(*html.Node)
			inner = func(c *html.Node) {
				if c.Type == html.ElementNode {
					if c.Data == "a" && hasClass(c, "cover-link") {
						if href := attrVal(c, "href"); href != "" {
							gameURL = "https://backloggd.com" + href
						}
					}
					if c.Data == "div" && hasClass(c, "game-text-centered") {
						title = textContent(c)
					}
					// Cover image: <img class="card-img height" src="https://images.igdb.com/...">
					if c.Data == "img" && hasClass(c, "card-img") {
						if src := attrVal(c, "src"); src != "" {
							coverURL = src
						}
					}
				}
				for child := c.FirstChild; child != nil; child = child.NextSibling {
					inner(child)
				}
			}
			inner(n)

			if title != "" && !seen[title] {
				seen[title] = true
				games = append(games, Game{
					Title:    title,
					URL:      gameURL,
					Rating:   rating,
					CoverURL: coverURL,
				})
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	return games
}

// fetchCoverB64 downloads a cover image and returns a base64 data URI.
func fetchCoverB64(imgURL string) string {
	if imgURL == "" {
		return ""
	}
	resp, err := httpClient.Get(imgURL)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil || len(data) == 0 {
		return ""
	}
	mime := http.DetectContentType(data)
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func loadGames() map[string][]Game {
	if g, ok := getCached(); ok {
		return g
	}

	statuses := []string{"playing", "completed"}
	result := make(map[string][]Game, len(statuses))

	for _, s := range statuses {
		games, err := fetchStatus(s)
		if err != nil {
			result[s] = nil
			time.Sleep(300 * time.Millisecond)
			continue
		}
		if len(games) > 5 {
			games = games[:5]
		}
		// Fetch covers in parallel
		var wg sync.WaitGroup
		for i := range games {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				games[i].CoverB64 = fetchCoverB64(games[i].CoverURL)
			}(i)
		}
		wg.Wait()
		result[s] = games
		time.Sleep(300 * time.Millisecond)
	}

	setCached(result)
	return result
}

// ---- SVG layout constants ----

const (
	cardWidth = 495
	padX      = 20
	coverW    = 75
	coverH    = 105 // ~3:4 ratio
	coverGap  = 12
	headerH   = 58
	secLabelH = 28
	titleH    = 18
	ratingH   = 16
	sectionH  = secLabelH + coverH + titleH + ratingH + 8
	betweenH  = 18
	footerH   = 30
)

type section struct {
	emoji  string
	label  string
	status string
}

var sections = []section{
	{"🕹️", "Playing", "playing"},
	{"✅", "Completed", "completed"},
}

func cardHeight(games map[string][]Game) int {
	h := headerH
	for _, sec := range sections {
		h += secLabelH
		if len(games[sec.status]) == 0 {
			h += 22
		} else {
			h += coverH + titleH + ratingH + 8
		}
		h += betweenH
	}
	return h + footerH
}

func buildSVG(games map[string][]Game) string {
	height := cardHeight(games)
	var body strings.Builder
	y := headerH

	for si, sec := range sections {
		gs := games[sec.status]

		body.WriteString(fmt.Sprintf(
			`<text x="%d" y="%d" class="sec">%s %s (%d)</text>`,
			padX, y+18, sec.emoji, sec.label, len(gs),
		))
		y += secLabelH

		if len(gs) == 0 {
			body.WriteString(fmt.Sprintf(
				`<text x="%d" y="%d" class="empty">Nothing here yet</text>`,
				padX+4, y+14,
			))
			y += 22
		} else {
			for i, g := range gs {
				cx := padX + i*(coverW+coverGap)
				cy := y

				// Rounded clip path per cover
				clipID := fmt.Sprintf("clip%d%d", si, i)
				body.WriteString(fmt.Sprintf(
					`<defs><clipPath id="%s"><rect x="%d" y="%d" width="%d" height="%d" rx="5"/></clipPath></defs>`,
					clipID, cx, cy, coverW, coverH,
				))

				// Cover image or placeholder
				if g.CoverB64 != "" {
					body.WriteString(fmt.Sprintf(
						`<image href="%s" x="%d" y="%d" width="%d" height="%d" preserveAspectRatio="xMidYMid slice" clip-path="url(#%s)"/>`,
						g.CoverB64, cx, cy, coverW, coverH, clipID,
					))
				} else {
					body.WriteString(fmt.Sprintf(
						`<rect x="%d" y="%d" width="%d" height="%d" rx="5" fill="#21262d"/>`,
						cx, cy, coverW, coverH,
					))
				}

				// Title centered under cover
				mid := cx + coverW/2
				title := xmlEscape(truncate(g.Title, 10))
				if g.URL != "" {
					body.WriteString(fmt.Sprintf(
						`<a href="%s" target="_blank"><text x="%d" y="%d" class="gtitle" text-anchor="middle">%s</text></a>`,
						xmlEscape(g.URL), mid, cy+coverH+13, title,
					))
				} else {
					body.WriteString(fmt.Sprintf(
						`<text x="%d" y="%d" class="gtitle" text-anchor="middle">%s</text>`,
						mid, cy+coverH+13, title,
					))
				}

				// Rating
				if g.Rating != "" {
					body.WriteString(fmt.Sprintf(
						`<text x="%d" y="%d" class="grating" text-anchor="middle">%s/10</text>`,
						mid, cy+coverH+27, g.Rating,
					))
				}
			}
			y += coverH + titleH + ratingH + 8
		}
		y += betweenH
	}

	updated := time.Now().UTC().Format("Jan 2, 2006")

	return fmt.Sprintf(`<svg width="%d" height="%d" xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink">
  <style>
    text     { font-family: 'Segoe UI', Ubuntu, sans-serif; }
    .title   { font-size: 15px; font-weight: 700; fill: #e6edf3; }
    .sub     { font-size: 12px; fill: #8b949e; }
    .sec     { font-size: 13px; font-weight: 600; fill: #58a6ff; }
    .gtitle  { font-size: 11px; fill: #c9d1d9; }
    .grating { font-size: 11px; fill: #8b949e; }
    .empty   { font-size: 12px; fill: #484f58; font-style: italic; }
    .foot    { font-size: 11px; fill: #484f58; }
  </style>
  <rect width="%d" height="%d" rx="10" fill="#0d1117"/>
  <rect width="%d" height="%d" rx="10" fill="none" stroke="#30363d" stroke-width="1"/>
  <text x="%d" y="30" class="title">🎮 %s · Backloggd</text>
  <text x="%d" y="46" class="sub">backloggd.com/u/%s</text>
  %s
  <text x="%d" y="%d" class="foot">Updated %s</text>
</svg>`,
		cardWidth, height,
		cardWidth, height,
		cardWidth, height,
		padX, backloggdUser,
		padX, backloggdUser,
		body.String(),
		padX, height-10,
		updated,
	)
}

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	s = strings.ReplaceAll(s, "'", "&#39;")
	return s
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}

// ---- rate limiter ----

// Allow 30 requests per IP per hour. Since the SVG is cached server-side for
// 24 hours, legitimate viewers will only hit this a handful of times.
const (
	rateLimit  = 30
	rateWindow = time.Hour
)

type ipLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	lastGC time.Time
}

var limiter = &ipLimiter{hits: make(map[string][]time.Time), lastGC: time.Now()}

func (l *ipLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()

	// Periodically clean up IPs that haven't been seen in a while.
	if now.Sub(l.lastGC) > rateWindow {
		for k, times := range l.hits {
			if len(times) == 0 || now.Sub(times[len(times)-1]) > rateWindow {
				delete(l.hits, k)
			}
		}
		l.lastGC = now
	}

	// Trim timestamps outside the window.
	cutoff := now.Add(-rateWindow)
	var recent []time.Time
	for _, t := range l.hits[ip] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}

	if len(recent) >= rateLimit {
		l.hits[ip] = recent
		return false
	}

	l.hits[ip] = append(recent, now)
	return true
}

func clientIP(req *http.Request) string {
	if xff := req.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first (leftmost) address — the original client.
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	// Strip port from RemoteAddr.
	ip := req.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
	return ip
}

// ---- Encore endpoint ----

//encore:api public raw method=GET path=/games-card
func GamesCard(w http.ResponseWriter, req *http.Request) {
	if !limiter.allow(clientIP(req)) {
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	games := loadGames()
	svg := buildSVG(games)

	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	fmt.Fprint(w, svg)
}
