package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/xaaha/kapi/internal/daemon"
	"github.com/xaaha/kapi/internal/page"
)

var (
	ErrConnectionLost = errors.New("daemon connection lost")
	ErrNotRunning     = errors.New("daemon not running")
)

const noStartDeadline = time.Second

type Client struct {
	Home    string
	NoStart bool
}

func (c Client) dial(openPage string) (net.Conn, error) {
	if err := daemon.CheckSocketPath(c.Home); err != nil {
		return nil, err
	}
	sock := daemon.SocketPath(c.Home)
	if conn, err := net.Dial("unix", sock); err == nil {
		return conn, nil
	}
	if c.NoStart {
		return nil, ErrNotRunning
	}
	ctx, cancel := context.WithTimeout(context.Background(), startWait)
	defer cancel()
	if err := c.startDaemon(ctx, openPage); err != nil {
		if ctx.Err() != nil {
			return nil, c.notStarted(sock, ctx.Err())
		}
		return nil, err
	}
	for {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			return nil, c.notStarted(sock, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

const startWait = 3 * time.Second

func (c Client) notStarted(sock string, err error) error {
	return fmt.Errorf("daemon did not start within %v on %s, see %s: %w", startWait, sock, filepath.Join(c.Home, "daemon.log"), err)
}

func (c Client) startDaemon(ctx context.Context, openPage string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"daemon", "--spawn"}
	if openPage != "" {
		args = append(args, "--reopen-except", page.ID(openPage))
	}
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Env = append(os.Environ(), "KAPI_HOME="+c.Home)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("start daemon: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// Spawn starts the daemon in its own session with output to daemon.log and returns without waiting for it.
func Spawn(home, reopenExcept string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(home, "daemon.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	args := []string{"daemon", "--reopen"}
	if reopenExcept != "" {
		args = append(args, "--reopen-except", reopenExcept)
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := closeInheritedOnExec(); err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func closeInheritedOnExec() error {
	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		return err
	}
	for _, e := range entries {
		if fd, err := strconv.Atoi(e.Name()); err == nil && fd > 2 {
			syscall.CloseOnExec(fd)
		}
	}
	return nil
}

func (c Client) Do(req *daemon.Request) (daemon.Response, error) {
	openPage := ""
	if req.Op == "open" {
		openPage = req.Page
	}
	conn, err := c.dial(openPage)
	if err != nil {
		return daemon.Response{}, err
	}
	defer conn.Close()
	if c.NoStart {
		if err := conn.SetDeadline(time.Now().Add(noStartDeadline)); err != nil {
			return daemon.Response{}, err
		}
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return daemon.Response{}, err
	}
	var resp daemon.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return daemon.Response{}, fmt.Errorf("%w: %v", ErrConnectionLost, err)
	}
	if resp.Error != "" {
		return resp, fmt.Errorf("%s", resp.Error)
	}
	return resp, nil
}

// Watch streams events to fn and marks each one queued after fn returns nil.
func (c Client) Watch(page, reader string, fn func(daemon.Response) error) error {
	conn, err := c.dial("")
	if err != nil {
		return err
	}
	defer conn.Close()
	enc := json.NewEncoder(conn)
	if err := enc.Encode(daemon.Request{Op: "watch", Page: page, Reader: reader}); err != nil {
		return err
	}
	dec := json.NewDecoder(conn)
	for {
		var resp daemon.Response
		if err := dec.Decode(&resp); err != nil {
			return fmt.Errorf("%w: %v", ErrConnectionLost, err)
		}
		if resp.Error != "" {
			return fmt.Errorf("%s", resp.Error)
		}
		if err := fn(resp); err != nil {
			return err
		}
		if err := enc.Encode(daemon.Request{Op: "queue", Seq: resp.Event.Seq}); err != nil {
			return err
		}
	}
}
