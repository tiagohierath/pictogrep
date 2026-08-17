package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	releasesPage  = "https://github.com/tiagohierath/pictogrep/releases/latest"
	maxUpdateSize = 250 << 20
)

var updateHTTPClient = &http.Client{Timeout: 30 * time.Second}
var releasesAPI = "https://api.github.com/repos/tiagohierath/pictogrep/releases/latest"

type releaseAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
}

type githubRelease struct {
	Tag    string         `json:"tag_name"`
	HTML   string         `json:"html_url"`
	Assets []releaseAsset `json:"assets"`
}

type updateState struct {
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	Available      bool   `json:"available"`
	Action         string `json:"action"`
	UpdateMethod   string `json:"updateMethod"`
	Hint           string `json:"hint,omitempty"`
	URL            string `json:"url,omitempty"`
	AssetURL       string `json:"-"`
	AssetSHA256    string `json:"-"`
}

type installationChannel struct {
	Action string
	Method string
	Hint   string
}

func checkForUpdate() (updateState, error) {
	req, err := http.NewRequest(http.MethodGet, releasesAPI, nil)
	if err != nil {
		return updateState{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Pictogrep/"+version)
	response, err := updateHTTPClient.Do(req)
	if err != nil {
		return updateState{}, fmt.Errorf("could not check GitHub Releases: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return updateState{}, fmt.Errorf("GitHub Releases returned %s", response.Status)
	}
	var release githubRelease
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&release); err != nil {
		return updateState{}, fmt.Errorf("could not read the latest release: %w", err)
	}
	latest := strings.TrimPrefix(strings.TrimSpace(release.Tag), "v")
	if latest == "" {
		return updateState{}, fmt.Errorf("the latest GitHub release has no version")
	}
	channel := currentInstallationChannel()
	state := updateState{
		CurrentVersion: version,
		LatestVersion:  latest,
		Available:      compareVersions(latest, version) > 0,
		Action:         "none",
		UpdateMethod:   channel.Method,
		Hint:           channel.Hint,
	}
	if !state.Available {
		return state, nil
	}
	state.URL = release.HTML
	if state.URL == "" {
		state.URL = releasesPage
	}
	assetName := ""
	state.Action = channel.Action
	switch state.Action {
	case "download":
		assetName = "pictogrep-windows-x86_64-setup.exe"
	case "replace":
		arch := map[string]string{"amd64": "x86_64", "arm64": "arm64"}[runtime.GOARCH]
		if arch != "" {
			assetName = "pictogrep-linux-" + arch
		}
	}
	for _, asset := range release.Assets {
		if asset.Name == assetName {
			if state.Action == "replace" {
				digest, valid := releaseAssetSHA256(asset.Digest)
				if !valid {
					break
				}
				state.AssetSHA256 = digest
			}
			state.AssetURL = asset.URL
			if state.Action == "download" {
				state.URL = asset.URL
			}
			break
		}
	}
	if (state.Action == "replace" || state.Action == "download") && state.AssetURL == "" {
		state.Action = "release"
		state.UpdateMethod = "GitHub Releases"
		state.Hint = "Download the newest standalone release from GitHub Releases."
	}
	return state, nil
}

func releaseAssetSHA256(value string) (string, bool) {
	algorithm, digest, found := strings.Cut(strings.TrimSpace(value), ":")
	if !found || !strings.EqualFold(algorithm, "sha256") || len(digest) != sha256.Size*2 {
		return "", false
	}
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != sha256.Size {
		return "", false
	}
	return strings.ToLower(digest), true
}

func verifyUpdateDigest(binary []byte, expected string) error {
	expectedBytes, err := hex.DecodeString(expected)
	if err != nil || len(expectedBytes) != sha256.Size {
		return fmt.Errorf("the release does not provide a valid SHA-256 digest")
	}
	actual := sha256.Sum256(binary)
	if subtle.ConstantTimeCompare(actual[:], expectedBytes) != 1 {
		return fmt.Errorf("the downloaded Pictogrep executable failed SHA-256 verification")
	}
	return nil
}

func currentInstallationChannel() installationChannel {
	if runtime.GOOS == "windows" {
		return installationChannel{
			Action: "download", Method: "Windows installer",
			Hint: "Download and open the installer to update.",
		}
	}
	if runtime.GOOS == "linux" && installedByScript() {
		return installationChannel{
			Action: "replace", Method: "Pictogrep installer",
			Hint: "Pictogrep can install it after you press Update.",
		}
	}
	// A binary somebody built and dropped in their own bin directory is just as
	// safe to replace as one the installer put there, and requiring the
	// installer's marker file left those installations with no way to update at
	// all: no button, and an automatic check that could never act. What matters
	// is that no package manager owns this copy and that the directory is
	// really writable, which is settled by writing to it rather than by reading
	// permission bits.
	if runtime.GOOS == "linux" && currentPackageManager() == "" && executableIsReplaceable() {
		return installationChannel{
			Action: "replace", Method: "Standalone binary",
			Hint: "Pictogrep can install it after you press Update.",
		}
	}
	if manager := currentPackageManager(); manager != "" {
		hint := "Update through your system package manager when the package is available."
		if manager == "Nix" {
			hint = "Update through Nix or your NixOS configuration when the package is available."
		} else if manager == "Arch/AUR" {
			hint = "Update through your Arch/AUR package manager when the package is available."
		}
		return installationChannel{
			Action: "managed", Method: manager,
			Hint: hint,
		}
	}
	return installationChannel{
		Action: "release", Method: "GitHub Releases",
		Hint: "Download the newest standalone archive from GitHub Releases.",
	}
}

func currentPackageManager() string {
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return ""
	}
	_, archError := os.Stat("/etc/arch-release")
	return packageManagerForExecutable(executable, archError == nil)
}

func packageManagerForExecutable(executable string, archLinux bool) string {
	path := filepath.ToSlash(filepath.Clean(executable))
	if strings.HasPrefix(path, "/nix/store/") {
		return "Nix"
	}
	if path == "/usr/bin/pictogrep" || path == "/bin/pictogrep" {
		if archLinux {
			return "Arch/AUR"
		}
		return "system package manager"
	}
	return ""
}

func updateMethod() string {
	return currentInstallationChannel().Method
}

func compareVersions(a, b string) int {
	parse := func(value string) ([]int, bool) {
		value = strings.TrimPrefix(strings.TrimSpace(value), "v")
		value = strings.SplitN(value, "+", 2)[0]
		core, prerelease, _ := strings.Cut(value, "-")
		parts := strings.Split(core, ".")
		numbers := make([]int, len(parts))
		for index, part := range parts {
			numbers[index], _ = strconv.Atoi(part)
		}
		return numbers, prerelease != ""
	}
	left, leftPrerelease := parse(a)
	right, rightPrerelease := parse(b)
	length := len(left)
	if len(right) > length {
		length = len(right)
	}
	for index := 0; index < length; index++ {
		var x, y int
		if index < len(left) {
			x = left[index]
		}
		if index < len(right) {
			y = right[index]
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	if !leftPrerelease && rightPrerelease {
		return 1
	}
	if leftPrerelease && !rightPrerelease {
		return -1
	}
	return 0
}

func installedByScript() bool {
	executable, err := os.Executable()
	if err != nil {
		return false
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return false
	}
	marker, err := os.ReadFile(executable + ".install-sh")
	if err != nil {
		return false
	}
	marked, err := filepath.EvalSymlinks(strings.TrimSpace(string(marker)))
	return err == nil && marked == executable
}

// executableIsReplaceable proves the running binary can be swapped out, by
// creating and removing a file beside it exactly the way the update does. A
// read-only /nix/store path, a root-owned /usr/bin, or a directory that has
// gone away all fail here instead of failing halfway through an update.
func executableIsReplaceable() bool {
	executable, err := os.Executable()
	if err != nil {
		return false
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return false
	}
	info, err := os.Stat(executable)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	probe, err := os.CreateTemp(filepath.Dir(executable), ".pictogrep-probe-*")
	if err != nil {
		return false
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return true
}

func installUpdate(state updateState) error {
	if runtime.GOOS != "linux" || state.Action != "replace" || state.AssetURL == "" || state.AssetSHA256 == "" {
		return fmt.Errorf("this installation must be updated from the releases page")
	}
	if !installedByScript() && !(currentPackageManager() == "" && executableIsReplaceable()) {
		return fmt.Errorf("this installation must be updated from the releases page")
	}
	response, err := updateHTTPClient.Get(state.AssetURL)
	if err != nil {
		return fmt.Errorf("could not download the update: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("update download returned %s", response.Status)
	}
	binary, err := io.ReadAll(io.LimitReader(response.Body, maxUpdateSize+1))
	if err != nil {
		return fmt.Errorf("could not download the update: %w", err)
	}
	if len(binary) == 0 || len(binary) > maxUpdateSize {
		return fmt.Errorf("the downloaded Pictogrep executable is invalid")
	}
	if err := verifyUpdateDigest(binary, state.AssetSHA256); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return err
	}
	temporaryFile, err := os.CreateTemp(filepath.Dir(executable), ".pictogrep-update-*")
	if err != nil {
		return fmt.Errorf("could not prepare the update: %w", err)
	}
	temporary := temporaryFile.Name()
	defer os.Remove(temporary)
	if err := temporaryFile.Chmod(0o755); err != nil {
		_ = temporaryFile.Close()
		return fmt.Errorf("could not prepare the update: %w", err)
	}
	if _, err := temporaryFile.Write(binary); err != nil {
		_ = temporaryFile.Close()
		return fmt.Errorf("could not prepare the update: %w", err)
	}
	if err := temporaryFile.Close(); err != nil {
		return fmt.Errorf("could not prepare the update: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, temporary, "version").CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "pictogrep "+state.LatestVersion {
		return fmt.Errorf("the downloaded Pictogrep executable could not be verified")
	}
	if err := os.Rename(temporary, executable); err != nil {
		return fmt.Errorf("could not replace Pictogrep: %w", err)
	}
	return nil
}
