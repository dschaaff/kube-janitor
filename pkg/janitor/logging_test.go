package janitor

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// at is the moment every rendered line in these tests is stamped with, so a
// case can state the whole line it expects.
var at = time.Date(2026, 8, 24, 13, 56, 40, 500_000_000, time.UTC)

// documentedJSONFormat is the JSON log format the README offers, written once
// so that the cases below cannot pin different versions of it.
const documentedJSONFormat = `{"level":"%(levelname)s","ts":"%(created)s","logger":"%(name)s","msg":"%(message)s"}`

func TestLogFormatRenders(t *testing.T) {
	tests := []struct {
		name    string
		format  string
		level   logLevel
		message string
		want    string
	}{
		{
			name:    "the default format",
			format:  defaultLogFormat,
			level:   levelInfo,
			message: "Pod default/web: delete, deadline ... (janitor/ttl)",
			want:    "2026-08-24 13:56:40 INFO: Pod default/web: delete, deadline ... (janitor/ttl)",
		},
		{
			name:    "a format naming every field",
			format:  "%(asctime)s|%(created)s|%(levelname)s|%(name)s|%(message)s",
			level:   levelError,
			message: "boom",
			want:    "2026-08-24 13:56:40|1787579800.500|ERROR|kube-janitor|boom",
		},
		{
			name:    "literal text with no placeholder at all",
			format:  "nothing to interpolate",
			level:   levelInfo,
			message: "ignored",
			want:    "nothing to interpolate",
		},
		{
			name:    "a placeholder repeated",
			format:  "%(levelname)s %(levelname)s",
			level:   levelWarning,
			message: "ignored",
			want:    "WARNING WARNING",
		},
		{
			// %% is how the Python format this syntax comes from writes a
			// literal percent.
			name:    "an escaped percent sign",
			format:  "100%% sure: %(message)s",
			level:   levelInfo,
			message: "yes",
			want:    "100% sure: yes",
		},
		{
			// A percent that opens nothing is literal text: the format is not a
			// Go format string, so a stray one is not an error either.
			name:    "a stray percent sign",
			format:  "50% off: %(message)s",
			level:   levelInfo,
			message: "yes",
			want:    "50% off: yes",
		},
		{
			name:    "an empty format",
			format:  "",
			level:   levelInfo,
			message: "ignored",
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			format, err := parseLogFormat(tt.format)
			if err != nil {
				t.Fatalf("parseLogFormat(%q) returned %v", tt.format, err)
			}

			if got := format.render(tt.level, tt.message, at); got != tt.want {
				t.Errorf("render() = %q, want %q", got, tt.want)
			}
		})
	}
}

// The README offers a JSON log format, so one has to come out as parseable JSON
// rather than as a line that merely looks like it.
func TestLogFormatRendersTheDocumentedJSONExample(t *testing.T) {
	format, err := parseLogFormat(documentedJSONFormat)
	if err != nil {
		t.Fatalf("parseLogFormat returned %v for the format the README documents", err)
	}

	line := format.render(levelInfo, "cleaning up", at)

	var got map[string]string
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("the rendered line is not JSON: %v\nline: %s", err, line)
	}

	want := map[string]string{
		"level":  "INFO",
		"ts":     "1787579800.500",
		"logger": "kube-janitor",
		"msg":    "cleaning up",
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %q, want %q", key, got[key], value)
		}
	}
}

// A run says which namespace it was listing, and it says it with quotes around
// the name, so the JSON the README offers has to survive a message that carries
// quotes of its own. This is the message a failed listing actually produces.
func TestLogFormatEscapesAQuotedFieldsValue(t *testing.T) {
	format, err := parseLogFormat(documentedJSONFormat)
	if err != nil {
		t.Fatalf("parseLogFormat returned %v", err)
	}

	message := "Error listing pods in namespace \"default\": a \\, a\nnewline and a \x01"
	line := format.render(levelError, message, at)

	var got map[string]string
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("the rendered line is not JSON: %v\nline: %s", err, line)
	}
	if got["msg"] != message {
		t.Errorf("msg = %q, want the message unchanged: %q", got["msg"], message)
	}
}

// Escaping follows the quotes the format itself wrote: a field that falls
// inside an open string is escaped so it stays inside it, and a field outside
// one is written through, which is what keeps a plain text line reading like
// one. Where the field sits within the string does not matter — only that it is
// in one.
func TestLogFormatEscapesWhatFallsInsideAString(t *testing.T) {
	const message = `namespace "default" \ here`
	const escaped = `namespace \"default\" \\ here`

	tests := []struct {
		name   string
		format string
		want   string
	}{
		{
			name:   "the default format opens no string",
			format: defaultLogFormat,
			want:   `2026-08-24 13:56:40 ERROR: ` + message,
		},
		{
			name:   "a field inside a string is escaped",
			format: `msg="%(message)s"`,
			want:   `msg="` + escaped + `"`,
		},
		{
			// The field does not have to touch the quotes to be inside them.
			// Rendering it raw here would end the string early.
			name:   "a field further inside a string is escaped too",
			format: `{"msg":"deleting %(message)s now"}`,
			want:   `{"msg":"deleting ` + escaped + ` now"}`,
		},
		{
			// Both fields sit in one string, though the text between them is
			// neither preceded nor followed by a quote.
			name:   "every field in one string is escaped",
			format: `{"msg":"%(levelname)s %(message)s"}`,
			want:   `{"msg":"ERROR ` + escaped + `"}`,
		},
		{
			name:   "an unterminated string still holds the field",
			format: `msg="%(message)s`,
			want:   `msg="` + escaped,
		},
		{
			name:   "a field after the string closed is written through",
			format: `{"a":"x"} %(message)s`,
			want:   `{"a":"x"} ` + message,
		},
		{
			// A quote the format escaped does not open a string.
			name:   "an escaped quote opens nothing",
			format: `he said \" %(message)s`,
			want:   `he said \" ` + message,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			format, err := parseLogFormat(tt.format)
			if err != nil {
				t.Fatalf("parseLogFormat(%q) returned %v", tt.format, err)
			}

			if got := format.render(levelError, message, at); got != tt.want {
				t.Errorf("render() = %q, want %q", got, tt.want)
			}
		})
	}
}

// The README promises that a format building JSON keeps producing JSON. That
// has to hold for a format an operator wrote, not only for the one the README
// prints, so these are JSON formats that place their fields differently.
func TestLogFormatKeepsAnyJSONFormatParseable(t *testing.T) {
	message := "listing pods in namespace \"default\": C:\\temp\\report\nfailed"

	formats := []string{
		documentedJSONFormat,
		`{"msg":"prefix %(message)s"}`,
		`{"msg":"%(message)s suffix"}`,
		`{"msg":"%(levelname)s %(message)s"}`,
		`{"level":"%(levelname)s","msg":"deleting %(message)s now"}`,
	}

	for _, f := range formats {
		t.Run(f, func(t *testing.T) {
			format, err := parseLogFormat(f)
			if err != nil {
				t.Fatalf("parseLogFormat returned %v", err)
			}

			line := format.render(levelError, message, at)

			var got map[string]string
			if err := json.Unmarshal([]byte(line), &got); err != nil {
				t.Fatalf("the rendered line is not JSON: %v\nline: %s", err, line)
			}
			if !strings.Contains(got["msg"], message) {
				t.Errorf("msg = %q, want it to carry the message unchanged: %q", got["msg"], message)
			}
		})
	}
}

// A format that cannot be rendered is refused where it is written down, not
// silently turned into blank lines at run time.
func TestParseLogFormatRefusesAFormatItCannotRender(t *testing.T) {
	tests := []struct {
		name   string
		format string
		want   string
	}{
		{
			name:   "a placeholder the janitor does not know",
			format: "%(filename)s: %(message)s",
			want:   "unknown placeholder %(filename)s",
		},
		{
			name:   "a placeholder that is never closed",
			format: "%(message: hello",
			want:   "unclosed placeholder",
		},
		{
			name:   "a placeholder missing its conversion",
			format: "%(message) hello",
			want:   "must be written %(message)s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseLogFormat(tt.format)

			if err == nil {
				t.Fatalf("parseLogFormat(%q) returned no error", tt.format)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("parseLogFormat(%q) = %v, want it to mention %q", tt.format, err, tt.want)
			}
		})
	}
}

// An unknown placeholder is a typo more often than not, so the error says what
// could have been written instead.
func TestParseLogFormatNamesThePlaceholdersItAccepts(t *testing.T) {
	_, err := parseLogFormat("%(levelnames)s")
	if err == nil {
		t.Fatal("parseLogFormat accepted an unknown placeholder")
	}

	for _, want := range []string{"%(asctime)s", "%(created)s", "%(levelname)s", "%(message)s", "%(name)s"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not offer %s: %v", want, err)
		}
	}
}

// The level decides whether a line is written at all, which is what --debug and
// --quiet control.
func TestLoggerWritesTheLevelsTheConfigAdmits(t *testing.T) {
	tests := []struct {
		name string
		cfg  *Config
		want []string
	}{
		{
			name: "an ordinary run",
			cfg:  &Config{},
			want: []string{"INFO", "WARNING", "ERROR"},
		},
		{
			name: "a run being diagnosed",
			cfg:  &Config{Debug: true},
			want: []string{"DEBUG", "INFO", "WARNING", "ERROR"},
		},
		{
			// A quiet run still has to report what went wrong.
			name: "a quiet run",
			cfg:  &Config{Quiet: true},
			want: []string{"WARNING", "ERROR"},
		},
		{
			// Asking for both is asking for a diagnosis, and a diagnosis that
			// left out the ordinary course of the run would be a poor one.
			name: "a quiet run being diagnosed",
			cfg:  &Config{Debug: true, Quiet: true},
			want: []string{"DEBUG", "INFO", "WARNING", "ERROR"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			// The line is just the level, so what was written reads as the list
			// of levels that got through.
			tt.cfg.LogFormat = "%(levelname)s"
			logger := NewLogger(tt.cfg, &out)

			logger.Debugf("a debug line")
			logger.Infof("an info line")
			logger.Warnf("a warning line")
			logger.Errorf("an error line")

			want := strings.Join(tt.want, "\n") + "\n"
			if got := out.String(); got != want {
				t.Errorf("wrote levels %q, want %q", got, want)
			}
		})
	}
}

func TestLoggerFormatsItsArguments(t *testing.T) {
	var out strings.Builder
	logger := NewLogger(&Config{LogFormat: "%(message)s"}, &out)

	logger.Infof("deleted %d %s", 3, "pods")

	if got := out.String(); got != "deleted 3 pods\n" {
		t.Errorf("wrote %q, want %q", got, "deleted 3 pods\n")
	}
}

// A Config built by hand carries no format, and must still produce readable
// lines rather than empty ones.
func TestNewLoggerFallsBackToTheDefaultFormat(t *testing.T) {
	for _, tt := range []struct {
		name string
		cfg  *Config
	}{
		{name: "no format at all", cfg: &Config{}},
		{name: "a format that does not compile", cfg: &Config{LogFormat: "%(nonesuch)s"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			NewLogger(tt.cfg, &out).Infof("hello")

			if got := out.String(); !strings.HasSuffix(got, "INFO: hello\n") {
				t.Errorf("wrote %q, want a line in the default format", got)
			}
		})
	}
}

// The format is part of the configuration, so a format that cannot be rendered
// stops the process at the same point a bad interval does.
func TestLoadConfigRefusesAnUnusableLogFormat(t *testing.T) {
	_, err := LoadConfig([]string{"-log-format", "%(nonesuch)s"}, noEnv)

	if err == nil {
		t.Fatal("LoadConfig accepted a log format it cannot render")
	}
	if !strings.Contains(err.Error(), "unknown placeholder %(nonesuch)s") {
		t.Errorf("LoadConfig = %v, want it to name the placeholder it does not know", err)
	}
}
