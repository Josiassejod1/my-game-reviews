package games

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"
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

// ---- HTML parser tests ----

const sampleHTML = `<!DOCTYPE html><html><body>
  <div class="card mx-auto game-cover user-rating" data-rating="9" game_id="1">
    <a href="/games/elden-ring/" class="cover-link"></a>
    <img class="card-img height" src="https://images.igdb.com/cover/elden.jpg" alt="Elden Ring">
    <div class="game-text-centered">Elden Ring</div>
  </div>
  <div class="card mx-auto game-cover user-rating" data-rating="10" game_id="2">
    <a href="/games/hollow-knight/" class="cover-link"></a>
    <img class="card-img height" src="https://images.igdb.com/cover/hk.jpg" alt="Hollow Knight">
    <div class="game-text-centered">Hollow Knight</div>
  </div>
  <div class="card mx-auto game-cover" game_id="3">
    <a href="/games/celeste/" class="cover-link"></a>
    <img class="card-img height" src="https://images.igdb.com/cover/celeste.jpg" alt="Celeste">
    <div class="game-text-centered">Celeste</div>
  </div>
</body></html>`

func TestExtractGames(t *testing.T) {
	doc, err := html.Parse(strings.NewReader(sampleHTML))
	if err != nil {
		t.Fatalf("html.Parse: %v", err)
	}

	games := extractGames(doc)
	if len(games) != 3 {
		t.Fatalf("expected 3 games, got %d", len(games))
	}

	want := []struct{ title, url, rating, cover string }{
		{"Elden Ring", "https://backloggd.com/games/elden-ring/", "9", "https://images.igdb.com/cover/elden.jpg"},
		{"Hollow Knight", "https://backloggd.com/games/hollow-knight/", "10", "https://images.igdb.com/cover/hk.jpg"},
		{"Celeste", "https://backloggd.com/games/celeste/", "", "https://images.igdb.com/cover/celeste.jpg"},
	}
	for i, w := range want {
		g := games[i]
		if g.Title != w.title {
			t.Errorf("[%d] Title = %q, want %q", i, g.Title, w.title)
		}
		if g.URL != w.url {
			t.Errorf("[%d] URL = %q, want %q", i, g.URL, w.url)
		}
		if g.Rating != w.rating {
			t.Errorf("[%d] Rating = %q, want %q", i, g.Rating, w.rating)
		}
		if g.CoverURL != w.cover {
			t.Errorf("[%d] CoverURL = %q, want %q", i, g.CoverURL, w.cover)
		}
	}
}

func TestExtractGamesDeduplication(t *testing.T) {
	dup := `<html><body>
	  <div class="game-cover"><a href="/games/a/" class="cover-link"></a><div class="game-text-centered">Game A</div></div>
	  <div class="game-cover"><a href="/games/a/" class="cover-link"></a><div class="game-text-centered">Game A</div></div>
	</body></html>`

	doc, _ := html.Parse(strings.NewReader(dup))
	games := extractGames(doc)
	if len(games) != 1 {
		t.Errorf("expected 1 unique game after dedup, got %d", len(games))
	}
}

// ---- SVG builder tests ----

func TestBuildSVGContents(t *testing.T) {
	games := map[string][]Game{
		"playing":   {{Title: "Elden Ring", URL: "https://backloggd.com/games/elden-ring/", Rating: "9"}},
		"completed": {},
	}
	svg := buildSVG(games)

	for _, needle := range []string{
		`<svg`, `xmlns="http://www.w3.org/2000/svg"`,
		backloggdUser, "Elden Ring", "9/10",
		"Playing (1)", "Completed (0)", "Nothing here yet", `</svg>`,
	} {
		if !strings.Contains(svg, needle) {
			t.Errorf("SVG missing %q", needle)
		}
	}
}

func TestBuildSVGEscapesTitles(t *testing.T) {
	games := map[string][]Game{
		"playing":   {{Title: `Banjo & Kazooie`}},
		"completed": {},
	}
	svg := buildSVG(games)
	if strings.Contains(svg, "Banjo & Kazooie") {
		t.Error("SVG contains unescaped ampersand")
	}
	if !strings.Contains(svg, "Banjo &amp;") {
		t.Error("SVG missing escaped ampersand")
	}
}

// ---- rate limiter tests ----

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

// ---- clientIP tests ----

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
