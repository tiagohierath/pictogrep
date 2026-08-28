package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const defaultPort = 8765

func usage() {
	fmt.Print(`Pictogrep

Usage:
  pictogrep                         open the local browser app
  pictogrep web [options]           open or serve the browser app
  pictogrep storyboard [options]    open the storyboard studio
  pictogrep index FOLDER...         add or refresh image folders
  pictogrep search QUERY            search image filenames from a terminal
  pictogrep doctor                  check the installation
  pictogrep paths                   show local data paths
  pictogrep version                 print the installed version

Web options:
  --port PORT                       preferred local port (default 8765)
  --no-open                         do not open a browser

Pictogrep needs no Python, Go, Git, or other programming tools at runtime.
`)
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		reportFatal(err)
		os.Exit(1)
	}
}

func run(args []string) error {
	command, commandArgs := splitCommand(args)
	switch command {
	case "help", "-h", "--help":
		usage()
		return nil
	case "version", "--version":
		fmt.Println("pictogrep " + version)
		return nil
	case "install-desktop":
		return installDesktopShortcut()
	case "uninstall-desktop":
		return uninstallDesktopShortcut()
	}
	home := defaultHome()
	if locksTheLibrary {
		lock, err := acquireInstanceLock(home)
		if err != nil {
			if errors.Is(err, errInstanceLocked) {
				if reused, reuseErr := reuseExistingServer(command, commandArgs, home); reused || reuseErr != nil {
					return reuseErr
				}
			}
			return err
		}
		defer lock.Close()
	}
	app, err := newApplication()
	if err != nil {
		return err
	}
	switch command {
	case "paths":
		fmt.Printf("home:        %s\nlibrary:     %s\nconfig:      %s\nindex:       %s\ntags:        %s\nstoryboards: %s\n", app.home, app.libraryDir, app.configPath, app.dataDir, app.tagsDir, app.boardsDir)
		return nil
	case "doctor":
		return doctor(app)
	case "index":
		if len(commandArgs) < 1 {
			return fmt.Errorf("usage: pictogrep index FOLDER…")
		}
		fmt.Println("Scanning image folders…")
		if err := app.indexFolders(commandArgs); err != nil {
			return err
		}
		paths, _, _ := app.snapshot()
		fmt.Printf("Library ready: %d images.\n", len(paths))
		return nil
	case "search":
		if len(commandArgs) < 1 {
			return fmt.Errorf("usage: pictogrep search QUERY")
		}
		results, ai, err := app.search(strings.Join(commandArgs, " "), 50)
		if err != nil {
			return err
		}
		if !ai {
			fmt.Fprintln(os.Stderr, "AI add-on not installed; showing filename matches.")
		}
		for _, result := range results {
			fmt.Printf("%.3f\t%s\n", result.Score, result.Path)
		}
		return nil
	case "storyboard", "story", "board", "sb":
		return serve(app, append([]string{"--page", "practice"}, commandArgs...))
	case "web", "app", "serve":
		return serve(app, commandArgs)
	default:
		// Preserve the convenient `pictogrep words to search` behavior.
		results, _, err := app.search(strings.Join(args, " "), 50)
		if err != nil {
			return err
		}
		for _, result := range results {
			fmt.Printf("%.3f\t%s\n", result.Score, result.Path)
		}
		return nil
	}
}

type serveOptions struct {
	port   int
	noOpen bool
	page   string
}

func parseServeOptions(args []string) (serveOptions, error) {
	flags := flag.NewFlagSet("pictogrep web", flag.ContinueOnError)
	port := flags.Int("port", defaultPort, "local port")
	noOpen := flags.Bool("no-open", false, "do not open a browser")
	page := flags.String("page", "app", "initial page")
	if err := flags.Parse(args); err != nil {
		return serveOptions{}, err
	}
	return serveOptions{port: *port, noOpen: *noOpen, page: *page}, nil
}

func reuseExistingServer(command string, args []string, home string) (bool, error) {
	switch command {
	case "storyboard", "story", "board", "sb":
		args = append([]string{"--page", "practice"}, args...)
	case "web", "app", "serve":
	default:
		return false, nil
	}
	options, err := parseServeOptions(args)
	if err != nil {
		return true, err
	}
	instance := probeRunningInstance(options.port)
	if instance.matches(home, version) {
		url := instance.pageURL(options.page)
		fmt.Println("Pictogrep is already running:", url)
		if !options.noOpen {
			_ = openBrowser(url)
		}
		return true, nil
	}
	if instance.found && sameDataHome(instance.home, home) {
		return true, fmt.Errorf("Pictogrep %s is still using this library; close it before starting Pictogrep %s", instance.version, version)
	}
	return true, fmt.Errorf("another Pictogrep process is using this library; close it before starting a new one")
}

func splitCommand(args []string) (string, []string) {
	if len(args) == 0 {
		return "web", nil
	}
	return args[0], args[1:]
}

func doctor(app *application) error {
	paths, sources, _ := app.snapshot()
	fmt.Println("Pictogrep doctor")
	fmt.Printf("version: %s\n", version)
	fmt.Printf("platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("home: %s\n", app.home)
	fmt.Printf("library: ok (%d images)\n", len(paths))
	fmt.Printf("sources: %d folder(s)\n", len(sources))
	fmt.Println("semantic search: built in")
	if browserCommand("http://127.0.0.1") == nil {
		fmt.Println("browser opener: not found (the app still prints its URL)")
	} else {
		fmt.Println("browser opener: ok")
	}
	return nil
}

func serve(app *application, args []string) error {
	options, err := parseServeOptions(args)
	if err != nil {
		return err
	}
	handler, err := newServer(app)
	if err != nil {
		return err
	}
	// Loading the sync identity (a file read, and on first run a P-256 keygen
	// plus an mDNS bind) used to run before the listener even opened, so a
	// slow disk or network stack delayed the browser for a feature most
	// launches never touch. It now runs on its own goroutine, overlapping
	// with everything below down to the URL print and browser open; only
	// httpServer.Serve, several lines down, waits for it to finish, which is
	// early enough that no request ever sees a half-attached sync and late
	// enough that the write to handler.sync happens-before any handler
	// goroutine can read it.
	//
	// Best effort: a device that cannot get an identity (a read-only data
	// directory, most likely) still gets a working library, just no sync tab.
	//
	// The success line matters as much as the failure one on Android, where it
	// is the only evidence from outside the process that PICTOGREP_SIGNER was
	// reachable and Keystore actually signed something: this line only prints
	// once loadIdentity has gone all the way through bridgedSigner and back.
	// scripts/launch-check.sh asserts it appears, which is the one check in
	// this whole codebase that touches real Android Keystore rather than a
	// desktop stand-in for it.
	syncAttached := make(chan struct{})
	go func() {
		defer close(syncAttached)
		if err := handler.attachSync(deviceDisplayName()); err != nil {
			log.Printf("sync unavailable: %v", err)
		} else {
			log.Printf("sync ready: device %s", handler.sync.identity.id)
		}
	}()
	listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(options.port))
	if err != nil && options.port == defaultPort {
		instance := probeRunningInstance(options.port)
		if instance.matches(app.home, version) {
			existingURL := instance.pageURL(options.page)
			fmt.Println("Pictogrep is already running:", existingURL)
			if !options.noOpen {
				_ = openBrowser(existingURL)
			}
			return nil
		}
		if instance.found && sameDataHome(instance.home, app.home) {
			return fmt.Errorf("Pictogrep %s is still using this library; close it before starting Pictogrep %s", instance.version, version)
		}
		listener, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		return fmt.Errorf("could not listen on port %d: %w", options.port, err)
	}
	actualPort := listener.Addr().(*net.TCPAddr).Port
	path := "/"
	if options.page == "practice" {
		path = "/practice"
	}
	url := fmt.Sprintf("http://127.0.0.1:%d%s", actualPort, path)
	paths, _, _ := app.snapshot()
	fmt.Println("Pictogrep:", url)
	fmt.Printf("%d images available. Press Ctrl+C to stop.\n", len(paths))
	if !options.noOpen {
		// No delay needed: net.Listen has already put the socket in the
		// kernel's accept queue, so a browser dialing in right now just waits
		// there harmlessly until httpServer.Serve (a few lines below) starts
		// pulling connections off it. The old fixed sleep was dead time on
		// every single launch for no benefit.
		go func() {
			if err := openBrowser(url); err != nil {
				fmt.Fprintln(os.Stderr, "Open this URL in your browser:", url)
			}
		}()
	}
	watch := newIdleWatch()
	httpServer := &http.Server{Handler: watch.wrap(handler.routes()), ReadHeaderTimeout: 5 * time.Second}
	// Tracked Pinterest boards are re-checked in the background so a board that
	// keeps growing does not silently fall behind the library.
	syncCtx, stopSync := context.WithCancel(context.Background())
	defer stopSync()
	go handler.watchPinterestBoards(syncCtx)
	if updatesItself {
		go handler.watchForUpdates(syncCtx)
	}
	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
	}
	superviseParent(shutdown)
	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stopped
		shutdown()
	}()
	if closesWhenIdle {
		go func() {
			ticker := time.NewTicker(idleCheckEvery)
			defer ticker.Stop()
			for range ticker.C {
				if watch.idleFor() < idleShutdownAfter || app.indexing() || handler.pinterest.running() {
					continue
				}
				fmt.Println("Pictogrep closed itself after an hour with no windows open.")
				shutdown()
				return
			}
		}()
	}
	<-syncAttached
	err = httpServer.Serve(listener)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

type runningInstance struct {
	found   bool
	port    int
	version string
	home    string
}

func probeRunningInstance(port int) runningInstance {
	client := http.Client{Timeout: 3 * time.Second}
	url := fmt.Sprintf("http://127.0.0.1:%d/api/app/state", port)
	response, err := client.Get(url)
	if err != nil {
		return runningInstance{}
	}
	defer response.Body.Close()
	var state struct {
		OK      bool              `json:"ok"`
		Version string            `json:"version"`
		Paths   map[string]string `json:"paths"`
	}
	if response.StatusCode != 200 || json.NewDecoder(response.Body).Decode(&state) != nil || !state.OK || state.Version == "" || state.Paths["home"] == "" {
		return runningInstance{}
	}
	return runningInstance{found: true, port: port, version: state.Version, home: state.Paths["home"]}
}

func (instance runningInstance) matches(home, expectedVersion string) bool {
	return instance.found && instance.version == expectedVersion && sameDataHome(instance.home, home)
}

func (instance runningInstance) pageURL(page string) string {
	if page == "practice" {
		return fmt.Sprintf("http://127.0.0.1:%d/practice", instance.port)
	}
	return fmt.Sprintf("http://127.0.0.1:%d/", instance.port)
}

func runningURL(port int, page, home, expectedVersion string) string {
	instance := probeRunningInstance(port)
	if !instance.matches(home, expectedVersion) {
		return ""
	}
	return instance.pageURL(page)
}

func browserCommand(url string) *exec.Cmd {
	if browser := strings.TrimSpace(os.Getenv("BROWSER")); browser != "" {
		parts := strings.Fields(browser)
		return exec.Command(parts[0], append(parts[1:], url)...)
	}
	if runtime.GOOS == "windows" {
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	}
	if runtime.GOOS == "darwin" {
		return exec.Command("open", url)
	}
	if command, err := exec.LookPath("xdg-open"); err == nil {
		return exec.Command(command, url)
	}
	if command, err := exec.LookPath("gio"); err == nil {
		return exec.Command(command, "open", url)
	}
	return nil
}

func openBrowser(url string) error {
	command := browserCommand(url)
	if command == nil {
		return fmt.Errorf("no browser opener found")
	}
	command.Stdout = nil
	command.Stderr = nil
	return command.Start()
}
