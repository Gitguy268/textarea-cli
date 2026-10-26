// Package oracle runs testdata/oracle.mjs - textarea's own JavaScript under
// Node - for the tests that check the Go port byte for byte.
//
// Where Node or the textarea tree is missing (CI), Run replays the answers
// recorded in testdata/oracle/ by the last live run with ORACLE_RECORD=1, so
// the port is still checked against them; a live run fails when a recording
// has gone stale.
package oracle

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

// Request is one call: Op plus the op's fields.
type Request map[string]any

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// Dir is the textarea tree the oracle loads.
func Dir() string {
	if d := os.Getenv("TEXTAREA_DIR"); d != "" {
		return d
	}
	return filepath.Join(repoRoot(), "..", "textarea-master-claude")
}

// Run sends the requests and returns one raw JSON result per request, live
// when it can and from the test's recording otherwise.
func Run(t *testing.T, reqs []Request) []json.RawMessage {
	t.Helper()
	return RunWith(t, reqs, sameJSON)
}

// RunWith is Run for answers that vary between runs (fresh ids): eq says
// whether a live answer still matches the recorded one.
func RunWith(t *testing.T, reqs []Request, eq func(live, recorded []byte) bool) []json.RawMessage {
	t.Helper()
	in, _ := json.Marshal(reqs)
	path := filepath.Join(repoRoot(), "testdata", "oracle", t.Name()+".json")
	var rec recording
	recorded := false
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &rec) == nil {
		recorded = sameJSON(rec.Requests, in)
	}
	if why := missing(); why != "" {
		if !recorded {
			t.Skip(why + ", and testdata/oracle has no recording of these requests")
		}
		t.Log(why + ": replaying the recorded answers")
		return rec.Results
	}
	res := Live(t, reqs)
	if os.Getenv("ORACLE_RECORD") != "" {
		b, _ := json.MarshalIndent(recording{in, res}, "", " ")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	} else if recorded {
		for i := range res {
			if !eq(res[i], rec.Results[i]) {
				t.Errorf("%s is stale (answer %d changed); rerun with ORACLE_RECORD=1", filepath.Base(path), i)
				break
			}
		}
	}
	return res
}

type recording struct {
	Requests json.RawMessage   `json:"requests"`
	Results  []json.RawMessage `json:"results"`
}

// sameJSON compares decoded values: the recording's encoder escapes <, >
// and & where the oracle's doesn't.
func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// missing says why the oracle can't run here, or "" when it can.
func missing() string {
	if _, err := exec.LookPath("node"); err != nil {
		return "node not installed"
	}
	if _, err := os.Stat(filepath.Join(Dir(), "lib", "folder-sync.js")); err != nil {
		return "textarea tree not found (set TEXTAREA_DIR)"
	}
	return ""
}

// Live runs the oracle itself, skipping the test when it can't, for
// requests that differ on every run (random salts) and so can't be replayed.
func Live(t *testing.T, reqs []Request) []json.RawMessage {
	t.Helper()
	if why := missing(); why != "" {
		t.Skip(why)
	}
	in, _ := json.Marshal(reqs)
	node, _ := exec.LookPath("node")
	cmd := exec.Command(node, filepath.Join(repoRoot(), "testdata", "oracle.mjs"))
	cmd.Env = append(os.Environ(), "TEXTAREA_DIR="+Dir())
	cmd.Stdin = bytes.NewReader(in)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("oracle: %v\n%s", err, stderr.String())
	}
	var res []json.RawMessage
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("oracle output: %v", err)
	}
	return res
}

// Fixtures are texts that exercise the edges both sides must agree on.
var Fixtures = []string{
	"",
	"# Hello\n\nworld",
	"\n\n  # Spaced title  \nbody",
	"no heading at all",
	"# Title\r\nwith CRLF\r\n",
	"\ufeff# BOM title\nx",
	"Tags: #home #work/taxes item #1 C# #Ünïcode #tag_with-dash/x and `#code` **#bold**",
	"```\n#notatag\n- [ ] not a task\n```\n- [ ] real task #inbox\n- [x] done task\n  * [X] nested done\n- not a task\n1. [ ] numbered task\n-- dash",
	"Links [[Some Page]] and [[ spaced ]] and [[]] and [[a|b]]",
	"Mail me at someone.name@example.com or google:best pizza & beer",
	"CJK 日本語のテキスト 한국어 and emoji 😀👍 done. Next sentence! And? Yes…  End",
	"Para one.\n\n\nPara two\n \nPara three",
	"=SUM(A1)\n+1\n-2\n@x\n\"quoted\"\ntrailing\n",
	"Code:\n```js\nconst a = {b: 1}\n```\nafter ]]> cdata & <b>",
	"~~~\nfenced tilde\n~~~\n# heading $&#^_~%",
	"line with ##### five mid-line\n###### six",
	"true",
	"value: with colon",
	"Tab\tseparated\tline\n\nunicode — dash “quotes”",
	"\u00a0nbsp start\u3000ideographic space #tag\u00a0#after",
	"# 2026-10-25\n\n- 18:53 called the bank\n",
	"- [ ] call mum\n",
	"buy milk #home\n",
	"- [ ] task 📅 2026-10-30 ⏫ 🔁 every week\nstatus:: draft\n#work/taxes\n",
}

// Script runs another testdata script with Node and returns its stdout,
// skipping the test the same way Run does.
func Script(t *testing.T, name string, args ...string) []byte {
	t.Helper()
	if why := missing(); why != "" {
		t.Skip(why)
	}
	node, _ := exec.LookPath("node")
	cmd := exec.Command(node, append([]string{filepath.Join(repoRoot(), "testdata", name)}, args...)...)
	cmd.Env = append(os.Environ(), "TEXTAREA_DIR="+Dir())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s: %v\n%s", name, err, stderr.String())
	}
	return out
}
