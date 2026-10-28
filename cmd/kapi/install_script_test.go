package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallScriptDownloadsVerifiesAndInstallsARelease(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	release := t.TempDir()
	payload := t.TempDir()
	fake := "#!/bin/sh\nprintf 'fake kapi %s\\n' \"$*\"\n"
	fakePath := filepath.Join(payload, "kapi")
	if err := os.WriteFile(fakePath, []byte(fake), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(fakePath, 0o755); err != nil {
		t.Fatal(err)
	}
	osName := map[string]string{"darwin": "Darwin", "linux": "Linux"}[runtime.GOOS]
	arch := map[string]string{"amd64": "x86_64", "arm64": "arm64"}[runtime.GOARCH]
	if osName == "" || arch == "" {
		t.Skip("installer release matrix does not include this platform")
	}
	archive := fmt.Sprintf("kapi_1.2.3_%s_%s.tar.gz", osName, arch)
	cmd := exec.Command("tar", "-czf", filepath.Join(release, archive), "-C", payload, "kapi") //nolint:gosec // every path is test-owned
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tar: %v: %s", err, out)
	}
	body, err := os.ReadFile(filepath.Join(release, archive))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	if err := os.WriteFile(filepath.Join(release, "checksums.txt"), []byte(fmt.Sprintf("%x  %s\n", sum, archive)), 0o600); err != nil {
		t.Fatal(err)
	}
	install := filepath.Join(t.TempDir(), "bin")
	cmd = exec.Command("sh", filepath.Join(root, "install.sh")) //nolint:gosec // the repository script is the test subject
	cmd.Env = append(os.Environ(),
		"KAPI_VERSION=v1.2.3",
		"KAPI_RELEASE_BASE_URL=file://"+release,
		"KAPI_INSTALL_DIR="+install,
		"KAPI_SKIP_SETUP=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install: %v:\n%s", err, out)
	}
	if !strings.Contains(string(out), "Installed "+filepath.Join(install, "kapi")) {
		t.Errorf("output = %q", out)
	}
	installed := filepath.Join(install, "kapi")
	cmd = exec.Command(installed, "version")
	if out, err := cmd.CombinedOutput(); err != nil || string(out) != "fake kapi version\n" {
		t.Fatalf("installed binary: %v, %q", err, out)
	}
}

func TestInstallScriptRejectsABadChecksum(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	release := t.TempDir()
	osName := map[string]string{"darwin": "Darwin", "linux": "Linux"}[runtime.GOOS]
	arch := map[string]string{"amd64": "x86_64", "arm64": "arm64"}[runtime.GOARCH]
	if osName == "" || arch == "" {
		t.Skip("installer release matrix does not include this platform")
	}
	archive := fmt.Sprintf("kapi_1.2.3_%s_%s.tar.gz", osName, arch)
	if err := os.WriteFile(filepath.Join(release, archive), []byte("not an archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, "checksums.txt"), []byte(strings.Repeat("0", 64)+"  "+archive+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", filepath.Join(root, "install.sh")) //nolint:gosec // the repository script is the test subject
	cmd.Env = append(os.Environ(), "KAPI_VERSION=v1.2.3", "KAPI_RELEASE_BASE_URL=file://"+release, "KAPI_INSTALL_DIR="+t.TempDir(), "KAPI_SKIP_SETUP=1")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "checksum verification failed") {
		t.Fatalf("bad checksum: %v, %q", err, out)
	}
}
