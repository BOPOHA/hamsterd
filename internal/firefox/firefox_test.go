package firefox

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type recordedCommand struct {
	name string
	args []string
}

type fakeRunner struct {
	paths      map[string]string
	runs       []recordedCommand
	starts     []recordedCommand
	runError   error
	runOutput  []byte
	startPID   int
	startError error
	startExit  chan error
	killed     bool
	createLock bool
	onStart    func([]string)
}

func (runner *fakeRunner) LookPath(name string) (string, error) {
	if path := runner.paths[name]; path != "" {
		return path, nil
	}
	return "", errors.New("not found")
}

func (runner *fakeRunner) Run(name string, args ...string) ([]byte, error) {
	runner.runs = append(runner.runs, recordedCommand{name, append([]string(nil), args...)})
	return runner.runOutput, runner.runError
}

func (runner *fakeRunner) Start(name string, args ...string) (startedCommand, error) {
	runner.starts = append(runner.starts, recordedCommand{name, append([]string(nil), args...)})
	if runner.startPID == 0 {
		runner.startPID = 4242
	}
	if runner.startExit == nil {
		runner.startExit = make(chan error)
	}
	if runner.createLock {
		for index, arg := range args {
			if arg == "-profile" && index+1 < len(args) {
				if err := os.WriteFile(filepath.Join(args[index+1], "parent.lock"), nil, 0o600); err != nil {
					return startedCommand{}, err
				}
				break
			}
		}
	}
	if runner.onStart != nil {
		runner.onStart(args)
	}
	if runner.startError != nil {
		return startedCommand{}, runner.startError
	}
	return startedCommand{
		pid:    runner.startPID,
		exited: runner.startExit,
		kill: func() error {
			runner.killed = true
			select {
			case runner.startExit <- errors.New("killed"):
			default:
			}
			return nil
		},
	}, nil
}

func TestPrepareAndLaunchCreatesIsolatedProfile(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(caPath, []byte("certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(dir, "firefox-profile")
	runner := newRunningRunner()
	config := Config{
		Profile: profile, CACertificate: caPath, ProxyAddress: "127.0.0.1:18080",
		URL: "https://www.foo-bar-example.test/",
	}
	if err := prepareAndLaunch(config, runner); err != nil {
		t.Fatal(err)
	}
	prefs, err := os.ReadFile(filepath.Join(profile, "user.js"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`user_pref("network.proxy.http", "127.0.0.1");`,
		`user_pref("network.proxy.http_port", 18080);`,
		`user_pref("network.proxy.ssl", "127.0.0.1");`,
	} {
		if !strings.Contains(string(prefs), want) {
			t.Errorf("preferences do not contain %q:\n%s", want, prefs)
		}
	}
	wantRuns := []recordedCommand{
		{"/bin/certutil", []string{"-N", "-d", "sql:" + profile, "--empty-password"}},
		{"/bin/certutil", []string{"-D", "-d", "sql:" + profile, "-n", certificateNickname}},
		{"/bin/certutil", []string{"-A", "-d", "sql:" + profile, "-n", certificateNickname, "-t", "C,,", "-i", caPath}},
	}
	if !reflect.DeepEqual(runner.runs, wantRuns) {
		t.Fatalf("commands = %#v, want %#v", runner.runs, wantRuns)
	}
	wantStart := []recordedCommand{{"/bin/firefox", []string{
		"-no-remote", "-profile", profile, config.URL,
	}}}
	if !reflect.DeepEqual(runner.starts, wantStart) {
		t.Fatalf("starts = %#v, want %#v", runner.starts, wantStart)
	}
	lease, err := os.ReadFile(filepath.Join(profile, launcherLockName))
	if err != nil {
		t.Fatal(err)
	}
	if state, pid, _, err := parseLease(lease); err != nil || state != "running" || pid != 4242 {
		t.Fatalf("lease = %q", lease)
	}
	stopFakeFirefox(t, runner, profile)
}

func TestPrepareAndLaunchRejectsConcurrentPreparation(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(caPath, []byte("certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(dir, "firefox-profile")
	if err := os.Mkdir(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(profile, preparationLockName), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := newRunningRunner()
	err := prepareAndLaunch(Config{
		Profile: profile, CACertificate: caPath, ProxyAddress: "127.0.0.1:18080", URL: "https://example.test/",
	}, runner)
	if err == nil || !strings.Contains(err.Error(), "being prepared") {
		t.Fatalf("error = %v", err)
	}
	if len(runner.runs) != 0 || len(runner.starts) != 0 {
		t.Fatalf("commands ran despite profile lock: %#v %#v", runner.runs, runner.starts)
	}
}

func TestAcquireProfileLeaseReplacesStaleLease(t *testing.T) {
	profile := t.TempDir()
	path := filepath.Join(profile, launcherLockName)
	if err := os.WriteFile(path, []byte("987654\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lease, err := acquireProfileLease(profile)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.remove()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	state, pid, token, err := parseLease(data)
	if err != nil || state != "preparing" || pid != os.Getpid() || token != lease.token {
		t.Fatalf("lease = %q", data)
	}
}

func TestAcquireProfileLeaseReplacesPriorRunningLease(t *testing.T) {
	profile := t.TempDir()
	path := filepath.Join(profile, launcherLockName)
	if err := os.WriteFile(path, []byte("running 12345 old-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lease, err := acquireProfileLease(profile)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.remove()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	state, _, token, err := parseLease(data)
	if err != nil || state != "preparing" || token != lease.token {
		t.Fatalf("lease = %q", data)
	}
}

func TestProfileLeaseDoesNotRemoveReplacement(t *testing.T) {
	profile := t.TempDir()
	lease, err := acquireProfileLease(profile)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(profile, launcherLockName)
	if err := os.WriteFile(path, []byte("preparing 12345 replacement-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := lease.remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("replacement lease was removed: %v", err)
	}
}

func TestExitedLeaseCleanupDoesNotRaceProfilePreparation(t *testing.T) {
	profile := t.TempDir()
	lease, err := acquireProfileLease(profile)
	if err != nil {
		t.Fatal(err)
	}
	preparation, err := acquirePreparationLock(profile)
	if err != nil {
		t.Fatal(err)
	}
	defer preparation.remove()

	path := filepath.Join(profile, launcherLockName)
	const replacement = "preparing 12345 replacement-token\n"
	if err := os.WriteFile(path, []byte(replacement), 0o600); err != nil {
		t.Fatal(err)
	}
	removeExitedLease(profile, lease)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("replacement lease was removed: %v", err)
	}
	if string(data) != replacement {
		t.Fatalf("lease = %q, want %q", data, replacement)
	}
}

func TestPrepareAndLaunchRequiresCertutil(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(caPath, []byte("certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := prepareAndLaunch(Config{
		Profile: filepath.Join(dir, "firefox-profile"), CACertificate: caPath,
		ProxyAddress: "127.0.0.1:18080", URL: "https://example.test/",
	}, &fakeRunner{paths: map[string]string{"firefox": "/bin/firefox"}})
	if err == nil || !strings.Contains(err.Error(), "certutil") {
		t.Fatalf("error = %v", err)
	}
}

func TestPrepareAndLaunchReusesCertificateDatabase(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.crt")
	profile := filepath.Join(dir, "firefox-profile")
	if err := os.Mkdir(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(caPath, []byte("certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "cert9.db"), []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := newRunningRunner()
	if err := prepareAndLaunch(Config{
		Profile: profile, CACertificate: caPath, ProxyAddress: "127.0.0.1:18080", URL: "https://example.test/",
	}, runner); err != nil {
		t.Fatal(err)
	}
	if len(runner.runs) != 2 || runner.runs[0].args[0] != "-D" || runner.runs[1].args[0] != "-A" {
		t.Fatalf("certificate commands = %#v", runner.runs)
	}
	stopFakeFirefox(t, runner, profile)
}

func TestPrepareAndLaunchRejectsFirefoxThatExitsDuringStartup(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(caPath, []byte("certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := newRunningRunner()
	runner.startExit <- errors.New("exit status 1")
	err := prepareAndLaunch(Config{
		Profile: filepath.Join(dir, "firefox-profile"), CACertificate: caPath,
		ProxyAddress: "127.0.0.1:18080", URL: "https://example.test/",
	}, runner)
	if err == nil || !strings.Contains(err.Error(), "exited during startup") {
		t.Fatalf("error = %v", err)
	}
}

func TestPrepareAndLaunchWaitsForNativeFirefoxLock(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(caPath, []byte("certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(dir, "firefox-profile")
	runner := newRunningRunner()
	runner.createLock = false
	started := make(chan struct{})
	runner.onStart = func([]string) { close(started) }
	result := make(chan error, 1)
	go func() {
		result <- prepareAndLaunch(Config{
			Profile: profile, CACertificate: caPath, ProxyAddress: "127.0.0.1:18080", URL: "https://example.test/",
		}, runner)
	}()
	<-started
	select {
	case err := <-result:
		t.Fatalf("launch returned before Firefox locked its profile: %v", err)
	default:
	}
	if err := os.WriteFile(filepath.Join(profile, "parent.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	stopFakeFirefox(t, runner, profile)
}

func TestPrepareAndLaunchRejectsNativeFirefoxLock(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.crt")
	profile := filepath.Join(dir, "firefox-profile")
	if err := os.Mkdir(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(caPath, []byte("certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "parent.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	runner := newRunningRunner()
	err := prepareAndLaunch(Config{
		Profile: profile, CACertificate: caPath, ProxyAddress: "127.0.0.1:18080", URL: "https://example.test/",
	}, runner)
	if err == nil || !strings.Contains(err.Error(), "appears to be in use") {
		t.Fatalf("error = %v", err)
	}
	if len(runner.runs) != 0 || len(runner.starts) != 0 {
		t.Fatalf("commands ran despite native profile lock: %#v %#v", runner.runs, runner.starts)
	}
	if _, err := os.Stat(filepath.Join(profile, preparationLockName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preparation lock was retained after failure: %v", err)
	}
}

func TestPrepareAndLaunchRemovesLeaseWhenFirefoxExits(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.crt")
	profile := filepath.Join(dir, "firefox-profile")
	if err := os.WriteFile(caPath, []byte("certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := newRunningRunner()
	if err := prepareAndLaunch(Config{
		Profile: profile, CACertificate: caPath, ProxyAddress: "127.0.0.1:18080", URL: "https://example.test/",
	}, runner); err != nil {
		t.Fatal(err)
	}
	stopFakeFirefox(t, runner, profile)
}

func TestPrepareAndLaunchRemovesPreparationLock(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.crt")
	profile := filepath.Join(dir, "firefox-profile")
	if err := os.WriteFile(caPath, []byte("certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := newRunningRunner()
	if err := prepareAndLaunch(Config{
		Profile: profile, CACertificate: caPath, ProxyAddress: "127.0.0.1:18080", URL: "https://example.test/",
	}, runner); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(profile, preparationLockName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preparation lock was retained: %v", err)
	}
	stopFakeFirefox(t, runner, profile)
}

func TestPrepareAndLaunchStopsFirefoxWhenPostStartSetupFails(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.crt")
	profile := filepath.Join(dir, "firefox-profile")
	if err := os.WriteFile(caPath, []byte("certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := newRunningRunner()
	runner.onStart = func([]string) {
		leasePath := filepath.Join(profile, launcherLockName)
		if err := os.Remove(leasePath); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(leasePath, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	err := prepareAndLaunch(Config{
		Profile: profile, CACertificate: caPath, ProxyAddress: "127.0.0.1:18080", URL: "https://example.test/",
	}, runner)
	if err == nil || !strings.Contains(err.Error(), "record Firefox process") {
		t.Fatalf("error = %v", err)
	}
	if !runner.killed {
		t.Fatal("Firefox was not stopped after post-start setup failed")
	}
}

func newRunningRunner() *fakeRunner {
	return &fakeRunner{
		paths:      map[string]string{"certutil": "/bin/certutil", "firefox": "/bin/firefox"},
		startExit:  make(chan error, 1),
		createLock: true,
	}
}

func stopFakeFirefox(t *testing.T, runner *fakeRunner, profile string) {
	t.Helper()
	runner.startExit <- nil
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		_, leaseErr := os.Stat(filepath.Join(profile, launcherLockName))
		_, preparationErr := os.Stat(filepath.Join(profile, preparationLockName))
		if errors.Is(leaseErr, os.ErrNotExist) && errors.Is(preparationErr, os.ErrNotExist) {
			return
		}
		if leaseErr != nil && !errors.Is(leaseErr, os.ErrNotExist) {
			t.Fatalf("inspect Firefox lease during shutdown: %v", leaseErr)
		}
		if preparationErr != nil && !errors.Is(preparationErr, os.ErrNotExist) {
			t.Fatalf("inspect Firefox preparation lock during shutdown: %v", preparationErr)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("Firefox lease cleanup did not finish")
}

func TestPrepareAndLaunchReportsCertutilOutputAndReleasesLease(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.crt")
	profile := filepath.Join(dir, "firefox-profile")
	if err := os.Mkdir(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(caPath, []byte("certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "cert9.db"), []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{
		paths:    map[string]string{"certutil": "/bin/certutil", "firefox": "/bin/firefox"},
		runError: errors.New("exit status 1"), runOutput: []byte("database is read-only\n"),
	}
	err := prepareAndLaunch(Config{
		Profile: profile, CACertificate: caPath, ProxyAddress: "127.0.0.1:18080", URL: "https://example.test/",
	}, runner)
	if err == nil || !strings.Contains(err.Error(), "database is read-only") {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(profile, launcherLockName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lease was not removed after failure: %v", err)
	}
}

func TestFindCertutilUsesExplicitPath(t *testing.T) {
	path, err := findCertutil("C:/nss/certutil.exe", &fakeRunner{})
	if err != nil || path != "C:/nss/certutil.exe" {
		t.Fatalf("findCertutil = %q, %v", path, err)
	}
}

func TestCommandErrorIncludesToolOutput(t *testing.T) {
	err := commandError("initialize database", errors.New("exit status 1"), []byte("bad password\n"))
	if !strings.Contains(err.Error(), "bad password") {
		t.Fatalf("error = %v", err)
	}
}

func TestWritePreferencesUsesLoopbackForWildcardListener(t *testing.T) {
	profile := t.TempDir()
	if err := writePreferences(profile, "0.0.0.0:18080"); err != nil {
		t.Fatal(err)
	}
	prefs, err := os.ReadFile(filepath.Join(profile, "user.js"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(prefs), "0.0.0.0") || !strings.Contains(string(prefs), `"127.0.0.1"`) {
		t.Fatalf("unexpected wildcard proxy preferences:\n%s", prefs)
	}
}

func TestWritePreferencesUsesLoopbackForEmptyHostWildcardListener(t *testing.T) {
	profile := t.TempDir()
	if err := writePreferences(profile, ":18080"); err != nil {
		t.Fatal(err)
	}
	prefs, err := os.ReadFile(filepath.Join(profile, "user.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(prefs), `user_pref("network.proxy.http", "127.0.0.1");`) {
		t.Fatalf("unexpected empty-host proxy preferences:\n%s", prefs)
	}
}

func TestFirefoxCandidates(t *testing.T) {
	environment := map[string]string{
		"ProgramFiles":      `C:\Program Files`,
		"ProgramFiles(x86)": `C:\Program Files (x86)`,
		"LOCALAPPDATA":      `C:\Users\developer\AppData\Local`,
	}
	getenv := func(name string) string { return environment[name] }
	home := func() (string, error) { return "/Users/developer", nil }

	mac := firefoxCandidates("darwin", getenv, home)
	if !reflect.DeepEqual(mac, []string{
		"/Applications/Firefox.app/Contents/MacOS/firefox",
		"/Users/developer/Applications/Firefox.app/Contents/MacOS/firefox",
	}) {
		t.Fatalf("macOS candidates = %#v", mac)
	}
	windows := firefoxCandidates("windows", getenv, home)
	if len(windows) != 3 || !strings.HasSuffix(windows[0], filepath.Join("Mozilla Firefox", "firefox.exe")) {
		t.Fatalf("Windows candidates = %#v", windows)
	}
}
