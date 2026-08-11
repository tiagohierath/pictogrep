package main

import (
	"context"
	"flag"
	"fmt"
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
	command := "web"
	if len(args) > 0 {
		command = args[0]
	}
	switch command {
	case "help", "-h", "--help":
		usage()
		return nil
	case "version", "--version":
		fmt.Println("pictogrep " + version)
		return nil
	}
	app, err := newApplication()
	if err != nil {
		return err
	}
	switch command {
	case "paths":
		fmt.Printf("home:        %s\nlibrary:     %s\nindex:       %s\ntags:        %s\nstoryboards: %s\n", app.home, app.libraryDir, app.dataDir, app.tagsDir, app.boardsDir)
		return nil
	case "doctor":
		return doctor(app)
	case "index":
		if len(args) < 2 {
			return fmt.Errorf("usage: pictogrep index FOLDER...")
		}
		fmt.Println("Scanning image folders…")
		if err := app.indexFolders(args[1:]); err != nil {
			return err
		}
		paths, _, _ := app.snapshot()
		fmt.Printf("Library ready: %d images.\n", len(paths))
		return nil
	case "search":
		if len(args) < 2 {
			return fmt.Errorf("usage: pictogrep search QUERY")
		}
		results, ai, err := app.search(strings.Join(args[1:], " "), 50)
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
		return serve(app, append([]string{"--page", "practice"}, args[1:]...))
	case "web", "app", "serve":
		return serve(app, args[1:])
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
	flags := flag.NewFlagSet("pictogrep web", flag.ContinueOnError)
	port := flags.Int("port", defaultPort, "local port")
	noOpen := flags.Bool("no-open", false, "do not open a browser")
	page := flags.String("page", "app", "initial page")
	if err := flags.Parse(args); err != nil {
		return err
	}
	handler, err := newServer(app)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(*port))
	if err != nil && *port == defaultPort {
		if existingURL := runningURL(*port, *page); existingURL != "" {
			fmt.Println("Pictogrep is already running:", existingURL)
			if !*noOpen {
				_ = openBrowser(existingURL)
			}
			return nil
		}
		listener, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		return fmt.Errorf("could not listen on port %d: %w", *port, err)
	}
	actualPort := listener.Addr().(*net.TCPAddr).Port
	path := "/"
	if *page == "practice" {
		path = "/practice"
	}
	url := fmt.Sprintf("http://127.0.0.1:%d%s", actualPort, path)
	paths, _, _ := app.snapshot()
	fmt.Println("Pictogrep:", url)
	fmt.Printf("%d images available. Press Ctrl+C to stop.\n", len(paths))
	if !*noOpen {
		go func() {
			time.Sleep(120 * time.Millisecond)
			if err := openBrowser(url); err != nil {
				fmt.Fprintln(os.Stderr, "Open this URL in your browser:", url)
			}
		}()
	}
	httpServer := &http.Server{Handler: handler.routes(), ReadHeaderTimeout: 5 * time.Second}
	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stopped
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
	}()
	err = httpServer.Serve(listener)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func runningURL(port int, page string) string {
	client := http.Client{Timeout: 350 * time.Millisecond}
	url := fmt.Sprintf("http://127.0.0.1:%d/api/app/state", port)
	response, err := client.Get(url)
	if err != nil {
		return ""
	}
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		return ""
	}
	if page == "practice" {
		return fmt.Sprintf("http://127.0.0.1:%d/practice", port)
	}
	return fmt.Sprintf("http://127.0.0.1:%d/", port)
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
