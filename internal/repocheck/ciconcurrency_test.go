package repocheck_test

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ciEvent is the part of the github context a concurrency expression may read.
type ciEvent struct {
	name, sha, ref, runID string
}

func (e ciEvent) lookup(name string) (any, error) {
	switch name {
	case "github.event_name":
		return e.name, nil
	case "github.sha":
		return e.sha, nil
	case "github.ref":
		return e.ref, nil
	case "github.run_id":
		return e.runID, nil
	}
	return nil, fmt.Errorf("unknown context %q", name)
}

var ciExpression = regexp.MustCompile(`\$\{\{(.*?)\}\}`)

// interpolate renders a workflow string the way Actions does for the subset the
// concurrency block uses. Anything else is an error, so a new construct fails
// closed instead of being read as a literal.
func (e ciEvent) interpolate(s string) (string, error) {
	var failure error
	out := ciExpression.ReplaceAllStringFunc(s, func(match string) string {
		p := &ciParser{src: strings.TrimSpace(ciExpression.FindStringSubmatch(match)[1]), event: e}
		value, err := p.parse()
		if err != nil && failure == nil {
			failure = fmt.Errorf("%q: %w", match, err)
		}
		return fmt.Sprint(value)
	})
	return out, failure
}

// ciParser evaluates `a == 'x' && b || c` with the Actions semantics that
// matter here: && and || return an operand, strings compare case-insensitively
// and an empty string or false is falsy.
type ciParser struct {
	src   string
	pos   int
	event ciEvent
}

func (p *ciParser) parse() (any, error) {
	value, err := p.or()
	if err != nil {
		return nil, err
	}
	p.skip()
	if p.pos != len(p.src) {
		return nil, fmt.Errorf("unexpected %q", p.src[p.pos:])
	}
	return value, nil
}

func (p *ciParser) skip() {
	for p.pos < len(p.src) && p.src[p.pos] == ' ' {
		p.pos++
	}
}

func (p *ciParser) accept(token string) bool {
	p.skip()
	if strings.HasPrefix(p.src[p.pos:], token) {
		p.pos += len(token)
		return true
	}
	return false
}

func ciTruthy(v any) bool {
	switch v := v.(type) {
	case bool:
		return v
	case string:
		return v != ""
	}
	return false
}

func (p *ciParser) or() (any, error) {
	left, err := p.and()
	for err == nil && p.accept("||") {
		var right any
		if right, err = p.and(); err == nil && !ciTruthy(left) {
			left = right
		}
	}
	return left, err
}

func (p *ciParser) and() (any, error) {
	left, err := p.equality()
	for err == nil && p.accept("&&") {
		var right any
		if right, err = p.equality(); err == nil && ciTruthy(left) {
			left = right
		}
	}
	return left, err
}

func (p *ciParser) equality() (any, error) {
	left, err := p.operand()
	if err != nil {
		return nil, err
	}
	for _, op := range []string{"==", "!="} {
		if p.accept(op) {
			right, err := p.operand()
			if err != nil {
				return nil, err
			}
			same := strings.EqualFold(fmt.Sprint(left), fmt.Sprint(right))
			return same == (op == "=="), nil
		}
	}
	return left, nil
}

var ciName = regexp.MustCompile(`^[a-z_]+(\.[a-z_]+)*`)

func (p *ciParser) operand() (any, error) {
	p.skip()
	rest := p.src[p.pos:]
	if strings.HasPrefix(rest, "'") {
		end := strings.Index(rest[1:], "'")
		if end < 0 {
			return nil, fmt.Errorf("unterminated string in %q", rest)
		}
		p.pos += end + 2
		return rest[1 : end+1], nil
	}
	name := ciName.FindString(rest)
	if name == "" {
		return nil, fmt.Errorf("unsupported syntax at %q", rest)
	}
	p.pos += len(name)
	switch name {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return p.event.lookup(name)
}

func TestCIConcurrencyExpressionEvaluator(t *testing.T) {
	t.Parallel()
	push := ciEvent{name: "push", sha: "aaa", ref: "refs/heads/main", runID: "1"}
	for expr, want := range map[string]string{
		"g-${{ github.event_name == 'push' && github.sha || github.ref }}": "g-aaa",
		"g-${{ github.event_name == 'Push' && github.sha || github.ref }}": "g-aaa",
		"g-${{ github.event_name != 'push' && github.sha || github.ref }}": "g-refs/heads/main",
		"${{ github.event_name == 'pull_request' }}":                       "false",
		"g-${{ github.run_id }}":                                           "g-1",
		"plain":                                                            "plain",
	} {
		if got, err := push.interpolate(expr); err != nil || got != want {
			t.Errorf("%s = %q, %v; want %q", expr, got, err, want)
		}
	}
	for _, expr := range []string{"${{ github.workflow }}", "${{ contains(github.ref, 'x') }}", "${{ github.sha == 'a }}"} {
		if got, err := push.interpolate(expr); err == nil {
			t.Errorf("%s must be refused, got %q", expr, got)
		}
	}
	pull := ciEvent{name: "pull_request", sha: "bbb", ref: "refs/pull/7/merge"}
	if got, err := pull.interpolate("g-${{ github.event_name == 'push' && github.sha || github.ref }}"); err != nil || got != "g-refs/pull/7/merge" {
		t.Errorf("pull request group = %q, %v", got, err)
	}
}

// TestCIConcurrencyKeepsEveryMainCommitsRun checks what the group expressions
// do, not how they are spelled. Actions keeps one running and one pending run
// per group and cancels the older pending run, even with cancel-in-progress
// false, so two pushes to main may never share a group. Superseded pull request
// runs are the only runs that may be cancelled.
func TestCIConcurrencyKeepsEveryMainCommitsRun(t *testing.T) {
	t.Parallel()
	paths, err := filepath.Glob(filepath.Join(repoRoot(t), ".github", "workflows", "*.yml"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("workflow inventory: %v (%d files)", err, len(paths))
	}
	for _, path := range paths {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			workflow := readCIWorkflow(t, name)
			if workflow.Concurrency.Group == "" {
				t.Fatal("workflow needs a concurrency group")
			}
			group := func(e ciEvent) string {
				t.Helper()
				got, err := e.interpolate(workflow.Concurrency.Group)
				if err != nil {
					t.Fatal(err)
				}
				return got
			}
			cancels := func(e ciEvent) bool {
				t.Helper()
				got, err := e.interpolate(workflow.Concurrency.Cancel)
				if err != nil {
					t.Fatal(err)
				}
				return got == "true"
			}
			for trigger := range workflow.On {
				switch trigger {
				case "push":
					first := ciEvent{name: "push", sha: "1111111", ref: "refs/heads/main", runID: "1"}
					second := ciEvent{name: "push", sha: "2222222", ref: "refs/heads/main", runID: "2"}
					if group(first) == group(second) {
						t.Errorf("pushes of two commits share group %q, so the earlier pending run is cancelled unrun", group(first))
					}
					if cancels(first) || cancels(second) {
						t.Error("a push run must never be cancelled")
					}
				case "pull_request":
					first := ciEvent{name: "pull_request", sha: "1111111", ref: "refs/pull/7/merge", runID: "1"}
					second := ciEvent{name: "pull_request", sha: "2222222", ref: "refs/pull/7/merge", runID: "2"}
					other := ciEvent{name: "pull_request", sha: "3333333", ref: "refs/pull/8/merge", runID: "3"}
					if group(first) != group(second) || !cancels(second) {
						t.Error("two runs of one pull request must share a group and cancel the superseded run")
					}
					if group(first) == group(other) {
						t.Error("different pull requests must not share a group")
					}
					if main := (ciEvent{name: "push", sha: "1111111", ref: "refs/heads/main", runID: "1"}); group(first) == group(main) {
						t.Error("a pull request and a push to main must not share a group")
					}
				case "workflow_dispatch", "schedule", "workflow_call":
					if e := (ciEvent{name: trigger, sha: "1111111", ref: "refs/heads/main", runID: "1"}); cancels(e) {
						t.Errorf("%s runs must never be cancelled", trigger)
					}
				default:
					t.Errorf("trigger %s has no concurrency case; add one", trigger)
				}
			}
		})
	}
}
