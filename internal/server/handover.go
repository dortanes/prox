package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"strconv"
	"time"
)

const (
	envListeners = "PROX_INHERITED_LISTENERS" // JSON: listen address → file descriptors
	envReadyFD   = "PROX_READY_FD"

	// handoverTimeout covers the new process loading config and certificates.
	handoverTimeout = 30 * time.Second

	firstExtraFD = 3 // descriptor of the first exec.Cmd.ExtraFiles entry
)

// Handover starts a new copy of this process on the same listening sockets and
// returns once it accepts connections. Both accept during the overlap, so no
// connection is refused; the caller then shuts this process down gracefully.
// On error the new process is gone and this one keeps serving.
func (g *Group) Handover() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating executable: %w", err)
	}

	byAddr, files, err := g.listenerFiles()
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	if err != nil {
		return err
	}

	readyR, readyW, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("creating readiness pipe: %w", err)
	}
	defer readyR.Close()
	defer readyW.Close()

	encoded, err := json.Marshal(byAddr)
	if err != nil {
		return err
	}

	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = append(files, readyW)
	cmd.Env = append(os.Environ(),
		envListeners+"="+string(encoded),
		envReadyFD+"="+strconv.Itoa(firstExtraFD+len(files)),
	)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting new process: %w", err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	readyW.Close()

	// The pipe reports EOF without a byte when the new process dies first.
	_ = readyR.SetReadDeadline(time.Now().Add(handoverTimeout))
	var b [1]byte
	if _, err := io.ReadFull(readyR, b[:]); err != nil {
		_ = cmd.Process.Kill()
		<-exited
		return fmt.Errorf("new process did not become ready: %w", err)
	}
	slog.Info("new process accepting connections", "new_pid", cmd.Process.Pid)
	return nil
}

// listenerFiles duplicates the listening sockets of all services. It returns
// the descriptors each address will have in the new process, and the files.
func (g *Group) listenerFiles() (map[string][]int, []*os.File, error) {
	byAddr := make(map[string][]int)
	var files []*os.File
	for _, ms := range g.servers {
		ms.lnMu.Lock()
		base := ms.baseLns
		ms.lnMu.Unlock()
		for _, ln := range base {
			filer, ok := ln.(interface{ File() (*os.File, error) })
			if !ok {
				return nil, files, errors.New("listener cannot be handed over")
			}
			f, err := filer.File()
			if err != nil {
				return nil, files, fmt.Errorf("duplicating listener %s: %w", ms.server.Addr, err)
			}
			byAddr[ms.server.Addr] = append(byAddr[ms.server.Addr], firstExtraFD+len(files))
			files = append(files, f)
		}
	}
	return byAddr, files, nil
}

// Inherited reports whether this process was started by a Handover.
func Inherited() bool {
	return os.Getenv(envListeners) != ""
}

// AdoptListeners makes services use the sockets passed by the previous
// process. Sockets of addresses no longer configured are closed.
func (g *Group) AdoptListeners() error {
	encoded := os.Getenv(envListeners)
	if encoded == "" {
		return nil
	}
	os.Unsetenv(envListeners)

	var byAddr map[string][]int
	if err := json.Unmarshal([]byte(encoded), &byAddr); err != nil {
		return fmt.Errorf("parsing %s: %w", envListeners, err)
	}

	for addr, fds := range byAddr {
		var target *managedServer
		for _, ms := range g.servers {
			if ms.server.Addr == addr {
				target = ms
			}
		}
		for _, fd := range fds {
			f := os.NewFile(uintptr(fd), "listener")
			if f == nil {
				return fmt.Errorf("inherited descriptor %d is invalid", fd)
			}
			ln, err := net.FileListener(f)
			f.Close()
			if err != nil {
				return fmt.Errorf("adopting listener %s: %w", addr, err)
			}
			if target == nil {
				ln.Close()
				continue
			}
			target.inherited = append(target.inherited, ln)
		}
	}
	return nil
}

// SignalReady tells the previous process that this one accepts connections.
func SignalReady() error {
	value := os.Getenv(envReadyFD)
	if value == "" {
		return nil
	}
	os.Unsetenv(envReadyFD)

	fd, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("parsing %s: %w", envReadyFD, err)
	}
	f := os.NewFile(uintptr(fd), "ready")
	if f == nil {
		return fmt.Errorf("readiness descriptor %d is invalid", fd)
	}
	defer f.Close()
	_, err = f.Write([]byte{1})
	return err
}

// Ready is closed once every service is listening.
func (g *Group) Ready() <-chan struct{} {
	ready := make(chan struct{})
	go func() {
		for _, ms := range g.servers {
			<-ms.listening
		}
		close(ready)
	}()
	return ready
}
