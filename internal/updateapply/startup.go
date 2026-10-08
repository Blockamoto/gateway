package updateapply

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const ackPathEnv = "GATEWAY_UPDATE_ACK_PATH"
const ackTokenEnv = "GATEWAY_UPDATE_ACK_TOKEN"

type startupAck struct {
	Token   string `json:"token"`
	PID     int    `json:"pid"`
	Version string `json:"version"`
}
type startedRuntime struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

func startRuntime(plan *Plan, ack bool) (*startedRuntime, error) {
	command := exec.Command(filepath.Join(plan.InstallDir, RuntimeName(plan.Platform)), plan.RestartArgs...)
	command.Dir = plan.InstallDir
	hideWindow(command)
	command.Env = []string{}
	for _, entry := range os.Environ() {
		name := strings.SplitN(entry, "=", 2)[0]
		if !strings.EqualFold(name, ackPathEnv) && !strings.EqualFold(name, ackTokenEnv) {
			command.Env = append(command.Env, entry)
		}
	}
	if ack {
		command.Env = append(command.Env, ackPathEnv+"="+filepath.Join(plan.WorkDir, "startup-ack.json"), ackTokenEnv+"="+plan.AckToken)
	}
	if e := command.Start(); e != nil {
		return nil, e
	}
	started := &startedRuntime{cmd: command, done: make(chan struct{})}
	go func() { started.err = command.Wait(); close(started.done) }()
	return started, nil
}

// AcknowledgeStartup is called after the initialized runtime's HTTP server is
// serving. A helper also verifies its PID/version via the authenticated local
// runtime API, so merely copying a stale acknowledgement is insufficient.
func AcknowledgeStartup(dataDir, version string) error {
	path := os.Getenv(ackPathEnv)
	token := os.Getenv(ackTokenEnv)
	if path == "" && token == "" {
		return nil
	}
	if len(token) != 64 || !filepath.IsAbs(path) || !within(filepath.Join(dataDir, "updates", "apply"), path) || filepath.Base(path) != "startup-ack.json" {
		return fmt.Errorf("invalid update startup acknowledgement")
	}
	if e := CheckPath(path); e != nil {
		return e
	}
	b, e := json.Marshal(startupAck{token, os.Getpid(), version})
	if e != nil {
		return e
	}
	return writePrivate(path, b)
}

func waitHealthy(ctx context.Context, plan *Plan, started *startedRuntime) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-started.done:
			return fmt.Errorf("updated client exited before healthy startup: %v", started.err)
		default:
		}
		b, e := readSmall(filepath.Join(plan.WorkDir, "startup-ack.json"), 64<<10)
		if e == nil {
			var ack startupAck
			if json.Unmarshal(b, &ack) == nil && ack.PID == started.cmd.Process.Pid && ack.Version == plan.Version && subtle.ConstantTimeCompare([]byte(ack.Token), []byte(plan.AckToken)) == 1 {
				if e = runtimeHealthy(plan.DataDir, ack.PID, ack.Version); e == nil {
					// Allow early crashes following the handshake to trigger rollback too.
					select {
					case <-started.done:
						return fmt.Errorf("updated client exited after startup: %v", started.err)
					case <-time.After(600 * time.Millisecond):
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("updated client did not acknowledge healthy startup: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
func runtimeHealthy(dataDir string, pid int, version string) error {
	b, e := readSmall(filepath.Join(dataDir, "runtime.json"), 64<<10)
	if e != nil {
		return e
	}
	var info struct {
		URL, Token, Version string
		PID                 int
	}
	if e = json.Unmarshal(b, &info); e != nil {
		return e
	}
	if info.PID != pid || info.Version != version || len(info.Token) < 16 {
		return fmt.Errorf("runtime identity mismatch")
	}
	base, e := url.Parse(info.URL)
	if e != nil || base.Scheme != "http" || base.User != nil || base.Path != "" || base.RawQuery != "" || base.Fragment != "" || (base.Hostname() != "127.0.0.1" && base.Hostname() != "localhost") {
		return fmt.Errorf("unsafe runtime health URL")
	}
	port, e := strconv.Atoi(base.Port())
	if e != nil || port <= 0 || port > 65535 {
		return fmt.Errorf("invalid runtime health port")
	}
	req, e := http.NewRequest("GET", info.URL+"/api/v1/runtime/ping", nil)
	if e != nil {
		return e
	}
	req.Header.Set("X-Gateway-Token", info.Token)
	client := &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("health redirect refused") }}
	response, e := client.Do(req)
	if e != nil {
		return e
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("runtime health status %d", response.StatusCode)
	}
	var pong struct {
		OK      bool   `json:"ok"`
		Version string `json:"version"`
		PID     int    `json:"pid"`
	}
	if e = json.NewDecoder(http.MaxBytesReader(nil, response.Body, 64<<10)).Decode(&pong); e != nil {
		return e
	}
	if !pong.OK || pong.PID != pid || pong.Version != version {
		return fmt.Errorf("runtime health response identity mismatch")
	}
	return nil
}
