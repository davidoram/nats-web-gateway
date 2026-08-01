//go:build mage

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/magefile/mage/sh"
)

const (
	modulePath   = "github.com/davidoram/nats-web-gateway"
	caddyVersion = "v2.11.4"
)

var generatedDirectories = []string{"build", "coverage", "dist"}

// Build creates the pinned custom Caddy binary under build/.
func Build() error {
	if err := os.MkdirAll("build", 0o755); err != nil {
		return fmt.Errorf("create build directory: %w", err)
	}
	env, err := commandEnvironment()
	if err != nil {
		return err
	}
	env["CGO_ENABLED"] = "0"
	env["GOFLAGS"] = "-trimpath -buildvcs=false"
	if err := sh.RunWithV(env, "go", "tool", "xcaddy", "build", caddyVersion,
		"--output", filepath.FromSlash("build/caddy"), "--with", modulePath+"=."); err != nil {
		return fmt.Errorf("build custom Caddy binary: %w", err)
	}
	return nil
}

// Test runs the fast unit-test suite.
func Test() error {
	return run("go", "test", "./...")
}

// TestRace runs all race-appropriate tests with the race detector.
func TestRace() error {
	return run("go", "test", "-race", "./...")
}

// Coverage writes machine-readable and HTML coverage reports under coverage/.
func Coverage() error {
	if err := os.MkdirAll("coverage", 0o755); err != nil {
		return fmt.Errorf("create coverage directory: %w", err)
	}
	if err := run("go", "test", "-covermode=atomic", "-coverprofile=coverage/coverage.out", "./..."); err != nil {
		return err
	}
	return run("go", "tool", "cover", "-html=coverage/coverage.out", "-o", "coverage/coverage.html")
}

// Integration runs protocol-boundary tests. OSS-003 will add the pinned local
// Caddy and NATS environment; the integration build tag keeps the entry point
// stable until those tests exist.
func Integration() error {
	return run("go", "test", "-tags=integration", "./...")
}

// Lint checks formatting, module metadata, go vet, and static analysis.
func Lint() error {
	files, err := goFiles()
	if err != nil {
		return err
	}
	args := append([]string{"-l"}, files...)
	output, err := commandOutput("gofmt", args...)
	if err != nil {
		return err
	}
	if strings.TrimSpace(output) != "" {
		return fmt.Errorf("Go files require formatting:\n%s", output)
	}
	if err := run("go", "mod", "tidy", "-diff"); err != nil {
		return err
	}
	if err := run("go", "vet", "./..."); err != nil {
		return err
	}
	return run("go", "tool", "staticcheck", "./...")
}

// Security verifies downloaded modules and checks reachable vulnerabilities.
func Security() error {
	if err := run("go", "mod", "verify"); err != nil {
		return err
	}
	return run("go", "tool", "govulncheck", "./...")
}

// Sbom creates a CycloneDX JSON SBOM for the application under dist/.
func Sbom() error {
	if err := Build(); err != nil {
		return err
	}
	return sbomForExistingBuild()
}

// Verify runs the normal pre-pull-request quality gate.
func Verify() error {
	return runTargets(Lint, Test, TestRace, Coverage)
}

// Ci runs the authoritative merge-gating suite.
func Ci() error {
	return runTargets(Verify, Build, Integration, Security, sbomForExistingBuild)
}

func sbomForExistingBuild() error {
	if err := os.MkdirAll("dist", 0o755); err != nil {
		return fmt.Errorf("create dist directory: %w", err)
	}
	return run("go", "tool", "cyclonedx-gomod", "bin", "-json", "-output", "dist/nats-web-gateway.cdx.json", "build/caddy")
}

func runTargets(targets ...func() error) error {
	for _, target := range targets {
		if err := target(); err != nil {
			return err
		}
	}
	return nil
}

// Clean removes only known generated repository artifacts.
func Clean() error {
	for _, directory := range generatedDirectories {
		if err := os.RemoveAll(directory); err != nil {
			return fmt.Errorf("remove %s: %w", directory, err)
		}
	}
	return nil
}

func goFiles() ([]string, error) {
	var files []string
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			for _, generated := range generatedDirectories {
				if path == generated {
					return filepath.SkipDir
				}
			}
			if path == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("find Go files: %w", err)
	}
	sort.Strings(files)
	return files, nil
}

func run(name string, args ...string) error {
	env, err := commandEnvironment()
	if err != nil {
		return err
	}
	if err := sh.RunWithV(env, name, args...); err != nil {
		return fmt.Errorf("%s: %w", strings.Join(append([]string{name}, args...), " "), err)
	}
	return nil
}

func commandEnvironment() (map[string]string, error) {
	cacheRoot, err := filepath.Abs(filepath.FromSlash("build/.cache"))
	if err != nil {
		return nil, fmt.Errorf("resolve tool cache: %w", err)
	}
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create tool cache: %w", err)
	}
	return map[string]string{
		"GOCACHE":           filepath.Join(cacheRoot, "go-build"),
		"STATICCHECK_CACHE": filepath.Join(cacheRoot, "staticcheck"),
	}, nil
}

func commandOutput(name string, args ...string) (string, error) {
	command := exec.Command(name, args...)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("%s: %w", strings.Join(append([]string{name}, args...), " "), err)
	}
	return output.String(), nil
}
