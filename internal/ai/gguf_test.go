package ai

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gguf writes a GGUF header the way llama.cpp does: string, bool, uint32 and
// string-array values.
func gguf(pairs ...any) []byte {
	var b bytes.Buffer
	u32 := func(v uint32) { binary.Write(&b, binary.LittleEndian, v) }
	u64 := func(v uint64) { binary.Write(&b, binary.LittleEndian, v) }
	str := func(s string) { u64(uint64(len(s))); b.WriteString(s) }
	b.WriteString("GGUF")
	u32(3)
	u64(0)
	u64(uint64(len(pairs) / 2))
	for i := 0; i < len(pairs); i += 2 {
		str(pairs[i].(string))
		switch v := pairs[i+1].(type) {
		case string:
			u32(8)
			str(v)
		case bool:
			u32(7)
			if v {
				b.WriteByte(1)
			} else {
				b.WriteByte(0)
			}
		case []string:
			u32(9)
			u32(8)
			u64(uint64(len(v)))
			for _, s := range v {
				str(s)
			}
		case int:
			u32(4)
			u32(uint32(v))
		}
	}
	return b.Bytes()
}

func write(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

var (
	modelHead     = gguf("general.architecture", "qwen3vl", "general.type", "model", "tokenizer.ggml.tokens", []string{"a", "b"}, "general.name", "Qwen3 ASR", "qwen3vl.block_count", 28)
	projectorHead = gguf("general.architecture", "clip", "general.type", "mmproj", "clip.has_audio_encoder", true, "clip.audio.projector_type", "qwen3a")
	visionHead    = gguf("general.architecture", "clip", "clip.has_vision_encoder", true)
)

func TestReadGGUFMetadata(t *testing.T) {
	meta, err := ReadGGUFMetadata(modelHead)
	if err != nil {
		t.Fatal(err)
	}
	if meta["general.architecture"] != "qwen3vl" || meta["general.name"] != "Qwen3 ASR" {
		t.Fatalf("got %v", meta)
	}
	if _, err := ReadGGUFMetadata([]byte("RIFF....WAVE")); err == nil || !strings.Contains(err.Error(), "not a GGUF") {
		t.Fatalf("want not-a-GGUF, got %v", err)
	}
	cut, err := ReadGGUFMetadata(modelHead[:75])
	if err != nil || cut["general.architecture"] != "qwen3vl" || len(cut) != 1 {
		t.Fatalf("cut header: %v %v", cut, err)
	}
}

func TestPairGGUF(t *testing.T) {
	model, _ := InspectGGUF(write(t, "m.gguf", modelHead))
	projector, _ := InspectGGUF(write(t, "mmproj.gguf", projectorHead))
	vision, _ := InspectGGUF(write(t, "mmproj-vision.gguf", visionHead))
	if projector.Kind != "projector" || !projector.Audio || projector.Projector != "qwen3a" {
		t.Fatalf("projector %+v", projector)
	}
	if m, p, err := PairGGUF([]GGUFInfo{projector, model}); err != nil || m.Path != model.Path || p.Path != projector.Path {
		t.Fatalf("pair: %v", err)
	}
	for _, tc := range []struct {
		files []GGUFInfo
		want  string
	}{
		{[]GGUFInfo{model}, "projector too"},
		{[]GGUFInfo{projector}, "language model too"},
		{[]GGUFInfo{model, model, projector}, "two files"},
		{[]GGUFInfo{model, vision}, "reads images"},
	} {
		if _, _, err := PairGGUF(tc.files); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("want %q, got %v", tc.want, err)
		}
	}
}

func TestFindProjector(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "model.gguf"), modelHead, 0o644)
	os.WriteFile(filepath.Join(dir, "mmproj-vision.gguf"), visionHead, 0o644)
	if got := FindProjector(filepath.Join(dir, "model.gguf")); got != "" {
		t.Fatalf("a vision projector is not a speech one: %s", got)
	}
	os.WriteFile(filepath.Join(dir, "mmproj-audio.gguf"), projectorHead, 0o644)
	if got := FindProjector(filepath.Join(dir, "model.gguf")); filepath.Base(got) != "mmproj-audio.gguf" {
		t.Fatalf("got %q", got)
	}
}

func TestCleanTranscript(t *testing.T) {
	for in, want := range map[string]string{
		"language English<asr_text>Ask not.":              "Ask not.",
		"<think>hm</think>\nTranscript: \"Hello there.\"": "Hello there.",
		"language None<asr_text>":                         "",
		"  plain words  ":                                 "plain words",
	} {
		if got := CleanTranscript(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

// wav is a 16-bit mono PCM file of n samples at rate.
func wav(n, rate int) []byte {
	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+n*2))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(rate), uint32(rate * 2), uint16(2), uint16(16)} {
		binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(n*2))
	b.Write(make([]byte, n*2))
	return b.Bytes()
}

func TestSplitWAV(t *testing.T) {
	short := write(t, "short.wav", wav(16000*10, 16000))
	parts, done, err := SplitWAV(short, 30)
	if err != nil || len(parts) != 1 || parts[0] != short {
		t.Fatalf("short: %v %v", parts, err)
	}
	done()
	long := write(t, "long.wav", wav(16000*65, 16000))
	parts, done, err = SplitWAV(long, 30)
	if err != nil || len(parts) != 3 {
		t.Fatalf("long: %v %v", parts, err)
	}
	var sizes []int64
	for _, p := range parts {
		st, _ := os.Stat(p)
		sizes = append(sizes, st.Size())
	}
	if sizes[0] != 44+30*16000*2 || sizes[2] != 44+5*16000*2 {
		t.Fatalf("sizes %v", sizes)
	}
	done()
	if _, err := os.Stat(parts[0]); !os.IsNotExist(err) {
		t.Fatal("windows were not removed")
	}
	if _, _, err := SplitWAV(write(t, "x.mp3", []byte("ID3....")), 30); err == nil {
		t.Fatal("an mp3 is not a WAV")
	}
}

// TestGGUFSpeechReal runs a real pair when TEXT_GGUF_DIR holds one (for
// example ggml-org/Qwen3-ASR-0.6B-GGUF) and llama-mtmd-cli is installed.
func TestGGUFSpeechReal(t *testing.T) {
	dir := os.Getenv("TEXT_GGUF_DIR")
	if dir == "" {
		t.Skip("TEXT_GGUF_DIR not set")
	}
	if _, err := exec.LookPath("llama-mtmd-cli"); err != nil {
		t.Skip("no llama-mtmd-cli")
	}
	models, _ := filepath.Glob(filepath.Join(dir, "*.gguf"))
	var model string
	for _, m := range models {
		if !strings.Contains(filepath.Base(m), "mmproj") {
			model = m
		}
	}
	s, ok := FindSpeech(model, "", "")
	if !ok {
		t.Fatal("no speech backend for", model)
	}
	audio := os.Getenv("TEXT_GGUF_AUDIO")
	if audio == "" {
		t.Skip("TEXT_GGUF_AUDIO not set (a recording of JFK's \"ask not\" line)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	text, err := s.Transcribe(ctx, audio, "en")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: %s", s.Name(), text)
	if !strings.Contains(strings.ToLower(text), "ask not what your country can do for you") {
		t.Fatalf("wrong transcript: %q", text)
	}
}
