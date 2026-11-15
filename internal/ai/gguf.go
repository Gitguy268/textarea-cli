package ai

// Speech to text through a llama.cpp GGUF speech model: the model and its
// mmproj projector, run by llama-mtmd-cli. The same pair the web app takes
// in its speech panel (lib/gguf.js there), with the same header check and
// the same transcript clean-up, so a pair that works in one works in the
// other. ggml-org/Qwen3-ASR-0.6B-GGUF is the one this was tested with.

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// HeaderBytes is how much of a file's head is read to find its metadata.
const HeaderBytes = 4 << 20

// GGUFInfo is what a GGUF file is to speech to text.
type GGUFInfo struct {
	Path      string
	Kind      string // "model" or "projector"
	Audio     bool   // a projector that hears
	Name      string // general.name, when the file has one
	Arch      string // general.architecture
	Projector string // clip.audio.projector_type, for projectors
}

var wantedKeys = map[string]bool{
	"general.architecture":      true,
	"general.type":              true,
	"general.name":              true,
	"clip.has_audio_encoder":    true,
	"clip.audio.projector_type": true,
}

var errEnd = errors.New("end of header")

// ReadGGUFMetadata reads the wanted keys from the start of a GGUF file.
// Arrays (a vocabulary is one) are walked past; a header cut off by the end
// of the bytes ends the walk with what was found.
func ReadGGUFMetadata(head []byte) (map[string]any, error) {
	if len(head) < 8 || string(head[:4]) != "GGUF" {
		return nil, errors.New("not a GGUF file (whisper.cpp's ggml-….bin models are a different format)")
	}
	if v := binary.LittleEndian.Uint32(head[4:]); v < 2 {
		return nil, fmt.Errorf("GGUF version %d is too old for llama.cpp", v)
	}
	at := 16
	found := map[string]any{}
	need := func(n int) error {
		if n < 0 || at+n > len(head) {
			return errEnd
		}
		return nil
	}
	u32 := func() (uint32, error) {
		if err := need(4); err != nil {
			return 0, err
		}
		at += 4
		return binary.LittleEndian.Uint32(head[at-4:]), nil
	}
	u64 := func() (int, error) {
		if err := need(8); err != nil {
			return 0, err
		}
		v := binary.LittleEndian.Uint64(head[at:])
		at += 8
		if v > math.MaxInt32 {
			return 0, errEnd
		}
		return int(v), nil
	}
	str := func() (string, error) {
		n, err := u64()
		if err != nil {
			return "", err
		}
		if err := need(n); err != nil {
			return "", err
		}
		at += n
		return string(head[at-n : at]), nil
	}
	sizes := map[uint32]int{0: 1, 1: 1, 2: 2, 3: 2, 4: 4, 5: 4, 6: 4, 7: 1, 10: 8, 11: 8, 12: 8}
	walk := func() error {
		pairs, err := u64()
		if err != nil {
			return err
		}
		for i := 0; i < pairs; i++ {
			key, err := str()
			if err != nil {
				return err
			}
			typ, err := u32()
			if err != nil {
				return err
			}
			switch typ {
			case 8:
				v, err := str()
				if err != nil {
					return err
				}
				if wantedKeys[key] {
					found[key] = v
				}
			case 9:
				itemType, err := u32()
				if err != nil {
					return err
				}
				count, err := u64()
				if err != nil {
					return err
				}
				if itemType == 8 {
					for j := 0; j < count; j++ {
						if _, err := str(); err != nil {
							return err
						}
					}
				} else if size, ok := sizes[itemType]; ok {
					if err := need(size * count); err != nil {
						return err
					}
					at += size * count
				} else {
					return fmt.Errorf("unknown GGUF array type %d", itemType)
				}
			default:
				size, ok := sizes[typ]
				if !ok {
					return fmt.Errorf("unknown GGUF value type %d", typ)
				}
				if err := need(size); err != nil {
					return err
				}
				if wantedKeys[key] && typ == 7 {
					found[key] = head[at] != 0
				}
				at += size
			}
		}
		return nil
	}
	if err := walk(); err != nil && !errors.Is(err, errEnd) {
		return nil, err
	}
	return found, nil
}

// InspectGGUF reads one file's header.
func InspectGGUF(path string) (GGUFInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return GGUFInfo{}, err
	}
	defer f.Close()
	head := make([]byte, HeaderBytes)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return GGUFInfo{}, err
	}
	meta, err := ReadGGUFMetadata(head[:n])
	if err != nil {
		return GGUFInfo{}, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	info := GGUFInfo{Path: path, Kind: "model"}
	info.Arch, _ = meta["general.architecture"].(string)
	info.Name, _ = meta["general.name"].(string)
	if t, _ := meta["general.type"].(string); info.Arch == "clip" || t == "mmproj" {
		info.Kind = "projector"
		info.Audio, _ = meta["clip.has_audio_encoder"].(bool)
		info.Projector, _ = meta["clip.audio.projector_type"].(string)
	}
	return info, nil
}

// PairGGUF sorts files into the model and projector llama.cpp needs, or
// says what is missing, in the web app's words.
func PairGGUF(files []GGUFInfo) (model, projector GGUFInfo, err error) {
	var models, projectors []GGUFInfo
	for _, f := range files {
		if f.Kind == "projector" {
			projectors = append(projectors, f)
		} else {
			models = append(models, f)
		}
	}
	switch {
	case len(models) == 0 && len(projectors) > 0:
		return model, projector, errors.New("pick the language model too: the .gguf file beside the mmproj one")
	case len(models) > 0 && len(projectors) == 0:
		return model, projector, errors.New("pick the projector too: llama.cpp hears audio through the mmproj-….gguf file that comes with the model (a single-file GGUF made for transcribe.cpp will not run here)")
	case len(models) != 1 || len(projectors) != 1:
		return model, projector, errors.New("pick two files: one model and one mmproj projector")
	case !projectors[0].Audio:
		return model, projector, fmt.Errorf("%s reads images, not audio; pick a speech model's mmproj", filepath.Base(projectors[0].Path))
	}
	return models[0], projectors[0], nil
}

// FindProjector looks for the audio mmproj beside a model: any
// mmproj*.gguf in the same folder whose header says it hears.
func FindProjector(model string) string {
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(model), "*mmproj*.gguf"))
	for _, m := range matches {
		if info, err := InspectGGUF(m); err == nil && info.Kind == "projector" && info.Audio {
			return m
		}
	}
	return ""
}

var (
	thinkRe  = regexp.MustCompile(`(?s)<think>.*?</think>`)
	asrRe    = regexp.MustCompile(`(?i)(^|\s)language\s+[^\s<]*\s*<asr_text>`)
	asrTagRe = regexp.MustCompile(`(?i)</?asr_text>`)
	labelRe  = regexp.MustCompile(`(?i)^\s*(transcript(ion)?|text)\s*:\s*`)
	quoteRe  = regexp.MustCompile(`(?s)^\s*["“](.*)["”]\s*$`)
	spaceRe  = regexp.MustCompile(`[ \t]+`)
)

// CleanTranscript takes off the markers some models add, as the web app does.
func CleanTranscript(s string) string {
	s = thinkRe.ReplaceAllString(s, "")
	s = asrRe.ReplaceAllString(s, "$1")
	s = asrTagRe.ReplaceAllString(s, "")
	s = labelRe.ReplaceAllString(s, "")
	s = quoteRe.ReplaceAllString(s, "$1")
	s = spaceRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// Transcriber turns an audio file into text.
type Transcriber interface {
	Name() string
	Transcribe(ctx context.Context, audio, language string) (string, error)
}

// Name is the model file's name.
func (w Whisper) Name() string { return "whisper.cpp · " + filepath.Base(w.Model) }

// GGUFSpeech is llama-mtmd-cli with a model and its audio projector.
type GGUFSpeech struct{ Bin, Model, Projector string }

// Name is the model file's name.
func (g GGUFSpeech) Name() string { return "llama.cpp · " + filepath.Base(g.Model) }

// FindSpeech picks the speech backend the settings point at: a .gguf model
// (and its projector, found beside it when not set) through llama-mtmd-cli,
// or else whisper.cpp. ok is false when neither can run.
func FindSpeech(model, projector, whisperModel string) (Transcriber, bool) {
	if strings.HasSuffix(strings.ToLower(model), ".gguf") {
		bin, err := exec.LookPath("llama-mtmd-cli")
		if err != nil {
			return nil, false
		}
		if projector == "" {
			projector = FindProjector(model)
		}
		if projector == "" {
			return nil, false
		}
		return GGUFSpeech{bin, model, projector}, true
	}
	if model != "" && whisperModel == "" {
		whisperModel = model
	}
	if w, ok := FindWhisper(whisperModel); ok {
		return w, true
	}
	return nil, false
}

// CheckGGUFPair reads both headers and says whether they make a pair.
func CheckGGUFPair(model, projector string) error {
	var infos []GGUFInfo
	for _, p := range []string{model, projector} {
		info, err := InspectGGUF(p)
		if err != nil {
			return err
		}
		infos = append(infos, info)
	}
	_, _, err := PairGGUF(infos)
	return err
}

// SpeechInstruction is the request each recording is sent with. Speech-only
// models such as Qwen3-ASR ignore it; chat models with ears need it.
func SpeechInstruction(language string) string {
	s := "Transcribe the speech in the audio exactly as spoken. Reply with the transcript only."
	if language != "" && language != "auto" {
		s += fmt.Sprintf(" The speech is in the language with code %q.", language)
	}
	return s
}

// llama.cpp decodes these itself; anything else goes through ffmpeg first.
var mtmdAudio = map[string]bool{".wav": true, ".mp3": true, ".flac": true}

// WindowSeconds is how much audio one request carries, as in the web app:
// fed a long file whole, llama.cpp's own windowing dropped sentences.
const WindowSeconds = 30

// Transcribe runs llama-mtmd-cli on a file, thirty seconds at a time.
func (g GGUFSpeech) Transcribe(ctx context.Context, audio, language string) (string, error) {
	input := audio
	ext := strings.ToLower(filepath.Ext(audio))
	ff, ffErr := exec.LookPath("ffmpeg")
	if ext != ".wav" && ffErr != nil && !mtmdAudio[ext] {
		return "", errors.New("only .wav, .mp3 and .flac files can be transcribed without ffmpeg")
	}
	if ext != ".wav" && ffErr == nil {
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
	parts, cleanup, err := SplitWAV(input, WindowSeconds)
	if err != nil {
		// Not a WAV this can cut (an mp3 with no ffmpeg, say): whole.
		parts, cleanup = []string{input}, func() {}
	}
	defer cleanup()
	var said []string
	for _, part := range parts {
		text, err := g.run(ctx, part, language)
		if err != nil {
			return "", err
		}
		if text != "" {
			said = append(said, text)
		}
	}
	return strings.Join(said, " "), nil
}

func (g GGUFSpeech) run(ctx context.Context, input, language string) (string, error) {
	cmd := exec.CommandContext(ctx, g.Bin,
		"-m", g.Model, "--mmproj", g.Projector, "--audio", input,
		"-p", SpeechInstruction(language), "--temp", "0", "-n", "2048")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("llama-mtmd-cli stopped: %w", ctx.Err())
		}
		msg := strings.TrimSpace(stderr.String())
		if i := strings.LastIndex(msg, "\n"); i >= 0 {
			msg = msg[i+1:]
		}
		return "", fmt.Errorf("llama-mtmd-cli: %s", firstLine(msg, err.Error()))
	}
	return CleanTranscript(stdout.String()), nil
}

func firstLine(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// SplitWAV cuts a PCM WAV file into windows of at most seconds each, as
// temporary files. A file that fits in one window comes back as itself.
func SplitWAV(path string, seconds int) ([]string, func(), error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, nil, errors.New("not a WAV file")
	}
	var fmtChunk, pcm []byte
	for at := 12; at+8 <= len(data); {
		id, size := string(data[at:at+4]), int(binary.LittleEndian.Uint32(data[at+4:]))
		body := at + 8
		if size < 0 || body+size > len(data) {
			size = len(data) - body
		}
		switch id {
		case "fmt ":
			fmtChunk = data[body : body+size]
		case "data":
			pcm = data[body : body+size]
		}
		at = body + size + size%2
	}
	if len(fmtChunk) < 16 || pcm == nil || binary.LittleEndian.Uint16(fmtChunk) != 1 {
		return nil, nil, errors.New("not a PCM WAV file")
	}
	rate := int(binary.LittleEndian.Uint32(fmtChunk[4:]))
	block := int(binary.LittleEndian.Uint16(fmtChunk[12:]))
	window := seconds * rate * block
	if block <= 0 || window <= 0 {
		return nil, nil, errors.New("not a WAV file this can cut")
	}
	if len(pcm) <= window {
		return []string{path}, func() {}, nil
	}
	var parts []string
	cleanup := func() {
		for _, p := range parts {
			os.Remove(p)
		}
	}
	for start := 0; start < len(pcm); start += window {
		end := min(start+window, len(pcm))
		f, err := os.CreateTemp("", "text-window-*.wav")
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		parts = append(parts, f.Name())
		head := make([]byte, 0, 44)
		head = append(head, "RIFF"...)
		head = binary.LittleEndian.AppendUint32(head, uint32(36+end-start))
		head = append(head, "WAVEfmt "...)
		head = binary.LittleEndian.AppendUint32(head, 16)
		head = append(head, fmtChunk[:16]...)
		head = append(head, "data"...)
		head = binary.LittleEndian.AppendUint32(head, uint32(end-start))
		_, werr := f.Write(append(head, pcm[start:end]...))
		cerr := f.Close()
		if werr != nil || cerr != nil {
			cleanup()
			return nil, nil, errors.Join(werr, cerr)
		}
	}
	return parts, cleanup, nil
}
