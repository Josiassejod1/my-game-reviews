// Service games serves a live SVG card of josiassejod1's game list.
// To update your games, edit games.json and push.
package games

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

//go:embed games.json
var gamesJSON []byte

// ---- types ----

type Game struct {
	Title  string `json:"title"`
	Rating string `json:"rating"`
	URL    string `json:"url"`
}

type GameList struct {
	Playing   []Game `json:"playing"`
	Completed []Game `json:"completed"`
}

// games.json is embedded at build time — parse it once.
var (
	gameList GameList
	loadOnce sync.Once
)

func getGames() GameList {
	loadOnce.Do(func() {
		if err := json.Unmarshal(gamesJSON, &gameList); err != nil {
			panic("games.json is invalid: " + err.Error())
		}
	})
	return gameList
}

// ---- SVG ----

const (
	cardWidth = 495
	padX      = 25
	lineH     = 22
	headerH   = 58
	secGap    = 8
	betweenH  = 16
	footerH   = 28
)

type section struct {
	emoji string
	label string
	games []Game
}

func cardHeight(sections []section) int {
	h := headerH
	for _, s := range sections {
		h += lineH + secGap
		if len(s.games) == 0 {
			h += lineH
		} else {
			h += len(s.games) * lineH
		}
		h += betweenH
	}
	return h + footerH
}

func buildSVG(list GameList) string {
	sections := []section{
		{"🕹️", "Playing", list.Playing},
		{"✅", "Completed", list.Completed},
	}

	height := cardHeight(sections)
	var body strings.Builder
	y := headerH

	for _, s := range sections {
		body.WriteString(fmt.Sprintf(
			`<text x="%d" y="%d" class="sec">%s %s (%d)</text>`,
			padX, y, s.emoji, s.label, len(s.games),
		))
		y += lineH + secGap

		if len(s.games) == 0 {
			body.WriteString(fmt.Sprintf(
				`<text x="%d" y="%d" class="empty">Nothing here yet</text>`,
				padX+8, y,
			))
			y += lineH
		} else {
			for _, g := range s.games {
				title := xmlEscape(truncate(g.Title, 44))
				if g.URL != "" {
					body.WriteString(fmt.Sprintf(
						`<a href="%s" target="_blank"><text x="%d" y="%d" class="game lnk">%s</text></a>`,
						xmlEscape(g.URL), padX+8, y, title,
					))
				} else {
					body.WriteString(fmt.Sprintf(
						`<text x="%d" y="%d" class="game">%s</text>`,
						padX+8, y, title,
					))
				}
				if g.Rating != "" {
					body.WriteString(fmt.Sprintf(
						`<text x="470" y="%d" class="rating" text-anchor="end">%s/10</text>`,
						y, g.Rating,
					))
				}
				y += lineH
			}
		}
		y += betweenH
	}

	updated := time.Now().UTC().Format("Jan 2, 2006")

	return fmt.Sprintf(`<svg width="%d" height="%d" xmlns="http://www.w3.org/2000/svg">
  <style>
    text    { font-family: 'Segoe UI', Ubuntu, sans-serif; }
    .title  { font-size: 15px; font-weight: 700; fill: #e6edf3; }
    .sub    { font-size: 12px; fill: #8b949e; }
    .sec    { font-size: 13px; font-weight: 600; fill: #58a6ff; }
    .game   { font-size: 13px; fill: #c9d1d9; }
    .lnk    { fill: #79c0ff; }
    .rating { font-size: 12px; fill: #8b949e; }
    .empty  { font-size: 12px; fill: #484f58; font-style: italic; }
    .foot   { font-size: 11px; fill: #484f58; }
  </style>
  <rect width="%d" height="%d" rx="10" fill="#0d1117"/>
  <rect width="%d" height="%d" rx="10" fill="none" stroke="#30363d" stroke-width="1"/>
  <text x="%d" y="30" class="title">🎮 josiassejod1 · Games</text>
  <text x="%d" y="46" class="sub">backloggd.com/u/josiassejod1</text>
  %s
  <text x="%d" y="%d" class="foot">Updated %s</text>
</svg>`,
		cardWidth, height,
		cardWidth, height,
		cardWidth, height,
		padX, padX,
		body.String(),
		padX, height-10, updated,
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
	if now.Sub(l.lastGC) > rateWindow {
		for k, times := range l.hits {
			if len(times) == 0 || now.Sub(times[len(times)-1]) > rateWindow {
				delete(l.hits, k)
			}
		}
		l.lastGC = now
	}

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
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	ip := req.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
	return ip
}

// ---- endpoint ----

//encore:api public raw method=GET path=/games-card
func GamesCard(w http.ResponseWriter, req *http.Request) {
	if !limiter.allow(clientIP(req)) {
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	svg := buildSVG(getGames())
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	fmt.Fprint(w, svg)
}
