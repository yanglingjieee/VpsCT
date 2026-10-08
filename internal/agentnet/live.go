package agentnet

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"ctlvps/internal/agentbudget"
	"ctlvps/internal/agentlive"
	"ctlvps/internal/safehttp"
)

// Live keeps the live worker running until ctx ends. The worker is the only
// long-lived network child: it holds the WebSocket to the controller, so the
// TLS and HTTP it parses stay out of the root agent like every other request.
// Root hands it the controller's address and the agent token and takes
// nothing back but log lines. Losing it costs the live readings only; the
// heartbeat carries on without it.
func Live(ctx context.Context, logger *slog.Logger, serverURL, token, version string) {
	in, _ := json.Marshal(agentlive.Config{URL: serverURL, Token: token, Version: version})
	wait := 5 * time.Second
	for ctx.Err() == nil {
		began := time.Now()
		err := runLive(ctx, logger, in)
		if ctx.Err() != nil {
			return
		}
		if time.Since(began) > time.Minute {
			wait = 5 * time.Second
		}
		logger.Warn("live worker stopped", "err", err, "retry_in", wait.String())
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, 5*time.Minute)
	}
}

func runLive(ctx context.Context, logger *slog.Logger, in []byte) error {
	// The kernel ties the child's parent-death signal to the thread that
	// started it, so that thread has to live as long as the child.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	cmd := exec.CommandContext(ctx, networkExecutable(), "network-live")
	cmd.Stdin = bytes.NewReader(in)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "GOMAXPROCS=1", "GOMEMLIMIT=" + agentbudget.NetworkGoLimit}
	if err := isolate(cmd); err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	// What the worker says is only ever logged, a line at a time and not often.
	lines := bufio.NewScanner(io.LimitReader(stderr, 64<<10))
	lines.Buffer(make([]byte, 0, 512), 512)
	var last time.Time
	for lines.Scan() {
		if time.Since(last) > 10*time.Second {
			last = time.Now()
			logger.Warn("live worker", "line", lines.Text())
		}
	}
	_, _ = io.Copy(io.Discard, stderr)
	return cmd.Wait()
}

func liveEntry(args []string) (bool, error) {
	if len(args) != 1 || args[0] != "network-live" {
		return false, nil
	}
	if os.Geteuid() == 0 {
		return true, errors.New("实时上报子进程不得以 root 运行")
	}
	if err := harden(); err != nil {
		return true, err
	}
	raw, err := safehttp.ReadBounded(os.Stdin, 4096)
	if err != nil {
		return true, err
	}
	var cfg agentlive.Config
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&cfg) != nil || d.Decode(new(any)) != io.EOF {
		return true, errors.New("无效的实时上报配置")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	return true, agentlive.Run(ctx, cfg)
}
