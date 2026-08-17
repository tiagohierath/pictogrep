//go:build !windows

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// installerRun is one piped `curl … | sh` install against a fake release, with
// the network, the Go tool, and the install directory all pointed somewhere the
// test owns.
type installerRun struct {
	root       string
	installBin string
	goLog      string
	output     string
}

func runPipedInstaller(t *testing.T, helperScript string) installerRun {
	t.Helper()
	root := t.TempDir()
	working := filepath.Join(root, "unrelated-go-project")
	fakeBin := filepath.Join(root, "fake-bin")
	installBin := filepath.Join(root, "installed")
	for _, directory := range []string{working, fakeBin, installBin} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(working, "go.mod"), []byte("module example.test/unrelated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	goLog := filepath.Join(root, "go-ran")
	fakeGo := "#!/bin/sh\nprintf ran > \"$PICTOGREP_TEST_GO_LOG\"\nexit 99\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "go"), []byte(fakeGo), 0o755); err != nil {
		t.Fatal(err)
	}

	archive := filepath.Join(root, "release.tar.gz")
	writeFakeReleaseArchive(t, archive, helperScript)
	fakeCurl := `#!/bin/sh
output=
url=
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    shift
    output=$1
  elif [ "${1#http}" != "$1" ]; then
    url=$1
  fi
  shift
done
case "$url" in
  *.sha256) sha256sum "$PICTOGREP_TEST_ARCHIVE" | awk '{print $1}' > "$output" ;;
  *) cp "$PICTOGREP_TEST_ARCHIVE" "$output" ;;
esac
`
	if err := os.WriteFile(filepath.Join(fakeBin, "curl"), []byte(fakeCurl), 0o755); err != nil {
		t.Fatal(err)
	}

	script, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh")
	command.Dir = working
	command.Stdin = bytes.NewReader(script)
	command.Env = append(os.Environ(),
		"PATH="+fakeBin+":"+os.Getenv("PATH"),
		"PICTOGREP_TEST_ARCHIVE="+archive,
		"PICTOGREP_TEST_GO_LOG="+goLog,
		"PICTOGREP_BIN_DIR="+installBin,
		"XDG_DATA_HOME="+filepath.Join(root, "data"),
		"HOME="+filepath.Join(root, "home"),
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("piped installer failed: %v\n%s", err, output)
	}
	return installerRun{root: root, installBin: installBin, goLog: goLog, output: string(output)}
}

func TestPipedInstallerDoesNotBuildCallersGoModule(t *testing.T) {
	run := runPipedInstaller(t, "#!/bin/sh\nexit 0\n")
	if _, err := os.Stat(run.goLog); !os.IsNotExist(err) {
		t.Fatalf("piped installer executed the caller's Go tool: %v", err)
	}
	if _, err := os.Stat(filepath.Join(run.installBin, "pictogrep")); err != nil {
		t.Fatalf("release executable was not installed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(run.installBin, "pictogrep-gallery-dl")); err != nil {
		t.Fatalf("Pinterest helper was not installed: %v", err)
	}
}

// The bundled downloader is an ordinary dynamically linked program, which NixOS
// and anything else without the usual loader refuses to start. Failing the
// whole install over it left those systems unable to install or update at all,
// for a helper that only matters when importing pictures.
func TestPipedInstallerSurvivesADownloaderThatCannotRun(t *testing.T) {
	run := runPipedInstaller(t, "#!/bin/sh\necho 'cannot run this executable' >&2\nexit 1\n")
	program := filepath.Join(run.installBin, "pictogrep")
	if _, err := os.Stat(program); err != nil {
		t.Fatalf("Pictogrep was not installed because its downloader could not run: %v", err)
	}
	if _, err := os.Stat(program + ".install-sh"); err != nil {
		t.Fatalf("the installation was not marked as the installer's: %v", err)
	}
	if _, err := os.Stat(filepath.Join(run.installBin, "pictogrep-gallery-dl")); !os.IsNotExist(err) {
		t.Fatalf("a downloader that cannot run was installed anyway: %v", err)
	}
	if !strings.Contains(run.output, "does not run on this system") {
		t.Fatalf("the installer did not say the downloader was skipped:\n%s", run.output)
	}
	// The refusal it prints on the way out is Pictogrep's own explanation, not
	// the loader's wall of text.
	if strings.Contains(run.output, "cannot run this executable") {
		t.Fatalf("the downloader's own error was left on screen:\n%s", run.output)
	}
}

func writeFakeReleaseArchive(t *testing.T, target, helperScript string) {
	t.Helper()
	directory := t.TempDir()
	program := filepath.Join(directory, "pictogrep")
	if err := os.WriteFile(program, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(directory, "gallery-dl")
	if err := os.WriteFile(helper, []byte(helperScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("tar", "-C", directory, "-czf", target, "pictogrep", "gallery-dl").CombinedOutput(); err != nil {
		t.Fatalf("create fake release archive: %v\n%s", err, output)
	}
}
