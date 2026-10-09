// Package firefox prepares an isolated Firefox profile for lemmingd.
package firefox

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const certificateNickname = "lemmingd local CA"
const launcherLockName = ".lemmingd-firefox.lock"
const preparationLockName = ".lemmingd-firefox.prepare"

const firefoxProfileLockTimeout = 5 * time.Second
const firefoxProfileLockPollInterval = 10 * time.Millisecond

// Config identifies the Firefox profile and proxy that Firefox should use.
type Config struct {
	Profile       string
	CACertificate string
	ProxyAddress  string
	URL           string
	Binary        string
	NSSCertutil   string
}

type commandRunner interface {
	LookPath(string) (string, error)
	Run(string, ...string) ([]byte, error)
	Start(string, ...string) (startedCommand, error)
}

type startedCommand struct {
	pid    int
	exited <-chan error
	kill   func() error
}

type systemRunner struct{}

func (systemRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (systemRunner) Run(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

func (systemRunner) Start(name string, args ...string) (startedCommand, error) {
	command := exec.Command(name, args...)
	if err := command.Start(); err != nil {
		return startedCommand{}, err
	}
	exited := make(chan error, 1)
	go func() {
		exited <- command.Wait()
		close(exited)
	}()
	return startedCommand{
		pid:    command.Process.Pid,
		exited: exited,
		kill:   command.Process.Kill,
	}, nil
}

// Launch prepares the dedicated profile, imports the public CA, and starts an
// isolated Firefox instance. It never modifies Firefox's default profile.
func Launch(config Config) error {
	return prepareAndLaunch(config, systemRunner{})
}

func prepareAndLaunch(config Config, runner commandRunner) error {
	if err := validate(config); err != nil {
		return err
	}
	certutil, err := findCertutil(config.NSSCertutil, runner)
	if err != nil {
		return err
	}
	firefox, err := findFirefox(config.Binary, runner)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(config.Profile, 0o700); err != nil {
		return fmt.Errorf("create Firefox profile: %w", err)
	}
	if err := os.Chmod(config.Profile, 0o700); err != nil {
		return fmt.Errorf("protect Firefox profile: %w", err)
	}
	preparation, err := acquirePreparationLock(config.Profile)
	if err != nil {
		return err
	}
	defer func() { _ = preparation.remove() }()
	if err := checkFirefoxProfileLock(config.Profile); err != nil {
		return err
	}
	lease, err := acquireProfileLease(config.Profile)
	if err != nil {
		return err
	}
	keepLease := false
	defer func() {
		if !keepLease {
			_ = lease.remove()
		}
	}()
	if err := writePreferences(config.Profile, config.ProxyAddress); err != nil {
		return err
	}

	database := "sql:" + config.Profile
	if _, err := os.Stat(filepath.Join(config.Profile, "cert9.db")); errors.Is(err, os.ErrNotExist) {
		if output, err := runner.Run(certutil, "-N", "-d", database, "--empty-password"); err != nil {
			return commandError("initialize Firefox certificate database", err, output)
		}
	} else if err != nil {
		return fmt.Errorf("inspect Firefox certificate database: %w", err)
	}
	// A CA may have been regenerated since the previous launch. Removing the
	// old nickname is harmless when it is absent and prevents stale trust.
	_, _ = runner.Run(certutil, "-D", "-d", database, "-n", certificateNickname)
	if output, err := runner.Run(certutil, "-A", "-d", database, "-n", certificateNickname,
		"-t", "C,,", "-i", config.CACertificate); err != nil {
		return commandError("trust lemmingd CA in Firefox profile", err, output)
	}
	command, err := runner.Start(firefox, "-no-remote", "-profile", config.Profile, config.URL)
	if err != nil {
		return fmt.Errorf("start Firefox: %w", err)
	}
	// Keep the preparation lock until Firefox has created its own native
	// profile lock. This prevents a second launcher from treating our lease as
	// stale and changing the profile while Firefox is still starting.
	if err := waitForFirefoxProfileLock(config.Profile, command.exited); err != nil {
		return errors.Join(err, stopCommand(command))
	}
	if err := lease.update(command.pid); err != nil {
		return errors.Join(fmt.Errorf("record Firefox process: %w", err), stopCommand(command))
	}
	keepLease = true
	go func() {
		<-command.exited
		removeExitedLease(config.Profile, lease)
	}()
	return nil
}

func stopCommand(command startedCommand) error {
	killErr := command.kill()
	if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
		return fmt.Errorf("stop Firefox after launch failure: %w", killErr)
	}
	<-command.exited
	return nil
}

func waitForFirefoxProfileLock(profile string, exited <-chan error) error {
	timeout := time.NewTimer(firefoxProfileLockTimeout)
	defer timeout.Stop()
	poll := time.NewTicker(firefoxProfileLockPollInterval)
	defer poll.Stop()
	for {
		select {
		case err := <-exited:
			if err != nil {
				return fmt.Errorf("Firefox exited during startup: %w", err)
			}
			return errors.New("Firefox exited during startup")
		default:
		}
		locked, err := firefoxProfileLocked(profile)
		if err != nil {
			return err
		}
		if locked {
			return nil
		}
		select {
		case err := <-exited:
			if err != nil {
				return fmt.Errorf("Firefox exited during startup: %w", err)
			}
			return errors.New("Firefox exited during startup")
		case <-timeout.C:
			return errors.New("Firefox did not lock its managed profile during startup")
		case <-poll.C:
		}
	}
}

func commandError(action string, err error, output []byte) error {
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %w: %s", action, err, detail)
}

func findCertutil(binary string, runner commandRunner) (string, error) {
	if binary != "" {
		return binary, nil
	}
	if runtime.GOOS == "windows" {
		if path, err := runner.LookPath("nss-certutil"); err == nil {
			return path, nil
		}
		return "", errors.New("NSS certutil was not found; pass -nss-certutil PATH (Windows certutil.exe is not compatible)")
	}
	if path, err := runner.LookPath("certutil"); err == nil {
		return path, nil
	}
	return "", errors.New("NSS certutil was not found; install nss-tools or libnss3-tools, or pass -nss-certutil PATH")
}

func validate(config Config) error {
	if config.Profile == "" || config.CACertificate == "" || config.ProxyAddress == "" || config.URL == "" {
		return errors.New("Firefox profile, CA certificate, proxy address, and URL are required")
	}
	if info, err := os.Stat(config.CACertificate); err != nil {
		return fmt.Errorf("read CA certificate: %w", err)
	} else if info.IsDir() {
		return errors.New("CA certificate is a directory")
	}
	// The host from ProxyAddress is written into Firefox's user.js via
	// strconv.Quote. Go and JavaScript quoting are compatible for IP addresses
	// and plain hostnames; writePreferences normalises unspecified addresses to
	// 127.0.0.1 / ::1 before quoting, so exotic characters cannot appear here.
	if _, _, err := net.SplitHostPort(config.ProxyAddress); err != nil {
		return fmt.Errorf("invalid proxy address %q: %w", config.ProxyAddress, err)
	}
	parsed, err := url.ParseRequestURI(config.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("Firefox URL must be an absolute HTTP or HTTPS URL: %q", config.URL)
	}
	return nil
}

func findFirefox(binary string, runner commandRunner) (string, error) {
	if binary != "" {
		return binary, nil
	}
	if path, err := runner.LookPath("firefox"); err == nil {
		return path, nil
	}
	for _, path := range firefoxCandidates(runtime.GOOS, os.Getenv, os.UserHomeDir) {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", errors.New("Firefox was not found; install it or pass -firefox-bin PATH")
}

func firefoxCandidates(goos string, getenv func(string) string, userHomeDir func() (string, error)) []string {
	switch goos {
	case "darwin":
		candidates := []string{"/Applications/Firefox.app/Contents/MacOS/firefox"}
		if home, err := userHomeDir(); err == nil {
			candidates = append(candidates, filepath.Join(home, "Applications", "Firefox.app", "Contents", "MacOS", "firefox"))
		}
		return candidates
	case "windows":
		var candidates []string
		for _, variable := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
			if base := getenv(variable); base != "" {
				candidates = append(candidates, filepath.Join(base, "Mozilla Firefox", "firefox.exe"))
			}
		}
		return candidates
	default:
		return nil
	}
}

type profileLease struct {
	path  string
	state string
	token string
}

func acquireProfileLease(profile string) (*profileLease, error) {
	path := filepath.Join(profile, launcherLockName)
	token, err := newLeaseToken()
	if err != nil {
		return nil, fmt.Errorf("create Firefox profile lease token: %w", err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			lease := &profileLease{path: path, state: "preparing", token: token}
			if _, err := fmt.Fprint(file, lease.contents(os.Getpid())); err != nil {
				file.Close()
				_ = os.Remove(path)
				return nil, fmt.Errorf("write Firefox profile lease: %w", err)
			}
			if err := file.Close(); err != nil {
				_ = os.Remove(path)
				return nil, fmt.Errorf("close Firefox profile lease: %w", err)
			}
			return lease, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("create Firefox profile lease: %w", err)
		}
		// The preparation directory serializes launchers. A live Firefox has one
		// of the native profile locks checked above, so a leftover launcher lease
		// is safe to replace without trusting a reusable PID.
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("remove stale Firefox profile lease: %w", err)
		}
	}
	return nil, fmt.Errorf("could not acquire Firefox profile lease %q", path)
}

type preparationLock struct {
	path string
}

func acquirePreparationLock(profile string) (*preparationLock, error) {
	path := filepath.Join(profile, preparationLockName)
	if err := os.Mkdir(path, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("Firefox profile %q is already being prepared; wait for the other lemmingd process to finish", profile)
		}
		return nil, fmt.Errorf("protect Firefox profile preparation: %w", err)
	}
	return &preparationLock{path: path}, nil
}

func (lock *preparationLock) remove() error {
	return os.Remove(lock.path)
}

func newLeaseToken() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func parseLease(data []byte) (string, int, string, error) {
	parts := strings.Fields(string(data))
	if len(parts) == 1 {
		pid, err := strconv.Atoi(parts[0])
		if err != nil || pid < 1 {
			return "", 0, "", errors.New("invalid lease contents")
		}
		return "preparing", pid, "", nil // Pre-0.1.3 development builds.
	}
	if len(parts) != 3 || (parts[0] != "preparing" && parts[0] != "running") {
		return "", 0, "", errors.New("invalid lease contents")
	}
	pid, err := strconv.Atoi(parts[1])
	if err != nil || pid < 1 || parts[2] == "" {
		return "", 0, "", errors.New("invalid lease contents")
	}
	return parts[0], pid, parts[2], nil
}

// checkFirefoxProfileLock catches a profile opened outside lemmingd. Firefox
// creates these locks itself; unlike the launcher lease, they also cover a
// browser started manually with -profile.
func checkFirefoxProfileLock(profile string) error {
	locked, err := firefoxProfileLocked(profile)
	if err != nil {
		return err
	}
	if locked {
		return fmt.Errorf("Firefox profile %q appears to be in use; close Firefox first", profile)
	}
	return nil
}

func firefoxProfileLocked(profile string) (bool, error) {
	for _, name := range []string{"lock", ".parentlock", "parent.lock"} {
		_, err := os.Lstat(filepath.Join(profile, name))
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("inspect Firefox profile lock %q (%s): %w", profile, name, err)
		}
	}
	return false, nil
}

func (lease *profileLease) update(pid int) error {
	lease.state = "running"
	return os.WriteFile(lease.path, []byte(lease.contents(pid)), 0o600)
}

func (lease *profileLease) remove() error {
	data, err := os.ReadFile(lease.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, _, token, err := parseLease(data)
	if err != nil || token != lease.token {
		return nil
	}
	return os.Remove(lease.path)
}

func removeExitedLease(profile string, lease *profileLease) {
	// Launchers only inspect or replace the lease while holding this lock. Take
	// it here as well so the token check and removal cannot race with a new
	// launcher replacing the lease. If another launcher already owns the lock,
	// it will remove the stale lease itself.
	preparation, err := acquirePreparationLock(profile)
	if err != nil {
		return
	}
	defer func() { _ = preparation.remove() }()
	_ = lease.remove()
}

func (lease *profileLease) contents(pid int) string {
	return fmt.Sprintf("%s %d %s\n", lease.state, pid, lease.token)
}

func writePreferences(profile, proxyAddress string) error {
	host, portText, err := net.SplitHostPort(proxyAddress)
	if err != nil {
		return fmt.Errorf("parse proxy address: %w", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("parse proxy port %q", portText)
	}
	if host == "" {
		// A wildcard listen address such as :18080 is valid when remote clients
		// are explicitly enabled. Firefox needs a concrete local destination.
		host = "127.0.0.1"
	} else if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		if ip.To4() != nil {
			host = "127.0.0.1"
		} else {
			host = "::1"
		}
	}
	prefs := fmt.Sprintf(`// Managed by lemmingd. Do not edit while Firefox is running.
user_pref("network.proxy.type", 1);
user_pref("network.proxy.share_proxy_settings", true);
user_pref("network.proxy.http", %s);
user_pref("network.proxy.http_port", %d);
user_pref("network.proxy.ssl", %s);
user_pref("network.proxy.ssl_port", %d);
user_pref("network.proxy.no_proxies_on", "localhost, 127.0.0.1, ::1");
`, strconv.Quote(host), port, strconv.Quote(host), port)
	if err := os.WriteFile(filepath.Join(profile, "user.js"), []byte(prefs), 0o600); err != nil {
		return fmt.Errorf("write Firefox proxy preferences: %w", err)
	}
	return nil
}
