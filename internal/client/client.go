package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/xaaha/kapi/internal/daemon"
)

var ErrConnectionLost = errors.New("daemon connection lost")

type Client struct {
	Home string
}

func (c Client) dial() (net.Conn, error) {
	if err := daemon.CheckSocketPath(c.Home); err != nil {
		return nil, err
	}
	sock := daemon.SocketPath(c.Home)
	if conn, err := net.Dial("unix", sock); err == nil {
		return conn, nil
	}
	if err := c.startDaemon(); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			return conn, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("daemon did not start on %s: %w", sock, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (c Client) startDaemon() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(c.Home, 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(c.Home+"/daemon.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, "daemon")
	cmd.Env = append(os.Environ(), "KAPI_HOME="+c.Home)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func (c Client) Do(req *daemon.Request) (daemon.Response, error) {
	conn, err := c.dial()
	if err != nil {
		return daemon.Response{}, err
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return daemon.Response{}, err
	}
	var resp daemon.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return daemon.Response{}, err
	}
	if resp.Error != "" {
		return resp, fmt.Errorf("%s", resp.Error)
	}
	return resp, nil
}

// Watch streams events to fn and acknowledges each one after fn returns nil.
func (c Client) Watch(page, reader string, fn func(daemon.Response) error) error {
	conn, err := c.dial()
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
		if err := enc.Encode(daemon.Request{Op: "ack", Seq: resp.Event.Seq}); err != nil {
			return err
		}
	}
}
