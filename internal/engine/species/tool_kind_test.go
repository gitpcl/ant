package species

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"
)

// TestToolFixKindWiring verifies the Sprint 017 manifest wiring: a tool fix
// with a command is VALID; a tool fix WITHOUT a command is a loud manifest error;
// formatter-idempotence is a recognized verify kind.
func TestToolFixKindWiring(t *testing.T) {
	reg := NewRegistry()
	if !reg.KnownFixKind(FixKindTool) {
		t.Fatal("registry does not know the tool fix kind")
	}
	if !reg.KnownVerifyKind("formatter-idempotence") {
		t.Fatal("registry does not know the formatter-idempotence verify kind")
	}

	valid := `name="t"
severity="low"
[detector]
kind="ast-grep"
rule="detect.yml"
[fix]
kind="tool"
command="gofmt"
args=["-w","{file}"]
[verify]
checks=["formatter-idempotence","compile"]
[verify.tool]
command="gofmt"
args=["-l","{file}"]
`
	fsys := fstest.MapFS{
		"t/species.toml": {Data: []byte(valid)},
		"t/detect.yml":   {Data: []byte("id: t")},
	}
	m, err := Load(fsys, "t", "test", reg)
	if err != nil {
		t.Fatalf("valid tool manifest failed to load: %v", err)
	}
	if m.Fix.Command != "gofmt" || len(m.Fix.Args) != 2 {
		t.Errorf("tool command/args not decoded: %+v", m.Fix)
	}
	if m.Verify.Tool.Command != "gofmt" {
		t.Errorf("[verify.tool] not decoded: %+v", m.Verify.Tool)
	}

	noCmd := strings.Replace(valid, `command="gofmt"
args=["-w","{file}"]`, "", 1)
	fsys2 := fstest.MapFS{
		"t/species.toml": {Data: []byte(noCmd)},
		"t/detect.yml":   {Data: []byte("id: t")},
	}
	_, err = Load(fsys2, "t", "test", reg)
	if err == nil {
		t.Fatal("tool manifest with no command loaded, want a loud rejection")
	}
	if !errors.Is(err, ErrInvalidManifest) {
		t.Errorf("err = %v, want ErrInvalidManifest", err)
	}
	if !strings.Contains(err.Error(), "command") {
		t.Errorf("err = %v, want it to name the missing command", err)
	}
}
