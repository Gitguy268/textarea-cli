// Package ai is the optional on-device AI: the web app's writing tools,
// through a local model, and speech to text through whisper.cpp. Nothing
// here downloads anything; each feature exists only when its tool is
// already on this machine, and nothing leaves it.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Tool is one rewrite, with the web app's own instruction.
type Tool struct{ ID, Label, Instruction string }

// Tools are lib/writing-tools.js TOOLS, minus the image ones.
var Tools = []Tool{
	{"improve", "Improve wording", "Rewrite the text so it reads better: smoother phrasing, clearer sentences, the same meaning, and the same language as the original."},
	{"shorten", "Shorten", "Shorten the text, keeping every fact and dropping only what is redundant. Keep the same language as the original."},
	{"summarize", "Summarize", "Summarize the text in a few sentences that make sense on their own. Use the same language as the original."},
	{"fix", "Fix spelling & grammar", "Fix every spelling and grammar mistake. Change nothing else: the wording is the author’s."},
	{"casual", "Make more casual", "Rewrite the text in a casual, conversational tone, keeping its meaning. Use the same language as the original."},
}

// NoteLimit is how much of a note the prompt carries, as in the web app.
const NoteLimit = 24_000

// Message is one chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// BuildPrompt is buildPrompt for the text tools.
func BuildPrompt(toolID, text, extra string) ([]Message, bool, error) {
	var tool *Tool
	for i := range Tools {
		if Tools[i].ID == toolID {
			tool = &Tools[i]
		}
	}
	if tool == nil {
		return nil, false, fmt.Errorf("unknown writing tool: %s", toolID)
	}
	system := tool.Instruction + "\n\nReply with the finished text only: no preamble, no explanation, and no quotation marks around it."
	units := []rune(text)
	truncated := len(units) > NoteLimit
	if truncated {
		text = string(units[:NoteLimit])
	}
	user := "Text:\n\n" + text
	if e := strings.TrimSpace(extra); e != "" {
		user += "\n\nExtra instructions: " + e
	}
	return []Message{{"system", system}, {"user", user}}, truncated, nil
}

// Writer is a local model that answers chat messages.
type Writer interface {
	Name() string
	Chat(ctx context.Context, msgs []Message) (string, error)
}

// FindWriter picks the first available backend: a llama-server or
// llamafile already listening on $TEXT_LLM_URL, a GGUF model run with
// llama-server, a .llamafile, or Ollama with a model pulled. nil when none.
func FindWriter(model string) Writer {
	if url := os.Getenv("TEXT_LLM_URL"); url != "" {
		return &openAI{url: strings.TrimRight(url, "/"), name: url}
	}
	if model != "" {
		if strings.HasSuffix(strings.ToLower(model), ".llamafile") {
			return &spawned{bin: model, args: []string{"--server", "--nobrowser", "-m"}, model: model}
		}
		if bin, err := exec.LookPath("llama-server"); err == nil {
			return &spawned{bin: bin, model: model}
		}
	}
	if _, err := exec.LookPath("ollama"); err == nil {
		if m := ollamaModel(model); m != "" {
			return &openAI{url: "http://127.0.0.1:11434", model: m, name: "Ollama · " + m, ollama: true}
		}
	}
	return nil
}

// ollamaModel is the configured Ollama model, or the first one pulled.
func ollamaModel(configured string) string {
	if strings.HasPrefix(configured, "ollama:") {
		return strings.TrimPrefix(configured, "ollama:")
	}
	out, err := exec.Command("ollama", "list").Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return ""
	}
	fields := strings.Fields(lines[1])
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

type openAI struct {
	url, model, name string
	ollama           bool
}

func (o *openAI) Name() string { return o.name }

func (o *openAI) Chat(ctx context.Context, msgs []Message) (string, error) {
	body := map[string]any{"messages": msgs, "stream": false, "temperature": 0.3}
	if o.model != "" {
		body["model"] = o.model
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", o.url+"/v1/chat/completions", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if o.ollama {
			return "", errors.New("Ollama is installed but not running: start it with `ollama serve`")
		}
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
		Error any `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("the model answered nothing (%v)", out.Error)
	}
	return Clean(out.Choices[0].Message.Content), nil
}

// spawned starts llama-server (or a llamafile) for one request.
type spawned struct {
	bin, model string
	args       []string
}

func (s *spawned) Name() string { return filepath.Base(s.model) }

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func (s *spawned) Chat(ctx context.Context, msgs []Message) (string, error) {
	port, err := freePort()
	if err != nil {
		return "", err
	}
	args := append([]string{}, s.args...)
	if len(args) == 0 {
		args = []string{"-m"}
	}
	args = append(args, s.model, "--host", "127.0.0.1", "--port", fmt.Sprint(port), "-c", "8192")
	cmd := exec.CommandContext(ctx, s.bin, args...)
	if err := cmd.Start(); err != nil {
		return "", err
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	for deadline := time.Now().Add(2 * time.Minute); ; {
		if resp, err := http.Get(url + "/health"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			return "", errors.New("the model did not start in two minutes")
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	return (&openAI{url: url}).Chat(ctx, msgs)
}

// Clean strips what small models wrap answers in: think blocks and quotes.
func Clean(s string) string {
	if i := strings.Index(s, "</think>"); i >= 0 {
		s = s[i+len("</think>"):]
	}
	s = strings.TrimSpace(s)
	for _, q := range [][2]string{{`"`, `"`}, {"“", "”"}} {
		if len(s) > 1 && strings.HasPrefix(s, q[0]) && strings.HasSuffix(s, q[1]) {
			s = strings.TrimSpace(s[len(q[0]) : len(s)-len(q[1])])
		}
	}
	return s
}

// ------------------------------------------------------------------ speech

// Whisper is whisper.cpp's command line tool and a ggml model.
type Whisper struct{ Bin, Model string }

// FindWhisper is whisper-cli plus a model: the configured one, or the first
// ggml-*.bin in the usual places. ok is false when either is missing.
func FindWhisper(model string) (Whisper, bool) {
	bin := ""
	for _, name := range []string{"whisper-cli", "whisper-cpp", "whisper.cpp"} {
		if p, err := exec.LookPath(name); err == nil {
			bin = p
			break
		}
	}
	if bin == "" {
		return Whisper{}, false
	}
	if model == "" {
		model = findModel()
	}
	if model == "" {
		return Whisper{}, false
	}
	return Whisper{bin, model}, true
}

func findModel() string {
	home, _ := os.UserHomeDir()
	var dirs []string
	if d := os.Getenv("WHISPER_MODELS"); d != "" {
		dirs = append(dirs, d)
	}
	dirs = append(dirs,
		filepath.Join(home, ".cache", "whisper.cpp"),
		filepath.Join(home, ".cache", "whisper"),
		filepath.Join(home, ".local", "share", "whisper.cpp"),
		"/opt/homebrew/share/whisper-cpp",
		"/usr/local/share/whisper-cpp",
		"/usr/share/whisper.cpp",
	)
	for _, d := range dirs {
		matches, _ := filepath.Glob(filepath.Join(d, "ggml-*.bin"))
		if len(matches) > 0 {
			return matches[0]
		}
	}
	return ""
}

// Transcribe turns an audio file into text. whisper.cpp reads 16 kHz WAV, so
// anything else goes through ffmpeg first.
func (w Whisper) Transcribe(ctx context.Context, audio, language string) (string, error) {
	input := audio
	if !strings.HasSuffix(strings.ToLower(audio), ".wav") {
		ff, err := exec.LookPath("ffmpeg")
		if err != nil {
			return "", errors.New("only .wav files can be transcribed without ffmpeg")
		}
		tmp, err := os.CreateTemp("", "text-audio-*.wav")
		if err != nil {
			return "", err
		}
		tmp.Close()
		defer os.Remove(tmp.Name())
		if out, err := exec.CommandContext(ctx, ff, "-y", "-loglevel", "error", "-i", audio, "-ar", "16000", "-ac", "1", tmp.Name()).CombinedOutput(); err != nil {
			return "", fmt.Errorf("ffmpeg: %s", strings.TrimSpace(string(out)))
		}
		input = tmp.Name()
	}
	if language == "" {
		language = "auto"
	}
	cmd := exec.CommandContext(ctx, w.Bin, "-m", w.Model, "-f", input, "-l", language, "-nt", "-np")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("whisper: %s", strings.TrimSpace(stderr.String()))
	}
	var lines []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, " "), nil
}

// Recorder captures the microphone to a WAV file until stopped.
type Recorder struct {
	cmd  *exec.Cmd
	stop func()
	Path string
}

// CanRecord reports whether a recorder exists: ffmpeg or sox's rec.
func CanRecord() bool {
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		return true
	}
	_, err := exec.LookPath("rec")
	return err == nil
}

// StartRecording begins capturing the default microphone.
func StartRecording() (*Recorder, error) {
	tmp, err := os.CreateTemp("", "text-recording-*.wav")
	if err != nil {
		return nil, err
	}
	tmp.Close()
	var cmd *exec.Cmd
	useFFmpeg := false
	if ff, err := exec.LookPath("ffmpeg"); err == nil {
		useFFmpeg = true
		var input []string
		switch runtime.GOOS {
		case "darwin":
			input = []string{"-f", "avfoundation", "-i", ":0"}
		case "windows":
			input = []string{"-f", "dshow", "-i", "audio=default"}
		default:
			input = []string{"-f", "pulse", "-i", "default"}
		}
		args := append([]string{"-y", "-loglevel", "error"}, input...)
		args = append(args, "-ar", "16000", "-ac", "1", tmp.Name())
		cmd = exec.Command(ff, args...)
	} else if rec, err := exec.LookPath("rec"); err == nil {
		cmd = exec.Command(rec, "-q", "-r", "16000", "-c", "1", tmp.Name())
	} else {
		return nil, errors.New("recording needs ffmpeg or sox")
	}
	// ffmpeg stops cleanly on "q" on its stdin, sox on an interrupt.
	stdin, _ := cmd.StdinPipe()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	r := &Recorder{cmd: cmd, Path: tmp.Name()}
	r.stop = func() {
		if useFFmpeg {
			stdin.Write([]byte("q"))
			stdin.Close()
		} else {
			cmd.Process.Signal(os.Interrupt)
		}
	}
	return r, nil
}

// Stop ends the recording and waits for the file to be complete.
func (r *Recorder) Stop() error {
	if r.stop != nil {
		r.stop()
	}
	done := make(chan error, 1)
	go func() { done <- r.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		r.cmd.Process.Kill()
		<-done
	}
	if info, err := os.Stat(r.Path); err != nil || info.Size() < 1024 {
		return errors.New("nothing was recorded: check the microphone permission for this terminal")
	}
	return nil
}
