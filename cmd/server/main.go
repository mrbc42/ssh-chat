// Command server runs the multi-room ANSI chat server over SSH.
package main

import (
	"bufio"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mrbc42/ssh-chat/internal/hub"
	"github.com/mrbc42/ssh-chat/internal/sshserver"
	"github.com/mrbc42/ssh-chat/internal/store"
)

func main() {
	addr := flag.String("addr", ":2222", "address to listen on")
	dbPath := flag.String("db", "./data/chat.db", "path to the SQLite database file")
	hostKeyPath := flag.String("hostkey", "./data/hostkey", "path to the SSH host key (generated if absent)")
	adminsPath := flag.String("admins", "./data/admins.txt", "path to a file listing admin SSH pubkey fingerprints, one per line")
	flag.Parse()

	if err := os.MkdirAll(dirOf(*dbPath), 0o755); err != nil {
		log.Fatalf("creating data directory: %v", err)
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("opening store: %v", err)
	}
	defer st.Close()

	adminFPs := loadAdminFPs(*adminsPath)
	h, err := hub.NewHub(st, adminFPs)
	if err != nil {
		log.Fatalf("creating hub: %v", err)
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
		h.BroadcastAll("*** server is shutting down ***")
		_ = srv.Close()
	}()

	log.Printf("ssh-chat listening on %s (db=%s)", *addr, *dbPath)
	if err := srv.ListenAndServe(); err != nil {
		log.Printf("server stopped: %v", err)
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
