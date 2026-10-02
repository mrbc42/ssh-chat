// Package bot is a deterministic, rules-and-events chat persona (no LLM, no
// network). It watches #main through hub.Observer and speaks through a
// normal chat session. All persona text lives in data/*.json.
package bot

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

//go:embed data/*.json
var embedded embed.FS

// Config is data/config.json. Zero values are replaced by defaults in
// loadConfig, so an override file may set only what it changes.
type Config struct {
	Nick                  string  `json:"nick"`
	Board                 string  `json:"board"`
	Timezone              string  `json:"timezone"` // "" = the server's local zone (TZ)
	TypingDelayMs         [2]int  `json:"typing_delay_ms"`
	TypingIndicator       bool    `json:"typing_indicator"`
	Baud                  int     `json:"baud"` // 0 = off
	Colour                bool    `json:"colour"`
	Wrap                  int     `json:"wrap"`
	IdleMinutes           int     `json:"idle_minutes"`
	LonelyMinutes         int     `json:"lonely_minutes"`
	LonelyCooldownMinutes int     `json:"lonely_cooldown_minutes"`
	BusyThreshold         int     `json:"busy_threshold"`
	BusyGreetProbability  float64 `json:"busy_greet_probability"`
	RemarkProbability     float64 `json:"remark_probability"`
	RapidReconnectSeconds int     `json:"rapid_reconnect_seconds"`
	RatePerUserPerMin     int     `json:"rate_per_user_per_min"`
	RateGlobalPerMin      int     `json:"rate_global_per_min"`
	DailyQuoteTime        string  `json:"daily_quote_time"`
	MaintenanceTime       string  `json:"maintenance_time"`
	TellMaxPerRecipient   int     `json:"tell_max_per_recipient"`
	TellMaxPerSender      int     `json:"tell_max_per_sender"`
	TellMaxLen            int     `json:"tell_max_len"`
	TellExpireDays        int     `json:"tell_expire_days"`
	CallerMilestones      []int   `json:"caller_milestones"`
	VisitMilestones       []int   `json:"visit_milestones"`
	TriviaSeconds         int     `json:"trivia_seconds"`
	RecentMemory          int     `json:"recent_memory"`
	locName               string  // resolved zone name, for logs
	loc                   *time.Location
}

// Variant is one reply option; Weight defaults to 1.
type Variant struct {
	Text   string
	Weight int
}

// UnmarshalJSON accepts either "plain text" or {"t": "...", "w": 3}.
func (v *Variant) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		v.Text, v.Weight = s, 1
		return nil
	}
	var o struct {
		T string `json:"t"`
		W int    `json:"w"`
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return err
	}
	v.Text, v.Weight = o.T, o.W
	if v.Weight <= 0 {
		v.Weight = 1
	}
	return nil
}

type Pool []Variant

type TriviaQ struct {
	Q string   `json:"q"`
	A []string `json:"a"` // accepted answers (normalised comparison)
}

type HistoryItem struct {
	Date string `json:"date"` // "MM-DD"
	Text string `json:"text"`
}

type Quote struct {
	Text string `json:"text"`
	By   string `json:"by"`
}

type FAQEntry struct {
	Patterns []string `json:"patterns"`
	Replies  Pool     `json:"replies"`
	res      []*regexp.Regexp
}

// Content is everything loaded from the data files.
type Content struct {
	Pools     map[string]Pool
	Motd      string
	Rules     []string
	FAQ       []FAQEntry
	Trivia    []TriviaQ
	Fortunes  []string
	Oneliners []string
	History   []HistoryItem
	Quotes    []Quote
}

func defaultConfig() Config {
	return Config{
		Nick: "SysOp-Gus", Board: "Gus's Garage BBS",
		TypingDelayMs: [2]int{1000, 3000}, Wrap: 60,
		IdleMinutes: 25, LonelyMinutes: 12, LonelyCooldownMinutes: 120,
		BusyThreshold: 6, BusyGreetProbability: 0.25, RemarkProbability: 0.35,
		RapidReconnectSeconds: 120, RatePerUserPerMin: 6, RateGlobalPerMin: 30,
		DailyQuoteTime: "09:00", MaintenanceTime: "03:00",
		TellMaxPerRecipient: 10, TellMaxPerSender: 5, TellMaxLen: 200, TellExpireDays: 30,
		CallerMilestones: []int{100, 500, 1000},
		VisitMilestones:  []int{10, 25, 50, 100, 250, 500, 1000},
		TriviaSeconds:    60, RecentMemory: 3,
	}
}

// readData reads name from dir when set and present there, else from the
// embedded defaults.
func readData(dir, name string) ([]byte, error) {
	if dir != "" {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			return b, nil
		}
	}
	return embedded.ReadFile("data/" + name)
}

func loadJSON(dir, name string, into any) error {
	b, err := readData(dir, name)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if err := json.Unmarshal(b, into); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// Load reads config and content from dir (optional overrides) falling back
// to the embedded starter data.
func Load(dir string) (Config, *Content, error) {
	cfg := defaultConfig()
	if err := loadJSON(dir, "config.json", &cfg); err != nil {
		return cfg, nil, err
	}
	cfg.loc = time.Local
	if cfg.Timezone != "" {
		loc, err := time.LoadLocation(cfg.Timezone)
		if err != nil {
			return cfg, nil, fmt.Errorf("config timezone %q: %w", cfg.Timezone, err)
		}
		cfg.loc = loc
	}
	cfg.locName = cfg.loc.String()
	if cfg.Wrap < 20 {
		cfg.Wrap = 60
	}
	if cfg.RecentMemory < 1 {
		cfg.RecentMemory = 3
	}
	for _, hhmm := range []string{cfg.DailyQuoteTime, cfg.MaintenanceTime} {
		if _, err := time.Parse("15:04", hhmm); err != nil {
			return cfg, nil, fmt.Errorf("bad time %q (want HH:MM)", hhmm)
		}
	}

	c := &Content{}
	var persona struct {
		Motd  string          `json:"motd"`
		Rules []string        `json:"rules"`
		Pools map[string]Pool `json:"pools"`
	}
	if err := loadJSON(dir, "persona.json", &persona); err != nil {
		return cfg, nil, err
	}
	c.Motd, c.Rules, c.Pools = persona.Motd, persona.Rules, persona.Pools
	for _, f := range []struct {
		name string
		into any
	}{
		{"faq.json", &c.FAQ}, {"trivia.json", &c.Trivia}, {"fortunes.json", &c.Fortunes},
		{"oneliners.json", &c.Oneliners}, {"history.json", &c.History}, {"quotes.json", &c.Quotes},
	} {
		if err := loadJSON(dir, f.name, f.into); err != nil {
			return cfg, nil, err
		}
	}
	for i := range c.FAQ {
		for _, p := range c.FAQ[i].Patterns {
			re, err := regexp.Compile("(?i)" + p)
			if err != nil {
				return cfg, nil, fmt.Errorf("faq pattern %q: %w", p, err)
			}
			c.FAQ[i].res = append(c.FAQ[i].res, re)
		}
		if len(c.FAQ[i].Replies) == 0 {
			return cfg, nil, fmt.Errorf("faq entry %d has no replies", i)
		}
	}
	return cfg, c, nil
}
