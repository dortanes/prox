//go:build !windows

package tests

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// newPIDPattern tolerates color codes between the log key and its value.
var newPIDPattern = regexp.MustCompile(`new_pid\D*?(?:\x1b\[[0-9;]*m)?(\d{2,})`)

// TestRestart_KeepsServing replaces a running process under constant traffic.
func TestRestart_KeepsServing(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "prox")
	if out, err := exec.Command("go", "build", "-o", bin, "../cmd/prox").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	port, adminPort := freePort(t), freePort(t)
	cfg := writeConfig(t, fmt.Sprintf(`{
		admin: { listen: "127.0.0.1:%d" },
		services: { web: { listen: "127.0.0.1:%d", routes: [{ action: "ok" }] } },
		actions: { ok: { type: "static", status: 200 } },
	}`, adminPort, port))

	// A file, not a pipe: the new process keeps it open after the old one exits.
	logPath := filepath.Join(dir, "prox.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()

	cmd := exec.Command(bin, "serve", "-config", cfg, "-watch=false")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})
	waitForPort(t, port, 5*time.Second)
	waitForPort(t, adminPort, 5*time.Second)

	var failures, requests atomic.Int64
	stop := make(chan struct{})
	done := make(chan struct{})
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 3 * time.Second}
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
			requests.Add(1)
			if err != nil {
				failures.Add(1)
				continue
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				failures.Add(1)
			}
		}
	}()

	if err := cmd.Process.Signal(syscall.SIGUSR2); err != nil {
		t.Fatal(err)
	}

	var child int
	var logged []byte
	deadline := time.Now().Add(10 * time.Second)
	for child == 0 && time.Now().Before(deadline) {
		logged, _ = os.ReadFile(logPath)
		if m := newPIDPattern.FindSubmatch(logged); m != nil {
			child, _ = strconv.Atoi(string(m[1]))
		}
		time.Sleep(5 * time.Millisecond)
	}
	if child == 0 {
		t.Fatalf("no new process appeared; log:\n%s", logged)
	}
	t.Cleanup(func() {
		if p, err := os.FindProcess(child); err == nil {
			_ = p.Signal(syscall.SIGKILL)
		}
	})

	select {
	case <-exited:
	case <-time.After(20 * time.Second):
		t.Fatal("old process did not exit")
	}

	time.Sleep(100 * time.Millisecond)
	close(stop)
	<-done

	if n := failures.Load(); n != 0 {
		t.Fatalf("%d of %d requests failed", n, requests.Load())
	}
	if err := syscall.Kill(child, 0); err != nil {
		t.Fatalf("new process is gone: %v", err)
	}
	waitForPort(t, adminPort, 5*time.Second)
}
