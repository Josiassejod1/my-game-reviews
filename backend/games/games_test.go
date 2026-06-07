package games

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// ---- helper tests ----

func TestXmlEscape(t *testing.T) {
	cases := []struct{ in, want string }{
		{`AT&T`, `AT&amp;T`},
		{`<script>`, `&lt;script&gt;`},
		{`say "hi"`, `say &quot;hi&quot;`},
		{`it's`, `it&#39;s`},
		{`clean`, `clean`},
	}
	for _, c := range cases {
		if got := xmlEscape(c.in); got != c.want {
			t.Errorf("xmlEscape(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"short", 10, "short"},
		{"exactly ten!", 12, "exactly ten!"},
		{"this is too long", 10, "this is t…"},
		{"", 5, ""},
	}
	for _, c := range cases {
		if got := truncate(c.in, c.max); got != c.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}

// ---- games.json loader ----

func TestGetGamesLoads(t *testing.T) {
	list := getGames()
	// games.json must have at least one section defined (even if empty slices).
	if list.Playing == nil && list.Completed == nil {
		t.Error("getGames() returned empty struct — check games.json")
	}
}

// ---- SVG builder ----

func TestBuildSVGContents(t *testing.T) {
	list := GameList{
		Playing:   []Game{{Title: "Elden Ring", Rating: "9", URL: "https://backloggd.com/games/elden-ring/"}},
		Completed: []Game{},
	}
	svg := buildSVG(list)

	for _, needle := range []string{
		`<svg`, `xmlns="http://www.w3.org/2000/svg"`,
		"Elden Ring", "9/10",
		"Playing (1)", "Completed (0)", "Nothing here yet",
		`</svg>`,
	} {
		if !strings.Contains(svg, needle) {
			t.Errorf("SVG missing %q", needle)
		}
	}
}

func TestBuildSVGEscapesTitles(t *testing.T) {
	list := GameList{
		Playing: []Game{{Title: `Banjo & Kazooie`}},
	}
	svg := buildSVG(list)
	if strings.Contains(svg, "Banjo & Kazooie") {
		t.Error("SVG contains unescaped ampersand")
	}
	if !strings.Contains(svg, "Banjo &amp;") {
		t.Error("SVG missing escaped ampersand")
	}
}

func TestBuildSVGNoRatingSkipped(t *testing.T) {
	list := GameList{
		Playing: []Game{{Title: "Some Game"}},
	}
	svg := buildSVG(list)
	if strings.Contains(svg, "/10") {
		t.Error("SVG should not show rating when none set")
	}
}

// ---- rate limiter ----

func newLimiter() *ipLimiter {
	return &ipLimiter{hits: make(map[string][]time.Time), lastGC: time.Now()}
}

func TestRateLimiterAllows(t *testing.T) {
	l := newLimiter()
	for i := 0; i < rateLimit; i++ {
		if !l.allow("1.2.3.4") {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
}

func TestRateLimiterBlocks(t *testing.T) {
	l := newLimiter()
	for i := 0; i < rateLimit; i++ {
		l.allow("1.2.3.4")
	}
	if l.allow("1.2.3.4") {
		t.Error("request beyond limit should be blocked")
	}
}

func TestRateLimiterIsolatesIPs(t *testing.T) {
	l := newLimiter()
	for i := 0; i < rateLimit; i++ {
		l.allow("1.1.1.1")
	}
	if !l.allow("2.2.2.2") {
		t.Error("different IP should not be rate limited")
	}
}

func TestRateLimiterResetsAfterWindow(t *testing.T) {
	l := newLimiter()
	old := time.Now().Add(-2 * rateWindow)
	for i := 0; i < rateLimit; i++ {
		l.hits["1.2.3.4"] = append(l.hits["1.2.3.4"], old)
	}
	if !l.allow("1.2.3.4") {
		t.Error("IP with only expired hits should be allowed again")
	}
}

// ---- clientIP ----

func TestClientIP(t *testing.T) {
	cases := []struct {
		xff        string
		remoteAddr string
		want       string
	}{
		{"203.0.113.5, 10.0.0.1", "10.0.0.1:1234", "203.0.113.5"},
		{"", "192.168.1.1:5678", "192.168.1.1"},
		{"10.0.0.2", "10.0.0.1:9999", "10.0.0.2"},
	}
	for _, c := range cases {
		req, _ := http.NewRequest("GET", "/games-card", nil)
		req.RemoteAddr = c.remoteAddr
		if c.xff != "" {
			req.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := clientIP(req); got != c.want {
			t.Errorf("clientIP(xff=%q, addr=%q) = %q, want %q", c.xff, c.remoteAddr, got, c.want)
		}
	}
}
