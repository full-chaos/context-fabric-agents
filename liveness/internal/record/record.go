// Package record holds the two files the liveness aggregator trusts: the
// leg declarations (liveness/legs.json) and one result record per live leg
// (results/<leg>.json). Both decode strictly: an unknown field, a missing
// field or a trailing value is an error, never a default.
package record

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
)

const (
	// ResultSchema is the schema_version of a result record.
	ResultSchema = "cfa.liveness.result.v1"
	// LegsSchema is the schema_version of liveness/legs.json.
	LegsSchema = "cfa.liveness.legs.v1"

	StatusPass   = "pass"
	StatusFail   = "fail"
	StatusNotRun = "not_run"

	ModeLive        = "live"
	ModeStaticOnly  = "static-only"
	ModeDeclaredOff = "declared-off"
)

// Step is one probe step. Detail is short, fixed text chosen by the probe:
// never a header, a body or a credential.
type Step struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// Result is results/<leg>.json.
type Result struct {
	SchemaVersion string `json:"schema_version"`
	Leg           string `json:"leg"`
	Status        string `json:"status"`
	Host          string `json:"host"`
	StartedAt     string `json:"started_at"`
	FinishedAt    string `json:"finished_at"`
	Steps         []Step `json:"steps"`
	// Negotiated maps requested revision -> revision the server answered.
	Negotiated    map[string]string `json:"negotiated"`
	ServerVersion string            `json:"server_version"`
	RequestCount  int               `json:"request_count"`
}

// Finalize sets Status from the steps: pass only when there is at least one
// step and every step passed.
func (r *Result) Finalize() {
	r.Status = StatusPass
	if len(r.Steps) == 0 {
		r.Status = StatusFail
	}
	for _, s := range r.Steps {
		if s.Status != StatusPass {
			r.Status = StatusFail
		}
	}
}

// Marshal returns indented, newline-terminated JSON.
func (r *Result) Marshal() ([]byte, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// decodeStrict decodes exactly one JSON value with no unknown fields.
func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing data after the JSON value")
	}
	return nil
}

// DecodeResult parses and checks a result record. It checks shape and
// internal consistency only; whether the leg passed is the caller's call.
func DecodeResult(data []byte) (*Result, error) {
	var r Result
	if err := decodeStrict(data, &r); err != nil {
		return nil, err
	}
	var errs []string
	if r.SchemaVersion != ResultSchema {
		errs = append(errs, fmt.Sprintf("schema_version %q, want %q", r.SchemaVersion, ResultSchema))
	}
	if r.Leg == "" {
		errs = append(errs, "leg is empty")
	}
	if r.Status != StatusPass && r.Status != StatusFail {
		errs = append(errs, fmt.Sprintf("status %q is not pass|fail", r.Status))
	}
	if r.StartedAt == "" || r.FinishedAt == "" {
		errs = append(errs, "started_at/finished_at missing")
	}
	if len(r.Steps) == 0 {
		errs = append(errs, "no steps")
	}
	seen := map[string]bool{}
	allPass := len(r.Steps) > 0
	for _, s := range r.Steps {
		if s.ID == "" || seen[s.ID] {
			errs = append(errs, fmt.Sprintf("step id %q empty or duplicated", s.ID))
		}
		seen[s.ID] = true
		switch s.Status {
		case StatusPass:
		case StatusFail, StatusNotRun:
			allPass = false
		default:
			errs = append(errs, fmt.Sprintf("step %s status %q is not pass|fail|not_run", s.ID, s.Status))
		}
	}
	if r.Status == StatusPass && !allPass {
		errs = append(errs, "status pass but a step did not pass")
	}
	if r.RequestCount < 0 {
		errs = append(errs, "request_count negative")
	}
	if len(errs) > 0 {
		return nil, errors.New(strings.Join(errs, "; "))
	}
	return &r, nil
}

// Leg is one declared leg.
type Leg struct {
	ID          string `json:"id"`
	Mode        string `json:"mode"`
	Description string `json:"description"`
	// Workflow is the workflow file (under .github/workflows) whose
	// aggregate job judges a live leg. Required for live legs.
	Workflow string `json:"workflow,omitempty"`
	// Job is the workflow job id that runs a live leg.
	Job string `json:"job,omitempty"`
	// RequiredSteps must all be present and pass in a live leg's result.
	RequiredSteps []string `json:"required_steps,omitempty"`
	// Reason and Issue are required for declared-off.
	Reason string `json:"reason,omitempty"`
	Issue  string `json:"issue,omitempty"`
}

// Legs is liveness/legs.json.
type Legs struct {
	SchemaVersion string `json:"schema_version"`
	Legs          []Leg  `json:"legs"`
}

var (
	legIDRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	workflowRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}\.yml$`)
)

// ResultFile is the only file name a live leg may write.
func ResultFile(id string) string { return id + ".json" }

// LiveIn returns the live legs judged by one workflow file.
func (l *Legs) LiveIn(workflow string) []Leg {
	var out []Leg
	for _, g := range l.Live() {
		if g.Workflow == workflow {
			out = append(out, g)
		}
	}
	return out
}

// Live returns the live legs.
func (l *Legs) Live() []Leg {
	var out []Leg
	for _, g := range l.Legs {
		if g.Mode == ModeLive {
			out = append(out, g)
		}
	}
	return out
}

// DecodeLegs parses and validates legs.json.
func DecodeLegs(data []byte) (*Legs, error) {
	var l Legs
	if err := decodeStrict(data, &l); err != nil {
		return nil, err
	}
	var errs []string
	if l.SchemaVersion != LegsSchema {
		errs = append(errs, fmt.Sprintf("schema_version %q, want %q", l.SchemaVersion, LegsSchema))
	}
	if len(l.Legs) == 0 {
		errs = append(errs, "no legs declared")
	}
	ids, jobs := map[string]bool{}, map[string]bool{}
	live := 0
	for _, g := range l.Legs {
		if !legIDRe.MatchString(g.ID) || ids[g.ID] {
			errs = append(errs, fmt.Sprintf("leg id %q invalid or duplicated", g.ID))
		}
		ids[g.ID] = true
		if strings.TrimSpace(g.Description) == "" {
			errs = append(errs, fmt.Sprintf("leg %s: description is empty", g.ID))
		}
		switch g.Mode {
		case ModeLive:
			live++
			if g.Job == "" || jobs[g.Job] {
				errs = append(errs, fmt.Sprintf("leg %s: live leg needs a unique job", g.ID))
			}
			jobs[g.Job] = true
			if !workflowRe.MatchString(g.Workflow) {
				errs = append(errs, fmt.Sprintf("leg %s: live leg needs a workflow file name (for example liveness.yml)", g.ID))
			}
			if g.Reason != "" || g.Issue != "" {
				errs = append(errs, fmt.Sprintf("leg %s: reason/issue only apply to declared-off", g.ID))
			}
		case ModeStaticOnly:
			if g.Job != "" || g.Workflow != "" || len(g.RequiredSteps) > 0 || g.Reason != "" || g.Issue != "" {
				errs = append(errs, fmt.Sprintf("leg %s: static-only takes no workflow, job, steps, reason or issue", g.ID))
			}
		case ModeDeclaredOff:
			if strings.TrimSpace(g.Reason) == "" {
				errs = append(errs, fmt.Sprintf("leg %s: declared-off needs a reason", g.ID))
			}
			if !validIssueLink(g.Issue) {
				errs = append(errs, fmt.Sprintf("leg %s: declared-off needs an https issue link (linear.app or github.com)", g.ID))
			}
			if g.Job != "" || g.Workflow != "" || len(g.RequiredSteps) > 0 {
				errs = append(errs, fmt.Sprintf("leg %s: declared-off takes no workflow, job or steps", g.ID))
			}
		default:
			errs = append(errs, fmt.Sprintf("leg %s: mode %q is not live|static-only|declared-off", g.ID, g.Mode))
		}
	}
	if live == 0 {
		errs = append(errs, "no live leg declared")
	}
	if len(errs) > 0 {
		return nil, errors.New(strings.Join(errs, "; "))
	}
	return &l, nil
}

func validIssueLink(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Path == "" || u.Path == "/" {
		return false
	}
	return u.Host == "linear.app" || u.Host == "github.com"
}

// LoadLegs reads and validates a legs file.
func LoadLegs(path string) (*Legs, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	l, err := DecodeLegs(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return l, nil
}
