package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// stubOpener puts a recording stand-in for the platform's URL handler at the
// front of PATH, so the real opener code runs without a browser appearing.
func stubOpener(t *testing.T, exitCode int) string {
	t.Helper()

	dir := t.TempDir()
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	record := filepath.Join(dir, "opened")

	script := "#!/bin/sh\nprintf '%s' \"$1\" > " + record + "\nexit " +
		string(rune('0'+exitCode)) + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("PATH", dir)

	return record
}

// The opener is handed the URL, and a clean exit is what "it opened" means.
func TestTheSystemBrowserLaunchesThePlatformOpener(t *testing.T) {
	record := stubOpener(t, 0)

	if !(SystemBrowser{}).Open("https://www.drupal.org/node/3223746") {
		t.Fatal("a clean exit was not read as opened")
	}

	opened, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("the opener was never run: %v", err)
	}
	if string(opened) != "https://www.drupal.org/node/3223746" {
		t.Errorf("opened %q", opened)
	}
}

// A refusal is an answer, not a crash: the commands that use this say
// something different when the browser did not open, and that is only useful
// if a headless machine reports false rather than dying.
func TestTheSystemBrowserReportsARefusal(t *testing.T) {
	stubOpener(t, 3)

	if (SystemBrowser{}).Open("https://www.drupal.org/node/3223746") {
		t.Error("a failing opener was read as opened")
	}
}

// No opener on the machine at all is the same answer.
func TestTheSystemBrowserReportsAMissingOpener(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	if (SystemBrowser{}).Open("https://www.drupal.org/node/3223746") {
		t.Error("a missing opener was read as opened")
	}
}

// The opener is a child process like any other, so the credential is not in
// its environment — a URL handler is a shell script on most desktops, and the
// environment it is given is visible to everything it launches.
func TestTheBrowserOpenerNeverSeesTheCredential(t *testing.T) {
	t.Setenv("UPKEEP_GITLAB_TOKEN", "glpat-not-for-children")

	dir := t.TempDir()
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	record := filepath.Join(dir, "env")
	if err := os.WriteFile(filepath.Join(dir, name),
		[]byte("#!/bin/sh\nenv > "+record+"\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("PATH", dir)

	(SystemBrowser{}).Open("https://www.drupal.org/node/3223746")

	environment, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("the opener was never run: %v", err)
	}
	if strings.Contains(string(environment), "glpat-not-for-children") {
		t.Error("the credential reached the browser opener")
	}
}

// NoBrowser is what --no-open and every test are wired to.
func TestNoBrowserOpensNothing(t *testing.T) {
	if (NoBrowser{}).Open("https://www.drupal.org/node/3223746") {
		t.Error("it claimed to open something")
	}
}

// Both platforms' openers, because only one of them is reachable wherever this
// test happens to run.
func TestTheOpenerIsThePlatformsOwn(t *testing.T) {
	if got := openerFor("darwin"); got != "open" {
		t.Errorf("macOS: %q", got)
	}
	for _, goos := range []string{"linux", "freebsd", "windows"} {
		if got := openerFor(goos); got != "xdg-open" {
			t.Errorf("%s: %q", goos, got)
		}
	}
}
