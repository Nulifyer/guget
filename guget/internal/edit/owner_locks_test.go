package edit

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nulifyer/guget/internal/atomicfile"
)

// The helper is a separate process so these assertions exercise the OS locks,
// including descriptor cleanup on process death, rather than only a Go mutex.
func TestOwnerLockProcessHelper(t *testing.T) {
	mode := os.Getenv("GUGET_LOCK_TEST_MODE")
	if mode == "" {
		return
	}
	var paths []string
	if err := json.Unmarshal([]byte(os.Getenv("GUGET_LOCK_TEST_PATHS")), &paths); err != nil {
		t.Fatal(err)
	}
	for i, path := range paths {
		resolved, err := canonicalPath(path)
		if err != nil {
			t.Fatal(err)
		}
		paths[i] = resolved
	}
	if mode == "hold" {
		unlock, err := lockOwners(context.Background(), paths)
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()
		fmt.Println("LOCKED")
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	var changes []Change
	for _, path := range paths {
		change, err := ReadChange(path, []byte(os.Getenv("GUGET_LOCK_TEST_AFTER")))
		if err != nil {
			t.Fatal(err)
		}
		changes = append(changes, change)
	}
	plan, err := NewPlan(changes...)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("READY")
	if _, err := io.ReadFull(os.Stdin, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	result, err := plan.Apply()
	switch {
	case err == nil:
		fmt.Println("OK")
	case errors.Is(err, ErrStale) && len(result.Changed) == 0:
		fmt.Println("STALE")
	default:
		t.Fatalf("apply result = %+v, error = %v", result, err)
	}
}

type ownerLockProcess struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	output *bufio.Scanner
	waited bool
	cancel context.CancelFunc
}

func startOwnerProcess(t *testing.T, mode string, paths []string, after string) *ownerLockProcess {
	t.Helper()
	encoded, err := json.Marshal(paths)
	if err != nil {
		t.Fatal(err)
	}
	// A broken lock-order implementation must fail the test rather than hang CI.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOwnerLockProcessHelper$")
	cmd.Env = append(os.Environ(), "GUGET_LOCK_TEST_MODE="+mode, "GUGET_LOCK_TEST_PATHS="+string(encoded), "GUGET_LOCK_TEST_AFTER="+after)
	cmd.Stderr = os.Stderr
	input, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	process := &ownerLockProcess{cmd: cmd, input: input, output: bufio.NewScanner(output), cancel: cancel}
	t.Cleanup(func() {
		_ = process.input.Close()
		if !process.waited {
			_ = process.cmd.Process.Kill()
			_ = process.cmd.Wait()
		}
		process.cancel()
	})
	return process
}

func (p *ownerLockProcess) line(t *testing.T, want string) string {
	t.Helper()
	if !p.output.Scan() {
		t.Fatalf("helper ended before %q: %v", want, p.output.Err())
	}
	got := p.output.Text()
	if want != "" && got != want {
		t.Fatalf("helper output = %q, want %q", got, want)
	}
	return got
}

func (p *ownerLockProcess) wait(t *testing.T) {
	t.Helper()
	_ = p.input.Close()
	err := p.cmd.Wait()
	p.waited = true
	if err != nil {
		t.Fatalf("helper failed: %v", err)
	}
}

func TestOverlappingProcessLockCanBeCanceledAndReleased(t *testing.T) {
	plan, first, _ := planPair(t)
	holder := startOwnerProcess(t, "hold", []string{first}, "")
	holder.line(t, "LOCKED")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	result, err := plan.ApplyContext(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || len(result.Changed) != 0 {
		t.Fatalf("waiting transaction result = %+v, error = %v", result, err)
	}
	// Killing the lock holder must release its locks without stale-PID repair.
	if err := holder.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = holder.cmd.Wait()
	holder.waited = true
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := plan.ApplyContext(ctx); err != nil {
		t.Fatalf("apply after lock-holder death: %v", err)
	}
}

func TestCanceledMultiOwnerWaitReleasesEarlierOwners(t *testing.T) {
	plan, first, second := planPair(t)
	holder := startOwnerProcess(t, "hold", []string{second}, "")
	holder.line(t, "LOCKED")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	result, err := plan.ApplyContext(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) || len(result.Changed) != 0 {
		t.Fatalf("waiting transaction result = %+v, error = %v", result, err)
	}
	// The held second owner must not block a disjoint first-owner transaction,
	// including any earlier first-owner lock acquired by the canceled request.
	change, err := ReadChange(first, []byte("independent"))
	if err != nil {
		t.Fatal(err)
	}
	independent, err := NewPlan(change)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := independent.ApplyContext(ctx); err != nil {
		t.Fatalf("independent owner blocked or leaked lock: %v", err)
	}
	holder.wait(t)
}

func TestOppositeOrderProcessesShareOneTransactionOrder(t *testing.T) {
	_, first, second := planPair(t)
	a := startOwnerProcess(t, "apply", []string{first, second}, "writer-A")
	b := startOwnerProcess(t, "apply", []string{second, first}, "writer-B")
	a.line(t, "READY")
	b.line(t, "READY")
	for _, process := range []*ownerLockProcess{a, b} {
		if _, err := process.input.Write([]byte("s")); err != nil {
			t.Fatal(err)
		}
	}
	results := []string{a.line(t, ""), b.line(t, "")}
	if strings.Join(results, ",") != "OK,STALE" && strings.Join(results, ",") != "STALE,OK" {
		t.Fatalf("writer results = %v, want one commit and one stale plan", results)
	}
	a.wait(t)
	b.wait(t)
	firstData, _ := os.ReadFile(first)
	secondData, _ := os.ReadFile(second)
	if string(firstData) != string(secondData) || (string(firstData) != "writer-A" && string(firstData) != "writer-B") {
		t.Fatalf("mixed transaction: %q, %q", firstData, secondData)
	}
}

func TestWorkspaceRootsAndAliasesShareOwnerLocks(t *testing.T) {
	root := t.TempDir()
	owner := filepath.Join(root, "Directory.Packages.props")
	if err := os.WriteFile(owner, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, workspace := range []string{"workspace-a", "workspace-b"} {
		if err := os.Mkdir(filepath.Join(root, workspace), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// Construct the '..' alias without Join cleaning it away.
	rootAlias := filepath.Join(root, "workspace-b") + string(os.PathSeparator) + ".." + string(os.PathSeparator) + filepath.Base(owner)
	aliases := []string{rootAlias}
	hardAlias := filepath.Join(root, "hardlink.props")
	if err := os.Link(owner, hardAlias); err == nil {
		aliases = append(aliases, hardAlias)
	} else {
		t.Logf("hard links unavailable: %v", err)
	}
	symlinkAlias := filepath.Join(root, "symlink.props")
	if err := os.Symlink(owner, symlinkAlias); err == nil {
		aliases = append(aliases, symlinkAlias)
	} else {
		t.Logf("symlinks unavailable: %v", err)
	}
	holder := startOwnerProcess(t, "hold", []string{owner}, "")
	holder.line(t, "LOCKED")
	for _, alias := range aliases {
		change, err := ReadChange(alias, []byte("new"))
		if err != nil {
			t.Fatal(err)
		}
		plan, err := NewPlan(change)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		result, err := plan.ApplyContext(ctx)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) || len(result.Changed) != 0 {
			t.Fatalf("alias %s bypassed owner lock: result = %+v, error = %v", alias, result, err)
		}
	}
	holder.wait(t)
}

func TestConcurrentPlansAreCheckedAfterAcquiringOwnerLock(t *testing.T) {
	plan, first, _ := planPair(t)
	ready, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := plan.applyChecked(context.Background(), func(path string, data []byte, mode os.FileMode, check func() error) error {
			if path == first {
				close(ready)
				<-release
			}
			return atomicfile.WriteFileChecked(path, data, mode, check)
		})
		done <- err
	}()
	<-ready
	// This second plan was prepared while the first writer held the owner.
	secondChange, err := ReadChange(first, []byte("second-writer"))
	if err != nil {
		t.Fatal(err)
	}
	secondPlan, err := NewPlan(secondChange)
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if result, err := secondPlan.Apply(); !errors.Is(err, ErrStale) || len(result.Changed) != 0 {
		t.Fatalf("second writer result = %+v, error = %v", result, err)
	}
}
