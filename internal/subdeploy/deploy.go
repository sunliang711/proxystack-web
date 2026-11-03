// Package subdeploy installs, updates, rolls back, and removes the standalone subscription server.
package subdeploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"proxystack-web/internal/subserver"
	"proxystack-web/internal/version"
)

const (
	DefaultDir  = "/opt/proxystack-web-sub"
	ServiceUser = "proxystack-web-sub"
	UnitName    = "proxystack-web-sub.service"
	UnitPath    = "/etc/systemd/system/" + UnitName
	MarkerName  = ".proxystack-web-sub"
	MarkerV1    = "proxystack-web-sub-v1"
	MarkerV2    = "proxystack-web-sub-v2"

	defaultHealthAttempts = 10
	defaultHealthDelay    = time.Second
)

// Status describes the remotely inspectable subscription deployment state.
type Status struct {
	State           string `json:"state" enum:"uninstalled,retained,installed"`
	Version         string `json:"version,omitempty"`
	PreviousVersion string `json:"previous_version,omitempty"`
	ServiceActive   bool   `json:"service_active"`
	ServiceEnabled  bool   `json:"service_enabled"`
	Directory       string `json:"directory"`
	Platform        string `json:"platform"`
}

// Result is emitted by apply, rollback, and uninstall.
type Result struct {
	Action          string `json:"action"`
	Version         string `json:"version,omitempty"`
	PreviousVersion string `json:"previous_version,omitempty"`
	RolledBack      bool   `json:"rolled_back,omitempty"`
}

// ApplyOptions configures the first installation. Existing installations keep their configuration.
type ApplyOptions struct {
	Dir            string
	Source         string
	PublicKey      string
	Listen         string
	Port           int
	ExpectedSHA256 string
	Executable     string
	HealthAttempts int
	HealthDelay    time.Duration
}

type installation struct {
	dir        string
	exists     bool
	marker     string
	config     bool
	binary     bool
	unit       bool
	unitOwned  bool
	unitData   []byte
	account    bool
	active     bool
	enabled    bool
	accountUID int
	accountGID int
}

// Inspect verifies ownership before returning the current deployment state.
func Inspect(ctx context.Context, dir string) (*Status, error) {
	dir = normalizeDir(dir)
	state, err := inspectInstallation(ctx, dir)
	if err != nil {
		return nil, err
	}
	status := &Status{State: "uninstalled", Directory: dir, Platform: runtime.GOOS + "/" + runtime.GOARCH}
	if state.config {
		status.State = "retained"
	}
	if state.binary && state.unit && state.unitOwned {
		status.State = "installed"
	}
	if state.binary {
		status.Version = probeVersion(ctx, binaryPath(dir))
	}
	if _, err := os.Stat(binaryPath(dir) + ".prev"); err == nil {
		status.PreviousVersion = probeVersion(ctx, binaryPath(dir)+".prev")
	}
	status.ServiceActive = state.active
	status.ServiceEnabled = state.enabled
	return status, nil
}

// Apply installs or updates the subscription server using the currently executing binary.
func Apply(ctx context.Context, options ApplyOptions) (*Result, error) {
	if err := requireRootAndSystemd(ctx); err != nil {
		return nil, err
	}
	options.Dir = normalizeDir(options.Dir)
	unlock, err := acquireLock(options.Dir)
	if err != nil {
		return nil, err
	}
	defer unlock()

	state, err := inspectInstallation(ctx, options.Dir)
	if err != nil {
		return nil, err
	}
	executable := options.Executable
	if executable == "" {
		executable, err = os.Executable()
		if err != nil {
			return nil, fmt.Errorf("resolve deployment binary: %w", err)
		}
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return nil, fmt.Errorf("resolve deployment binary: %w", err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		return nil, fmt.Errorf("read deployment binary: %w", err)
	}
	actualSHA256 := sum(data)
	if options.ExpectedSHA256 != "" && !strings.EqualFold(options.ExpectedSHA256, actualSHA256) {
		return nil, errors.New("deployment binary SHA-256 does not match")
	}

	if !state.config {
		return install(ctx, state, options, data)
	}
	return update(ctx, state, options, data)
}

// Rollback swaps the current binary with the previous version and verifies service health.
func Rollback(ctx context.Context, dir string) (*Result, error) {
	if err := requireRootAndSystemd(ctx); err != nil {
		return nil, err
	}
	dir = normalizeDir(dir)
	unlock, err := acquireLock(dir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	state, err := inspectInstallation(ctx, dir)
	if err != nil {
		return nil, err
	}
	if !state.config || !state.binary || !state.unitOwned {
		return nil, errors.New("subscription server is not installed")
	}
	previous := binaryPath(dir) + ".prev"
	if _, err := os.Stat(previous); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("no previous subscription server version is available")
		}
		return nil, err
	}
	currentVersion := probeVersion(ctx, binaryPath(dir))
	previousVersion := probeVersion(ctx, previous)
	if err := swapFiles(binaryPath(dir), previous); err != nil {
		return nil, err
	}
	if err := restartAndCheck(ctx, dir, previousVersion, defaultHealthAttempts, defaultHealthDelay); err != nil {
		_ = swapFiles(binaryPath(dir), previous)
		_ = restartAndCheck(ctx, dir, currentVersion, defaultHealthAttempts, defaultHealthDelay)
		return nil, fmt.Errorf("rollback health check failed; restored %s: %w", currentVersion, err)
	}
	return &Result{Action: "rolled_back", Version: previousVersion, PreviousVersion: currentVersion}, nil
}

// Uninstall removes the service and binary. Without purge, configuration and subscription data are retained.
func Uninstall(ctx context.Context, dir string, purge bool) (*Result, error) {
	if err := requireRootAndSystemd(ctx); err != nil {
		return nil, err
	}
	dir = normalizeDir(dir)
	unlock, err := acquireLock(dir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	state, err := inspectInstallation(ctx, dir)
	if err != nil {
		return nil, err
	}
	if state.unitOwned {
		if _, err := run(ctx, "systemctl", "disable", "--now", UnitName); err != nil {
			return nil, err
		}
		serviceState := systemctlState(ctx, "is-active", UnitName)
		if !stoppedServiceState(serviceState) {
			return nil, fmt.Errorf("subscription service state is %q; refusing to remove it", serviceState)
		}
		if err := os.Remove(UnitPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if _, err := run(ctx, "systemctl", "daemon-reload"); err != nil {
			return nil, err
		}
	}
	removeManagedSymlink(dir)
	if purge {
		if err := os.RemoveAll(dir); err != nil {
			return nil, err
		}
		if state.account {
			_, _ = run(ctx, "userdel", ServiceUser)
		}
		return &Result{Action: "purged"}, nil
	}
	if err := os.RemoveAll(filepath.Join(dir, "bin")); err != nil {
		return nil, err
	}
	return &Result{Action: "uninstalled"}, nil
}

func stoppedServiceState(state string) bool {
	switch state {
	case "inactive", "failed", "unknown", "not-found":
		return true
	default:
		return false
	}
}

func install(ctx context.Context, state installation, options ApplyOptions, data []byte) (_ *Result, returnErr error) {
	if options.Source == "" || options.PublicKey == "" {
		return nil, errors.New("source and public key are required for the first installation")
	}
	config := subserver.DefaultConfig()
	if options.Listen != "" {
		config.Listen = options.Listen
	}
	if options.Port != 0 {
		config.Port = options.Port
	}
	config.Sources = []subserver.SourceConfig{{Name: options.Source, PublicKey: options.PublicKey, Primary: true}}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	createdAccount := false
	createdDir := false
	defer func() {
		if returnErr == nil {
			return
		}
		_, _ = run(context.Background(), "systemctl", "disable", "--now", UnitName)
		_ = os.Remove(UnitPath)
		_, _ = run(context.Background(), "systemctl", "daemon-reload")
		if createdDir {
			_ = os.RemoveAll(options.Dir)
		}
		if createdAccount {
			_, _ = run(context.Background(), "userdel", ServiceUser)
		}
	}()

	if !state.account {
		if _, err := run(ctx, "useradd", "--system", "--home-dir", options.Dir, "--no-create-home", "--shell", "/usr/sbin/nologin", ServiceUser); err != nil {
			return nil, err
		}
		createdAccount = true
	}
	uid, gid, err := accountIDs(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(options.Dir); errors.Is(err, os.ErrNotExist) {
		createdDir = true
	}
	for _, item := range []struct {
		path       string
		mode       fs.FileMode
		owner, gid int
	}{
		{options.Dir, 0o750, 0, gid}, {filepath.Join(options.Dir, "bin"), 0o750, 0, gid},
		{filepath.Join(options.Dir, "data"), 0o750, uid, gid}, {filepath.Join(options.Dir, "inbox"), 0o2770, uid, gid},
		{filepath.Join(options.Dir, "inbox", "rejected"), 0o750, uid, gid},
	} {
		if err := os.MkdirAll(item.path, item.mode); err != nil {
			return nil, err
		}
		if err := os.Chmod(item.path, item.mode); err != nil {
			return nil, err
		}
		if err := os.Chown(item.path, item.owner, item.gid); err != nil {
			return nil, err
		}
	}
	if err := installBinary(binaryPath(options.Dir), data, 0, gid); err != nil {
		return nil, err
	}
	if err := subserver.SaveConfig(subserver.Layout{Dir: options.Dir}, config); err != nil {
		return nil, err
	}
	if err := writeMarker(options.Dir); err != nil {
		return nil, err
	}
	if err := writeUnit(options.Dir); err != nil {
		return nil, err
	}
	if _, err := run(ctx, "systemctl", "daemon-reload"); err != nil {
		return nil, err
	}
	if _, err := run(ctx, "systemctl", "enable", "--now", UnitName); err != nil {
		return nil, err
	}
	linkManagedBinary(options.Dir)
	candidate := version.Current().Version
	if err := waitHealth(ctx, options.Dir, candidate, attempts(options), delay(options)); err != nil {
		return nil, err
	}
	return &Result{Action: "installed", Version: candidate}, nil
}

func update(ctx context.Context, state installation, options ApplyOptions, data []byte) (*Result, error) {
	if !state.account {
		return nil, fmt.Errorf("service account %s does not exist", ServiceUser)
	}
	_, gid, err := accountIDs(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := subserver.LoadConfig(subserver.Layout{Dir: options.Dir}); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(options.Dir, "bin"), 0o750); err != nil {
		return nil, err
	}
	if err := os.Chown(filepath.Join(options.Dir, "bin"), 0, gid); err != nil {
		return nil, err
	}
	target := binaryPath(options.Dir)
	staged := target + ".new"
	if err := writeFile(staged, data, 0o755, 0, gid); err != nil {
		return nil, err
	}
	defer os.Remove(staged)
	if output, err := exec.CommandContext(ctx, staged, "--version").CombinedOutput(); err != nil {
		return nil, fmt.Errorf("new subscription server binary does not run: %s", strings.TrimSpace(string(output)))
	}
	if output, err := exec.CommandContext(ctx, staged, "sub", "--dir", options.Dir, "config").CombinedOutput(); err != nil {
		return nil, fmt.Errorf("new subscription server binary rejects the current configuration: %s", strings.TrimSpace(string(output)))
	}
	candidate := version.Current().Version
	current := ""
	if state.binary {
		current = probeVersion(ctx, target)
		currentData, readErr := os.ReadFile(target)
		if readErr == nil && bytes.Equal(currentData, data) {
			if err := writeMarker(options.Dir); err != nil {
				return nil, err
			}
			if err := writeUnit(options.Dir); err != nil {
				return nil, err
			}
			if _, err := run(ctx, "systemctl", "daemon-reload"); err != nil {
				return nil, err
			}
			if _, err := run(ctx, "systemctl", "enable", "--now", UnitName); err != nil {
				return nil, err
			}
			if err := waitHealth(ctx, options.Dir, candidate, attempts(options), delay(options)); err != nil {
				return nil, err
			}
			return &Result{Action: "unchanged", Version: candidate, PreviousVersion: probeVersion(ctx, target+".prev")}, nil
		}
	}
	previous := target + ".prev"
	savedPrevious := previous + ".saved"
	_ = os.Remove(savedPrevious)
	hadPrevious := false
	if _, err := os.Stat(previous); err == nil {
		if err := os.Rename(previous, savedPrevious); err != nil {
			return nil, err
		}
		hadPrevious = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if state.binary {
		if err := os.Rename(target, previous); err != nil {
			if hadPrevious {
				_ = os.Rename(savedPrevious, previous)
			}
			return nil, err
		}
	}
	if err := os.Rename(staged, target); err != nil {
		return nil, rollbackUpdate(ctx, state, target, previous, savedPrevious, hadPrevious, err)
	}
	if err := writeMarker(options.Dir); err != nil {
		return nil, rollbackUpdate(ctx, state, target, previous, savedPrevious, hadPrevious, err)
	}
	if err := writeUnit(options.Dir); err != nil {
		return nil, rollbackUpdate(ctx, state, target, previous, savedPrevious, hadPrevious, err)
	}
	if _, err := run(ctx, "systemctl", "daemon-reload"); err != nil {
		return nil, rollbackUpdate(ctx, state, target, previous, savedPrevious, hadPrevious, err)
	}
	if _, err := run(ctx, "systemctl", "enable", UnitName); err != nil {
		return nil, rollbackUpdate(ctx, state, target, previous, savedPrevious, hadPrevious, err)
	}
	if err := restartAndCheck(ctx, options.Dir, candidate, attempts(options), delay(options)); err != nil {
		rollbackErr := rollbackUpdate(ctx, state, target, previous, savedPrevious, hadPrevious, err)
		return nil, fmt.Errorf("subscription server update failed and was rolled back: %w", rollbackErr)
	}
	_ = os.Remove(savedPrevious)
	linkManagedBinary(options.Dir)
	return &Result{Action: "updated", Version: candidate, PreviousVersion: current}, nil
}

func inspectInstallation(ctx context.Context, dir string) (installation, error) {
	state := installation{dir: dir}
	if info, err := os.Lstat(dir); err == nil {
		state.exists = true
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return state, fmt.Errorf("refusing non-directory or symbolic link: %s", dir)
		}
		if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("refusing symbolic link inside %s: %s", dir, path)
			}
			return nil
		}); err != nil {
			return state, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return state, err
	}
	markerData, markerErr := os.ReadFile(filepath.Join(dir, MarkerName))
	if markerErr == nil {
		state.marker = strings.TrimSpace(string(markerData))
		if state.marker != MarkerV1 && state.marker != MarkerV2 {
			return state, fmt.Errorf("cannot confirm ownership of %s", dir)
		}
	} else if !errors.Is(markerErr, os.ErrNotExist) {
		return state, markerErr
	}
	_, configErr := os.Stat(filepath.Join(dir, "config.yaml"))
	state.config = configErr == nil
	if configErr != nil && !errors.Is(configErr, os.ErrNotExist) {
		return state, configErr
	}
	_, binaryErr := os.Stat(binaryPath(dir))
	state.binary = binaryErr == nil
	if binaryErr != nil && !errors.Is(binaryErr, os.ErrNotExist) {
		return state, binaryErr
	}
	loadState := systemctlState(ctx, "show", "--value", "-p", "LoadState", UnitName)
	state.unit = loadState != "" && loadState != "not-found"
	if state.unit {
		output, err := run(ctx, "systemctl", "show", "--value", "-p", "ExecStart", UnitName)
		if err != nil {
			return state, err
		}
		expected := binaryPath(dir) + " sub --dir " + dir + " serve"
		state.unitOwned = strings.Contains(string(output), "argv[]="+expected+" ;")
		if !state.unitOwned {
			return state, fmt.Errorf("%s belongs to another installation; refusing to modify it", UnitName)
		}
		info, err := os.Lstat(UnitPath)
		if err != nil {
			return state, err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return state, fmt.Errorf("refusing non-regular or symbolic link: %s", UnitPath)
		}
		state.unitData, err = os.ReadFile(UnitPath)
		if err != nil {
			return state, err
		}
	}
	account, uid, gid, home, err := lookupAccount(ctx)
	if err != nil {
		return state, err
	}
	state.account, state.accountUID, state.accountGID = account, uid, gid
	if account && home != dir {
		return state, fmt.Errorf("service account %s has an unexpected home directory", ServiceUser)
	}
	state.active = systemctlState(ctx, "is-active", UnitName) == "active"
	state.enabled = systemctlState(ctx, "is-enabled", UnitName) == "enabled"
	if (state.exists || state.config || state.binary || state.unit) && state.marker == "" {
		return state, fmt.Errorf("cannot confirm ownership of %s", dir)
	}
	if state.marker != "" && !state.config {
		return state, fmt.Errorf("%s has no configuration; refusing an incomplete installation", dir)
	}
	if state.config {
		if _, err := subserver.LoadConfig(subserver.Layout{Dir: dir}); err != nil {
			return state, err
		}
	}
	return state, nil
}

func requireRootAndSystemd(ctx context.Context) error {
	if runtime.GOOS != "linux" {
		return errors.New("subscription deployment requires Linux")
	}
	if os.Geteuid() != 0 {
		return errors.New("subscription deployment must run as root")
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return errors.New("systemd is not the running init system")
	}
	if _, err := run(ctx, "systemctl", "--version"); err != nil {
		return errors.New("systemctl is required")
	}
	return nil
}

func acquireLock(dir string) (func(), error) {
	if err := os.MkdirAll("/run/lock", 0o755); err != nil {
		return nil, err
	}
	name := strings.NewReplacer("/", "_", "\\", "_").Replace(dir)
	file, err := os.OpenFile(filepath.Join("/run/lock", "proxystack-web-sub"+name+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, errors.New("another subscription deployment is already running")
	}
	return func() {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
	}, nil
}

func writeUnit(dir string) error {
	return writeFile(UnitPath, []byte(unitFile(dir)), 0o644, 0, 0)
}

func unitFile(dir string) string {
	return `[Unit]
Description=proxystack-web subscription server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=proxystack-web-sub
Group=proxystack-web-sub
ExecStart=` + binaryPath(dir) + ` sub --dir ` + dir + ` serve
WorkingDirectory=` + dir + `
UMask=0027
Restart=on-failure
RestartSec=2
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
PrivateDevices=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
ReadWritePaths=` + dir + `

[Install]
WantedBy=multi-user.target
`
}

func writeMarker(dir string) error {
	return writeFile(filepath.Join(dir, MarkerName), []byte(MarkerV2+"\n"), 0o644, 0, 0)
}

func installBinary(path string, data []byte, uid, gid int) error {
	return writeFile(path, data, 0o755, uid, gid)
}

func writeFile(path string, data []byte, mode fs.FileMode, uid, gid int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	cleanup := func(cause error) error {
		_ = file.Close()
		_ = os.Remove(temporary)
		return cause
	}
	if _, err := file.Write(data); err != nil {
		return cleanup(err)
	}
	if err := file.Chmod(mode); err != nil {
		return cleanup(err)
	}
	if err := file.Chown(uid, gid); err != nil {
		return cleanup(err)
	}
	if err := file.Sync(); err != nil {
		return cleanup(err)
	}
	if err := file.Close(); err != nil {
		return cleanup(err)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func restoreBinary(target, previous string, hadCurrent bool, cause error) error {
	_ = os.Remove(target)
	if hadCurrent {
		if err := os.Rename(previous, target); err != nil {
			return fmt.Errorf("%v; restore previous binary: %w", cause, err)
		}
	}
	return cause
}

func rollbackUpdate(_ context.Context, state installation, target, previous, savedPrevious string, hadPrevious bool, cause error) error {
	rollbackCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rollbackErrors := []error{}
	if err := restoreBinary(target, previous, state.binary, cause); err != nil && !errors.Is(err, cause) {
		rollbackErrors = append(rollbackErrors, err)
	}
	if hadPrevious {
		_ = os.Remove(previous)
		if err := os.Rename(savedPrevious, previous); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("restore previous rollback binary: %w", err))
		}
	} else {
		_ = os.Remove(savedPrevious)
	}
	if !state.binary {
		_ = os.RemoveAll(filepath.Dir(target))
	}
	if !state.unitOwned {
		_, _ = run(rollbackCtx, "systemctl", "disable", "--now", UnitName)
		if err := os.Remove(UnitPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			rollbackErrors = append(rollbackErrors, err)
		}
	} else if err := writeFile(UnitPath, state.unitData, 0o644, 0, 0); err != nil {
		rollbackErrors = append(rollbackErrors, fmt.Errorf("restore systemd unit: %w", err))
	}
	if _, err := run(rollbackCtx, "systemctl", "daemon-reload"); err != nil {
		rollbackErrors = append(rollbackErrors, err)
	}
	if state.unitOwned {
		if state.enabled {
			if _, err := run(rollbackCtx, "systemctl", "enable", UnitName); err != nil {
				rollbackErrors = append(rollbackErrors, err)
			}
		} else if _, err := run(rollbackCtx, "systemctl", "disable", UnitName); err != nil {
			rollbackErrors = append(rollbackErrors, err)
		}
		if state.active {
			if _, err := run(rollbackCtx, "systemctl", "restart", UnitName); err != nil {
				rollbackErrors = append(rollbackErrors, err)
			}
		} else if _, err := run(rollbackCtx, "systemctl", "stop", UnitName); err != nil {
			rollbackErrors = append(rollbackErrors, err)
		}
	}
	if len(rollbackErrors) > 0 {
		return fmt.Errorf("%w; rollback errors: %v", cause, errors.Join(rollbackErrors...))
	}
	return cause
}

func swapFiles(first, second string) error {
	temporary := first + ".swap"
	_ = os.Remove(temporary)
	if err := os.Rename(first, temporary); err != nil {
		return err
	}
	if err := os.Rename(second, first); err != nil {
		_ = os.Rename(temporary, first)
		return err
	}
	if err := os.Rename(temporary, second); err != nil {
		return err
	}
	return nil
}

func restartAndCheck(ctx context.Context, dir, expectedVersion string, healthAttempts int, healthDelay time.Duration) error {
	if _, err := run(ctx, "systemctl", "restart", UnitName); err != nil {
		return err
	}
	return waitHealth(ctx, dir, expectedVersion, healthAttempts, healthDelay)
}

func waitHealth(ctx context.Context, dir, expectedVersion string, healthAttempts int, healthDelay time.Duration) error {
	config, err := subserver.LoadConfig(subserver.Layout{Dir: dir})
	if err != nil {
		return err
	}
	host := config.Listen
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	endpoint := "http://" + net.JoinHostPort(host, strconv.Itoa(config.Port)) + "/health"
	client := &http.Client{Timeout: 2 * time.Second}
	var lastErr error
	for attempt := 0; attempt < healthAttempts; attempt++ {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		response, requestErr := client.Do(request)
		if requestErr == nil {
			var health struct {
				Status  string `json:"status"`
				Version string `json:"version"`
			}
			decodeErr := json.NewDecoder(response.Body).Decode(&health)
			_ = response.Body.Close()
			if (response.StatusCode == http.StatusOK || response.StatusCode == http.StatusServiceUnavailable) && decodeErr == nil &&
				(health.Status == "ok" || health.Status == "degraded" || health.Status == "unavailable") &&
				(expectedVersion == "" || health.Version == expectedVersion) {
				return nil
			}
			lastErr = fmt.Errorf("health returned HTTP %d, status %q, version %q", response.StatusCode, health.Status, health.Version)
		} else {
			lastErr = requestErr
		}
		if attempt+1 < healthAttempts {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(healthDelay):
			}
		}
	}
	return fmt.Errorf("subscription service did not become healthy: %w", lastErr)
}

func probeVersion(ctx context.Context, path string) string {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(probeCtx, path, "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(output))
	for index, field := range fields {
		if field == "version" && index+1 < len(fields) {
			return strings.TrimSpace(fields[index+1])
		}
	}
	return ""
}

func lookupAccount(ctx context.Context) (bool, int, int, string, error) {
	output, err := run(ctx, "getent", "passwd", ServiceUser)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
			return false, 0, 0, "", nil
		}
		return false, 0, 0, "", err
	}
	fields := strings.Split(strings.TrimSpace(string(output)), ":")
	if len(fields) < 7 {
		return false, 0, 0, "", errors.New("invalid service account entry")
	}
	uid, err := strconv.Atoi(fields[2])
	if err != nil {
		return false, 0, 0, "", err
	}
	gid, err := strconv.Atoi(fields[3])
	if err != nil {
		return false, 0, 0, "", err
	}
	return true, uid, gid, fields[5], nil
}

func accountIDs(ctx context.Context) (int, int, error) {
	found, uid, gid, _, err := lookupAccount(ctx)
	if err != nil {
		return 0, 0, err
	}
	if !found {
		return 0, 0, fmt.Errorf("service account %s does not exist", ServiceUser)
	}
	return uid, gid, nil
}

func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s failed: %s: %w", name, strings.TrimSpace(string(output)), err)
	}
	return output, nil
}

func systemctlState(ctx context.Context, args ...string) string {
	output, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
	if err != nil && len(output) == 0 {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func linkManagedBinary(dir string) {
	const link = "/usr/local/bin/proxystack-web"
	info, err := os.Lstat(link)
	if errors.Is(err, os.ErrNotExist) {
		_ = os.Symlink(binaryPath(dir), link)
		return
	}
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		target, readErr := os.Readlink(link)
		if readErr == nil && strings.Contains(target, "proxystack-web-sub") {
			_ = os.Remove(link)
			_ = os.Symlink(binaryPath(dir), link)
		}
	}
}

func removeManagedSymlink(dir string) {
	const link = "/usr/local/bin/proxystack-web"
	if target, err := os.Readlink(link); err == nil && target == binaryPath(dir) {
		_ = os.Remove(link)
	}
}

func binaryPath(dir string) string { return filepath.Join(dir, "bin", "proxystack-web") }

func normalizeDir(dir string) string {
	if dir == "" {
		return DefaultDir
	}
	return filepath.Clean(dir)
}

func sum(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func attempts(options ApplyOptions) int {
	if options.HealthAttempts > 0 {
		return options.HealthAttempts
	}
	return defaultHealthAttempts
}

func delay(options ApplyOptions) time.Duration {
	if options.HealthDelay > 0 {
		return options.HealthDelay
	}
	return defaultHealthDelay
}
