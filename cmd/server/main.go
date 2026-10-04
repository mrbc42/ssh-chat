// Command server runs the multi-room ANSI chat server over SSH.
package main

import (
	"bufio"
	"context"
	"flag"
	"log"
	"net/http"
	_ "net/http/pprof" // registers /debug/pprof on the default mux; only served when -pprof is set
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/mrbc42/ssh-chat/internal/bot"
	"github.com/mrbc42/ssh-chat/internal/filter"
	"github.com/mrbc42/ssh-chat/internal/hub"
	"github.com/mrbc42/ssh-chat/internal/sshserver"
	"github.com/mrbc42/ssh-chat/internal/store"
)

// version is set at build time (-ldflags "-X main.version=…", from the
// Containerfile's VERSION build arg); "dev" for an unlabelled build.
var version = "dev"

func main() {
	addr := flag.String("addr", ":2222", "address to listen on")
	dbPath := flag.String("db", "./data/chat.db", "path to the SQLite database file")
	hostKeyPath := flag.String("hostkey", "./data/hostkey", "path to the SSH host key (generated if absent)")
	adminsPath := flag.String("admins", "./data/admins.txt", "path to a file listing admin SSH pubkey fingerprints, one per line")
	afkAfter := flag.Duration("afk-after", hub.AfkThreshold, "idle time before a user is marked away")
	profanity := flag.Bool("profanity-filter", true, "mask a built-in list of profanity in chat")
	filterWords := flag.String("filter-words", "", "optional file of extra words to mask, one per line")
	backupDir := flag.String("backup-dir", "", "directory for periodic database backups (default: <db dir>/backups; \"off\" disables)")
	backupEvery := flag.Duration("backup-every", 24*time.Hour, "how often to back up the database")
	backupKeep := flag.Int("backup-keep", 7, "how many database backups to keep")
	botOn := flag.Bool("bot", true, "run the SysOp-Gus chat bot in #main")
	botData := flag.String("bot-data", "", "optional directory of bot data files overriding the built-in persona/content")
	botDB := flag.String("bot-db", "", "bot SQLite database (default: <db dir>/bot.db)")
	retention := flag.Duration("retention", 90*24*time.Hour, "delete chat history older than this (0 keeps everything)")
	permanentChannels := flag.String("permanent-channels", "", "comma-separated channel names created at startup that are never expired or deleted")
	roomExpiry := flag.Duration("room-expiry", 30*24*time.Hour, "delete empty channels idle this long (0 disables)")
	pprofAddr := flag.String("pprof", "", "debug: serve runtime profiling (pprof) on this address, e.g. 127.0.0.1:6060; off by default")
	flag.IntVar(&sshserver.MaxConnsPerIP, "max-conns-per-ip", sshserver.MaxConnsPerIP, "max concurrent connections from one IP")
	flag.IntVar(&sshserver.MaxConnsPerMinute, "max-conns-per-min", sshserver.MaxConnsPerMinute, "max new connections per minute from one IP")
	adminFPsFlag := flag.String("admin-fps", "", "comma-separated admin SSH key fingerprints (SHA256:...), in addition to the -admins file")
	// Settings may come from SSHCHAT_* environment variables (e.g. a .env /
	// env_file / quadlet EnvironmentFile); explicit flags still win.
	fromEnv, err := applyEnv(flag.CommandLine, os.LookupEnv)
	if err != nil {
		log.Fatalf("bad environment setting: %v", err)
	}
	flag.Parse()
	if len(fromEnv) > 0 {
		log.Printf("settings from the environment: %s", strings.Join(fromEnv, ", "))
	}

	if err := os.MkdirAll(dirOf(*dbPath), 0o755); err != nil {
		log.Fatalf("creating data directory: %v", err)
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("opening store: %v", err)
	}
	defer st.Close()

	adminFPs := loadAdminFPs(*adminsPath)
	for _, fp := range splitList(*adminFPsFlag) {
		adminFPs[fp] = true
	}
	h, err := hub.NewHub(st, adminFPs)
	if err != nil {
		log.Fatalf("creating hub: %v", err)
	}

	if *pprofAddr != "" {
		go func() { log.Printf("pprof: %v", http.ListenAndServe(*pprofAddr, nil)) }()
	}
	if names := splitList(*permanentChannels); len(names) > 0 {
		if err := h.EnsurePermanent(context.Background(), names); err != nil {
			log.Fatalf("permanent channels: %v", err)
		}
		log.Printf("permanent channels: %s", strings.Join(names, ", "))
	}
	h.SetVersion(version)
	h.StartJanitor(context.Background(), 30*time.Second)
	h.SetAfkThreshold(*afkAfter)
	if *profanity {
		var extra []string
		if *filterWords != "" {
			if extra, err = filter.LoadWords(*filterWords); err != nil {
				log.Fatalf("reading filter words: %v", err)
			}
		}
		h.SetFilter(filter.New(extra))
	}

	bg, stopBg := context.WithCancel(context.Background())
	defer stopBg()
	if *backupDir != "off" && *backupEvery > 0 {
		dir := *backupDir
		if dir == "" {
			dir = filepath.Join(dirOf(*dbPath), "backups")
		}
		go runBackups(bg, st, dir, filepath.Dir(*hostKeyPath), *backupEvery, *backupKeep)
	}
	if *botOn {
		botPath := *botDB
		if botPath == "" {
			botPath = filepath.Join(dirOf(*dbPath), "bot.db")
		}
		nick, err := bot.Start(bg, h, st, bot.Options{
			DBPath:        botPath,
			UnmatchedPath: filepath.Join(filepath.Dir(botPath), "unmatched.log"),
			DataDir:       *botData,
		})
		if err != nil {
			log.Fatalf("starting bot: %v", err)
		}
		log.Printf("bot %s is in #main (db=%s)", nick, botPath)
	}
	if *retention > 0 {
		go runRetention(bg, st, *retention)
	}
	if *roomExpiry > 0 {
		go runRoomExpiry(bg, h, *roomExpiry)
	}

	srv, err := sshserver.New(*addr, *hostKeyPath, st, h)
	if err != nil {
		log.Fatalf("creating ssh server: %v", err)
	}

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigc
		log.Println("shutting down...")
		stopBg()
		h.BroadcastAll("*** server is shutting down ***")
		_ = srv.Close()
	}()

	log.Printf("ssh-chat %s listening on %s (db=%s)", version, *addr, *dbPath)
	if err := srv.ListenAndServe(); err != nil {
		log.Printf("server stopped: %v", err)
	}
}

// runBackups snapshots the database every interval (and once at startup),
// keeping the newest `keep` snapshots. The SSH host key is copied alongside
// when not already there, since losing it breaks every client's trust.
func runBackups(ctx context.Context, st *store.Store, dir, dataDir string, every time.Duration, keep int) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("backup: cannot create %s: %v", dir, err)
		return
	}
	do := func() {
		dest := filepath.Join(dir, "chat-"+time.Now().Format("20060102-150405")+".db")
		if err := st.Backup(ctx, dest); err != nil {
			log.Printf("backup failed: %v", err)
			return
		}
		log.Printf("backup written: %s", dest)
		if key, err := os.ReadFile(filepath.Join(dataDir, "hostkey")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "hostkey")); err != nil {
				_ = os.WriteFile(filepath.Join(dir, "hostkey"), key, 0o600)
			}
		}
		files, _ := filepath.Glob(filepath.Join(dir, "chat-*.db"))
		sort.Strings(files) // timestamped names sort oldest-first
		for len(files) > keep {
			_ = os.Remove(files[0])
			files = files[1:]
		}
	}
	do()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			do()
		}
	}
}

// runRetention prunes chat history older than maxAge at startup and every 6 hours.
func runRetention(ctx context.Context, st *store.Store, maxAge time.Duration) {
	t := time.NewTicker(6 * time.Hour)
	defer t.Stop()
	for {
		n, err := st.PruneMessages(ctx, time.Now().Add(-maxAge))
		if err != nil && ctx.Err() == nil {
			log.Printf("retention: %v", err)
		}
		if n > 0 {
			log.Printf("retention: deleted %d messages older than %s", n, maxAge)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// runRoomExpiry hourly deletes empty channels with no activity for maxIdle.
func runRoomExpiry(ctx context.Context, h *hub.Hub, maxIdle time.Duration) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		gone, err := h.ExpireRooms(ctx, maxIdle)
		if err != nil {
			log.Printf("room expiry: %v", err)
		}
		if len(gone) > 0 {
			log.Printf("room expiry: deleted %v", gone)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func dirOf(path string) string {
	idx := strings.LastIndexByte(path, '/')
	if idx < 0 {
		return "."
	}
	return path[:idx]
}

func loadAdminFPs(path string) map[string]bool {
	out := make(map[string]bool)
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = true
	}
	return out
}
