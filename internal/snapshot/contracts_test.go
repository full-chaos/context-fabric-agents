package snapshot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const contractsDir = "../../contracts/acr-mcp"

// The committed files are the pin. A missing file is a failure: an
// unmeasured contract must not read as valid.
func TestCommittedSnapshotIsWellFormed(t *testing.T) {
	s, err := Load(filepath.Join(contractsDir, "snapshot.json"))
	if err != nil {
		t.Fatalf("committed snapshot: %v", err)
	}
	if s.SchemaVersion != "mcp_tools.v1" {
		t.Errorf("schema_version = %q", s.SchemaVersion)
	}
	if s.CapturedAt == "" || s.Host != "mcp.fullchaos.dev" || s.ServerInfo.Name == "" {
		t.Errorf("incomplete header: %+v %+v", s.Host, s.ServerInfo)
	}
	if len(s.Tools) == 0 {
		t.Fatal("no tools pinned")
	}
	for _, tl := range s.Tools {
		d, err := Digest(tl.InputSchema)
		if err != nil || d != tl.InputSchemaDigest {
			t.Errorf("tool %s: stored digest %q != recomputed %q", tl.Name, tl.InputSchemaDigest, d)
		}
	}
	got := map[string]string{}
	for _, n := range s.Negotiations {
		got[n.Requested+"|"+n.Path] = n.Negotiated
	}
	if got["2026-07-28|server/discover"] != "2026-07-28" || got["2025-06-18|initialize"] != "2025-06-18" {
		t.Errorf("negotiations = %+v", s.Negotiations)
	}
	// Re-serializing must reproduce the file byte for byte (canonical form).
	raw, _ := os.ReadFile(filepath.Join(contractsDir, "snapshot.json"))
	out, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(raw) {
		t.Error("snapshot.json is not in canonical form; regenerate it with cmd/snapshot")
	}
}

type compatFile struct {
	Contract string `json:"contract"`
	Clients  map[string]struct {
		PinnedVersion    string `json:"pinned_version"`
		ExpectedRevision string `json:"expected_revision"`
		ExpectedMethod   string `json:"expected_method"`
		Status           string `json:"status"`
	} `json:"clients"`
}

func TestCompatMatchesSnapshot(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(contractsDir, "compat.json"))
	if err != nil {
		t.Fatalf("compat.json: %v", err)
	}
	var c compatFile
	dec := json.NewDecoder(bytesReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		t.Fatal(err)
	}
	if c.Contract != "mcp_tools.v1" {
		t.Errorf("contract = %q", c.Contract)
	}
	s, err := Load(filepath.Join(contractsDir, "snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	negotiated := map[string]bool{}
	for _, n := range s.Negotiations {
		negotiated[n.Negotiated] = true
	}
	confirmed := 0
	for name, cl := range c.Clients {
		switch cl.Status {
		case "confirmed":
			confirmed++
			if cl.PinnedVersion == "" || cl.ExpectedRevision == "" || cl.ExpectedMethod == "" {
				t.Errorf("%s: confirmed entry is incomplete: %+v", name, cl)
			}
			if !negotiated[cl.ExpectedRevision] {
				t.Errorf("%s: expects revision %s, which the server snapshot does not negotiate", name, cl.ExpectedRevision)
			}
		case "unconfirmed":
			if cl.ExpectedRevision != "" || cl.ExpectedMethod != "" {
				t.Errorf("%s: unconfirmed entry must not claim a revision or method", name)
			}
		default:
			t.Errorf("%s: status %q", name, cl.Status)
		}
	}
	if confirmed < 2 {
		t.Errorf("confirmed clients = %d, want >= 2 (claude-code, codex)", confirmed)
	}
}
