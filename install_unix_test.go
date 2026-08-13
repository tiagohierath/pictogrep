//go:build !windows

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPipedInstallerDoesNotBuildCallersGoModule(t *testing.T) {
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
	writeFakeReleaseArchive(t, archive)
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
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("piped installer failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(goLog); !os.IsNotExist(err) {
		t.Fatalf("piped installer executed the caller's Go tool: %v", err)
	}
	if _, err := os.Stat(filepath.Join(installBin, "pictogrep")); err != nil {
		t.Fatalf("release executable was not installed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(installBin, "pictogrep-gallery-dl")); err != nil {
		t.Fatalf("Pinterest helper was not installed: %v", err)
	}
}

func writeFakeReleaseArchive(t *testing.T, target string) {
	t.Helper()
	directory := t.TempDir()
	program := filepath.Join(directory, "pictogrep")
	if err := os.WriteFile(program, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(directory, "gallery-dl")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("tar", "-C", directory, "-czf", target, "pictogrep", "gallery-dl").CombinedOutput(); err != nil {
		t.Fatalf("create fake release archive: %v\n%s", err, output)
	}
}
